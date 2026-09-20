"""Offline publication fixtures; every gh call and tag lookup is intercepted."""
import copy
import hashlib
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("ach_publish", Path(__file__).with_name("publish-async-commit-hook.py"))
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)


class Remote:
    def __init__(self):
        self.release = None
        self.assets = []
        self.mutations = []
        self.failure = None
        self.corrupt = None
        self.tag_checks = 0
        self.tag_failure = None

    def verify_tag(self, version, revision):
        self.tag_checks += 1
        if self.tag_checks == self.tag_failure:
            raise ValueError("tag mismatch")

    def gh(self, *args, payload=None):
        if args[0] == "api":
            assert args[1:3] == ("--hostname", "github.com")
            path = args[3]
            if "--paginate" in args:
                assert args[-2:] == ("--paginate", "--slurp")
                if path == publisher.ENDPOINT + "?per_page=100":
                    if self.failure == "lookup":
                        raise subprocess.CalledProcessError(1, "fixture lookup")
                    # The owned release is deliberately on a later page.
                    return json.dumps([[{"tag_name": "unrelated", "draft": False}], [self.release] if self.release else []])
                assert path == publisher.ENDPOINT + "/7/assets?per_page=100"
                return json.dumps([self.assets[:5], self.assets[5:]])
            if "--method" in args:
                assert args[-2:] == ("--input", "-")
                body = json.loads(payload)
                if args[5] == "POST":
                    assert path == publisher.ENDPOINT and self.release is None and body["draft"] is True
                    self.release = {**body, "id": 7}
                    self.mutations.append("create-draft")
                    if self.failure == "create-response":
                        raise subprocess.CalledProcessError(1, "fixture response lost")
                else:
                    assert args[5] == "PATCH" and path == publisher.ENDPOINT + "/7" and body == {"draft": False}
                    self.release.update(body)
                    self.mutations.append("publish")
                return json.dumps(self.release)
            assert path == publisher.ENDPOINT + "/7"
            return json.dumps(self.release)
        assert args[:2] == ("release", "upload")
        assert args[-3:] == ("--repo", "github.com/delinoio/oss", "--clobber")
        assert self.release["draft"] is True
        self.mutations.append("upload")
        self.assets = []
        for filename in args[3:-3]:
            path = Path(filename)
            data = path.read_bytes()
            self.assets.append({"name": path.name, "size": len(data), "digest": "sha256:" + hashlib.sha256(data).hexdigest(), "state": "uploaded"})
            if self.failure == "upload":
                raise subprocess.CalledProcessError(1, "fixture partial upload")
        if self.corrupt == "missing":
            self.assets.pop()
        elif self.corrupt == "duplicate":
            self.assets[-1] = copy.deepcopy(self.assets[0])
        elif self.corrupt == "extra":
            self.assets.append({"name": "unexpected"})
        elif self.corrupt:
            self.assets[0][self.corrupt] = None
        return ""


class PublicationTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="ach-publish-fixture-")
        self.addCleanup(self.temporary.cleanup)
        self.directory = Path(self.temporary.name)
        metadata = publisher.build.metadata()
        self.version = metadata["version"]
        self.revision = "a" * 40
        names = [f'ach-{target["os"]}-{target["arch"]}' + (".zip" if target["os"] == "windows" else ".tar.gz") for target in metadata["targets"]]
        names += ["install.sh", "install.ps1", "async-commit-hook.rb", "compatibility.json", "SHA256SUMS"]
        for name in names:
            (self.directory / name).write_text("fixture " + name)
            (self.directory / (name + ".sigstore.json")).write_text("fixture signature " + name)
        self.remote = Remote()
        self.addCleanup(patch.stopall)
        patch.object(publisher, "gh", self.remote.gh).start()
        patch.object(publisher.build, "verify_tag", self.remote.verify_tag).start()

    def publish(self, run_id="123"):
        publisher.publish(self.directory, self.version, self.revision, run_id)

    def incomplete(self):
        self.remote.failure = "upload"
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertTrue(self.remote.release["draft"])
        self.assertEqual(len(self.remote.assets), 1)
        self.remote.failure = None

    def test_fresh_publication_and_immutable_retry(self):
        self.publish()
        self.assertFalse(self.remote.release["draft"])
        self.assertEqual(len(self.remote.assets), 22)
        self.assertEqual(self.remote.tag_checks, 2)
        self.assertEqual(self.remote.mutations, ["create-draft", "upload", "publish"])
        with self.assertRaisesRegex(ValueError, "immutable"):
            self.publish()
        self.assertEqual(self.remote.mutations, ["create-draft", "upload", "publish"])

    def test_retry_partial_upload_replaces_only_owned_draft_assets(self):
        self.incomplete()
        bundle = self.directory / "SHA256SUMS.sigstore.json"
        bundle.write_text("new signature from the same workflow run retry")
        self.publish()
        self.assertEqual(self.remote.mutations, ["create-draft", "upload", "upload", "publish"])
        self.assertFalse(self.remote.release["draft"])
        self.assertEqual(len(self.remote.assets), 22)
        uploaded = next(asset for asset in self.remote.assets if asset["name"] == bundle.name)
        self.assertEqual(uploaded["digest"], "sha256:" + hashlib.sha256(bundle.read_bytes()).hexdigest())

    def test_creation_response_loss_is_resumable(self):
        self.remote.failure = "create-response"
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.remote.failure = None
        self.publish()
        self.assertEqual(self.remote.mutations, ["create-draft", "upload", "publish"])

    def test_unowned_or_changed_drafts_are_untouched(self):
        self.incomplete()
        saved = copy.deepcopy(self.remote.release)
        before = self.remote.mutations[:]
        for field, value in [("body", "manual draft"), ("target_commitish", "b" * 40), ("name", "modified"), ("draft", False), ("id", "7"), ("prerelease", True)]:
            with self.subTest(field=field):
                self.remote.release = {**saved, field: value}
                with self.assertRaises(ValueError):
                    self.publish()
                self.assertEqual(self.remote.mutations, before)
        self.remote.release = saved
        with self.assertRaisesRegex(ValueError, "not owned"):
            self.publish(run_id="124")
        self.assertEqual(self.remote.mutations, before)
        self.remote.assets.append({"name": "maintainer-extra"})
        with self.assertRaisesRegex(ValueError, "unexpected assets"):
            self.publish()
        self.assertEqual(self.remote.mutations, before)

    def test_incomplete_or_mismatched_uploads_never_publish(self):
        self.incomplete()
        for corruption in ["missing", "duplicate", "extra", "digest", "size", "state"]:
            with self.subTest(corruption=corruption):
                self.remote.assets = []
                self.remote.corrupt = corruption
                with self.assertRaisesRegex(ValueError, "incomplete or do not match"):
                    self.publish()
                self.assertTrue(self.remote.release["draft"])
                self.assertNotIn("publish", self.remote.mutations)

    def test_lookup_and_tag_errors_fail_closed(self):
        self.remote.failure = "lookup"
        with self.assertRaises(subprocess.CalledProcessError):
            self.publish()
        self.assertEqual(self.remote.mutations, [])
        self.remote.failure = None
        self.remote.tag_failure = 1
        with self.assertRaisesRegex(ValueError, "tag mismatch"):
            self.publish()
        self.assertEqual(self.remote.mutations, [])
        self.remote.tag_failure = 3
        with self.assertRaisesRegex(ValueError, "tag mismatch"):
            self.publish()
        self.assertEqual(self.remote.mutations, ["create-draft", "upload"])
        self.assertTrue(self.remote.release["draft"])

    def test_invalid_local_artifact_set_cannot_mutate_release(self):
        for kind in ["missing", "extra", "symlink"]:
            with self.subTest(kind=kind):
                bundle = self.directory / "SHA256SUMS.sigstore.json"
                original = bundle.read_bytes()
                extra = self.directory / "extra"
                if kind == "extra":
                    extra.write_text("extra")
                else:
                    bundle.unlink()
                    if kind == "symlink":
                        bundle.symlink_to(self.directory / "SHA256SUMS")
                with self.assertRaisesRegex(ValueError, "complete signed artifact set"):
                    self.publish()
                self.assertEqual(self.remote.mutations, [])
                extra.unlink(missing_ok=True)
                bundle.unlink(missing_ok=True)
                bundle.write_bytes(original)


if __name__ == "__main__":
    unittest.main()
