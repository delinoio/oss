#!/usr/bin/env python3
"""Build and inspect the six release archives without publishing or signing."""
import argparse
import gzip
import hashlib
import io
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import zipfile

ROOT = Path(__file__).resolve().parents[2]


def metadata():
    m = json.loads((ROOT / "packaging/async-commit-hook/release-metadata.json").read_text())
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", m["version"]):
        raise ValueError("invalid release version")
    for package in ["apps/async-commit-hook", "apps/async-commit-hook-docs", "packages/async-commit-hook-api-client"]:
        if json.loads((ROOT / package / "package.json").read_text())["version"] != m["version"]:
            raise ValueError("source versions disagree")
    source = (ROOT / "cmds/async-commit-hook/internal/core/model.go").read_text()
    if f'const Version = "{m["version"]}"' not in source:
        raise ValueError("Go release version disagrees")
    expected = {(o, a) for o in ["darwin", "linux", "windows"] for a in ["amd64", "arm64"]}
    if {(t["os"], t["arch"]) for t in m["targets"]} != expected or len(m["targets"]) != 6:
        raise ValueError("exactly six targets are required")
    return m



def verify_tag(version, revision, remote="origin"):
    if not re.fullmatch(r"(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)", version):
        raise ValueError("invalid release version")
    if not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", revision):
        raise ValueError("release revision must be a full commit ID")
    ref = f"refs/tags/async-commit-hook@v{version}"
    # Read the remote on every call, including immediately before publication.
    # ls-remote peels annotated (including nested) tags to their final object.
    result = subprocess.run(["git", "ls-remote", "--tags", remote, ref, ref + "^{}"], check=True, capture_output=True, text=True)
    refs = {}
    for line in result.stdout.splitlines():
        oid, name = line.split("\t")
        if name not in (ref, ref + "^{}") or name in refs or not re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", oid):
            raise ValueError("unexpected release tag response")
        refs[name] = oid
    if refs and (ref not in refs or refs.get(ref + "^{}", refs[ref]) != revision):
        raise ValueError("existing release tag does not target the validated source commit")
    return {"event": "release.tag_verified", "tag": ref.removeprefix("refs/tags/"), "revision": revision, "exists": bool(refs)}


def archive_bytes(binary, name, windows):
    output = io.BytesIO()
    if windows:
        with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
            entry = zipfile.ZipInfo(name, (2026, 1, 1, 0, 0, 0))
            entry.external_attr = 0o100755 << 16
            entry.compress_type = zipfile.ZIP_DEFLATED
            archive.writestr(entry, binary)
    else:
        # Fix the compression header as well as tar metadata, so retries of
        # identical binaries reproduce the signed archive/checksum bytes.
        with gzip.GzipFile(fileobj=output, mode="wb", filename="", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as archive:
                entry = tarfile.TarInfo(name)
                entry.size, entry.mode, entry.mtime = len(binary), 0o755, 0
                archive.addfile(entry, io.BytesIO(binary))
    return output.getvalue()


def formula(version, hashes):
    base = f"https://github.com/delinoio/oss/releases/download/async-commit-hook@v{version}"
    body = ['class AsyncCommitHook < Formula', '  desc "Asynchronous exact-commit checks for people and coding agents"', '  homepage "https://ach.delino.io"', f'  version "{version}"', '  license "Apache-2.0"']
    for os_name, clause in [("darwin", "on_macos"), ("linux", "on_linux")]:
        body.append(f"  {clause} do")
        for arch, cpu in [("arm64", "arm"), ("amd64", "intel")]:
            asset = f"ach-{os_name}-{arch}.tar.gz"
            body += [f"    on_{cpu} do", f'      url "{base}/{asset}"', f'      sha256 "{hashes[asset]}"', "    end"]
        body.append("  end")
    body += ['  depends_on "git"', '  def install', '    bin.install "ach"', '  end', '  test do', '    assert_match version.to_s, shell_output("#{bin}/ach version")', '  end', 'end', '']
    return "\n".join(body)


def build(destination):
    m = metadata()
    out = Path(destination).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        raise ValueError("output directory must be empty")
    # Always rebuild the UI before compilation; never package a stale embed.
    subprocess.run(["pnpm", "--filter", "async-commit-hook", "build:embedded"], cwd=ROOT, check=True)
    hashes = {}
    with tempfile.TemporaryDirectory(prefix="ach-release-") as temporary:
        for target in m["targets"]:
            windows = target["os"] == "windows"
            name = "ach.exe" if windows else "ach"
            binary = Path(temporary) / name
            env = dict(os.environ, GOOS=target["os"], GOARCH=target["arch"], CGO_ENABLED="0")
            subprocess.run(["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", str(binary), "./cmds/async-commit-hook"], cwd=ROOT, env=env, check=True)
            asset = f'ach-{target["os"]}-{target["arch"]}' + (".zip" if windows else ".tar.gz")
            data = archive_bytes(binary.read_bytes(), name, windows)
            (out / asset).write_bytes(data)
            hashes[asset] = hashlib.sha256(data).hexdigest()
            print(json.dumps({"event": "archive.built", "target": target, "asset": asset, "sha256": hashes[asset]}), flush=True)
    (out / "async-commit-hook.rb").write_text(formula(m["version"], hashes))
    for name in ["install.sh", "install.ps1"]:
        (out / name).write_bytes((ROOT / "apps/async-commit-hook-docs/public" / name).read_bytes())
    (out / "compatibility.json").write_text(json.dumps({**m, "cross_build": "passed for all six targets", "signed": False, "published": False}, indent=2) + "\n")
    hashes = {p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(out.iterdir())}
    (out / "SHA256SUMS").write_text("".join(f"{digest}  {name}\n" for name, digest in hashes.items()))
    return out


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output")
    parser.add_argument("--validate", action="store_true")
    parser.add_argument("--verify-tag", metavar="COMMIT")
    args = parser.parse_args()
    if args.validate:
        print(json.dumps(metadata()))
    elif args.verify_tag:
        print(json.dumps(verify_tag(metadata()["version"], args.verify_tag)))
    elif args.output:
        build(args.output)
    else:
        parser.error("--output, --validate or --verify-tag is required")
