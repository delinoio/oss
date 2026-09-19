#!/usr/bin/env python3
"""Publish signed ach artifacts, resuming only this workflow run's owned draft."""
import argparse
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("ach_build", Path(__file__).with_name("build-async-commit-hook.py"))
build = importlib.util.module_from_spec(spec)
spec.loader.exec_module(build)
REPOSITORY = "delinoio/oss"
ENDPOINT = f"repos/{REPOSITORY}/releases"


def gh(*args, payload=None):
    result = subprocess.run(["gh", *args], input=payload, check=True, capture_output=True, text=True)
    return result.stdout


def api(path, payload=None):
    args = ["api", "--hostname", "github.com", path]
    if payload is not None:
        args += ["--method", "POST" if path == ENDPOINT else "PATCH", "--input", "-"]
    return json.loads(gh(*args, payload=None if payload is None else json.dumps(payload)))


def pages(path):
    return [item for page in json.loads(gh("api", "--hostname", "github.com", path, "--paginate", "--slurp")) for item in page]


def asset_inventory(directory, metadata):
    names = {f'ach-{target["os"]}-{target["arch"]}' + (".zip" if target["os"] == "windows" else ".tar.gz") for target in metadata["targets"]}
    names.update({"install.sh", "install.ps1", "async-commit-hook.rb", "compatibility.json", "SHA256SUMS"})
    names.update({name + ".sigstore.json" for name in list(names)})
    files = {path.name: path for path in Path(directory).resolve().iterdir()}
    if set(files) != names or any(path.is_symlink() or not path.is_file() for path in files.values()):
        raise ValueError("publication requires the complete signed artifact set with no extra paths")
    inventory = {}
    for name, path in sorted(files.items()):
        digest = hashlib.sha256()
        with path.open("rb") as source:
            for block in iter(lambda: source.read(1024 * 1024), b""):
                digest.update(block)
        inventory[name] = {"name": name, "size": path.stat().st_size, "digest": "sha256:" + digest.hexdigest(), "state": "uploaded"}
    return files, inventory


def require_owned_draft(release, expected, release_id=None):
    if release.get("draft") is not True:
        raise ValueError("release already published; assets are immutable")
    if any(release.get(key) != value for key, value in expected.items()):
        raise ValueError("draft is not owned by this workflow run and validated commit")
    identity = release.get("id")
    if type(identity) is not int or identity <= 0 or (release_id is not None and identity != release_id):
        raise ValueError("release identity changed")
    return identity


def publish(directory, version, revision, run_id):
    metadata = build.metadata()
    if version != metadata["version"] or not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", revision) or not re.fullmatch(r"[1-9][0-9]*", run_id):
        raise ValueError("publication requires the committed version, full revision and workflow run ID")
    files, inventory = asset_inventory(directory, metadata)
    tag = f"async-commit-hook@v{version}"
    owner = json.dumps({"workflow": "release-async-commit-hook.yml", "repository": REPOSITORY, "run_id": run_id, "revision": revision}, sort_keys=True, separators=(",", ":"))
    expected = {
        "tag_name": tag, "target_commitish": revision, "name": f"async-commit-hook {version}", "draft": True, "prerelease": False,
        "body": "Signed ach archives for six targets. See compatibility.json for validation scope and https://ach.delino.io/docs/ for installation and recovery.\n\n<!-- ach-release-owner " + owner + " -->",
    }
    matches = [release for release in pages(ENDPOINT + "?per_page=100") if release.get("tag_name") == tag]
    if len(matches) > 1:
        raise ValueError("multiple releases use the version tag")
    build.verify_tag(version, revision)
    release = matches[0] if matches else api(ENDPOINT, expected)
    release_id = require_owned_draft(release, expected)
    endpoint = f"{ENDPOINT}/{release_id}"
    # Ownership is reread before replacing incomplete uploads. Completed releases,
    # unrelated drafts and unexpected assets are never clobbered by a retry.
    require_owned_draft(api(endpoint), expected, release_id)
    existing = pages(endpoint + "/assets?per_page=100")
    if any(asset.get("name") not in inventory for asset in existing):
        raise ValueError("owned draft contains unexpected assets; maintainer review required")
    gh("release", "upload", tag, *[str(files[name]) for name in sorted(files)], "--repo", "github.com/" + REPOSITORY, "--clobber")
    require_owned_draft(api(endpoint), expected, release_id)
    uploaded = pages(endpoint + "/assets?per_page=100")
    if len(uploaded) != len(inventory) or any({key: asset.get(key) for key in ("name", "size", "digest", "state")} != inventory.get(asset.get("name")) for asset in uploaded) or {asset.get("name") for asset in uploaded} != set(inventory):
        raise ValueError("uploaded artifacts are incomplete or do not match signed local bytes")
    build.verify_tag(version, revision)
    published = api(endpoint, {"draft": False})
    if published.get("id") != release_id or published.get("draft") is not False:
        raise ValueError("publication response did not confirm the owned release")
    print(json.dumps({"event": "release.published", "tag": tag, "release_id": release_id, "resumed": bool(matches)}))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--assets", required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--run-id", required=True)
    args = parser.parse_args()
    publish(args.assets, args.version, args.revision, args.run_id)
