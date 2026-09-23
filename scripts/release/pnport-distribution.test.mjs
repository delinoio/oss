import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import { buildPackage, nativeArchive, packageNames, tarEntries } from "../../packages/pnport/scripts/package.mjs";
import { publishArtifacts } from "../../packages/pnport/scripts/publish.mjs";
import { publish as publishGithub } from "../../packages/pnport/scripts/github-release.mjs";
import { metadata } from "../../packages/pnport/scripts/common.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
const require = createRequire(import.meta.url);
const { targets, selectTarget } = require("../../packages/pnport/src/platforms.cjs");
const revision = "a".repeat(40);
const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");
function fixture(t) {
  const directory = mkdtempSync(path.join(tmpdir(), "pnport-release-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}

test("launcher tarball has exact source text, version and executable mode", (t) => {
  const output = fixture(t);
  const artifact = buildPackage({ output, sourceRevision: revision });
  assert.equal(artifact.version, metadata().version);
  const files = tarEntries(readFileSync(path.join(output, "tarballs", artifact.filename)));
  assert.equal(files.get("package.json")?.mode & 0o111, 0);
  assert.equal(files.get("bin/pnport.cjs")?.mode & 0o111, 0o111);
  assert.deepEqual(JSON.parse(files.get("package.json").bytes).optionalDependencies, Object.fromEntries(targets.map(({ name }) => [name, metadata().version])));
});

test("native release archive contains exactly one matched adjacent pair", () => {
  const target = targets[0];
  const archive = tarEntries(nativeArchive(target, Buffer.from("binary"), Buffer.from("preload")), "");
  assert.deepEqual([...archive.keys()].sort(), [target.binary, "libpnport_preload.dylib"].sort());
  assert.equal(archive.get(target.binary).mode & 0o111, 0o111);
  assert.equal(archive.get("libpnport_preload.dylib").bytes.toString(), "preload");
});

test("npm publication confirms six native dependencies before launcher and fails before writing on conflict", async () => {
  assert.deepEqual(packageNames("0.1.0"), [...targets.map(({ name }) => `delino-${name.split("/")[1]}-0.1.0.tgz`), "delino-pnport-0.1.0.tgz"]);
  const artifacts = [...targets.map(({ name }) => ({ name, version: "0.1.0", integrity: name })), { name: "@delino/pnport", version: "0.1.0", integrity: "main" }];
  const remote = new Map();
  const writes = [];
  const options = { dryRun: false, lookup: async ({ name }) => remote.get(name) ?? null, publish: async (artifact) => { writes.push(artifact.name); remote.set(artifact.name, artifact.integrity); }, delay: async () => {}, report: () => {} };
  await publishArtifacts(artifacts, options);
  assert.deepEqual(writes, artifacts.map(({ name }) => name));
  await publishArtifacts(artifacts, options);
  assert.equal(writes.length, 7);
  remote.set(artifacts[5].name, "conflicting-bytes");
  await assert.rejects(() => publishArtifacts(artifacts, options), /Conflicting npm integrity/u);
  assert.equal(writes.length, 7);
  await publishArtifacts(artifacts, { ...options, dryRun: true });
  assert.equal(writes.length, 7);
});

test("an older pnport retry cannot downgrade the Homebrew tap", (t) => {
  const directory = fixture(t);
  const remote = path.join(directory, "tap.git");
  const checkout = path.join(directory, "tap");
  execFileSync("git", ["init", "--bare", "--initial-branch=main", remote], { stdio: "pipe" });
  execFileSync("git", ["clone", remote, checkout], { stdio: "pipe" });
  execFileSync("git", ["config", "user.name", "Fixture"], { cwd: checkout });
  execFileSync("git", ["config", "user.email", "fixture@example.invalid"], { cwd: checkout });
  mkdirSync(path.join(checkout, "Formula"));
  const current = 'class Pnport < Formula\n  version "0.2.0"\nend\n';
  writeFileSync(path.join(checkout, "Formula/pnport.rb"), current);
  execFileSync("git", ["add", "Formula/pnport.rb"], { cwd: checkout });
  execFileSync("git", ["commit", "-m", "newer pnport release"], { cwd: checkout, stdio: "pipe" });
  execFileSync("git", ["push", "origin", "HEAD:main"], { cwd: checkout, stdio: "pipe" });
  const args = ["scripts/release/update-homebrew.sh", "--project", "pnport", "--version", "0.1.0", "--tap-repo", "fixture/pnport-tap"];
  for (const platform of ["darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"]) {
    args.push(`--${platform}-url`, `https://example.invalid/pnport-${platform}.tar.gz`, `--${platform}-sha256`, "a".repeat(64));
  }
  const env = { ...process.env, GH_TOKEN: "fixture", GIT_CONFIG_COUNT: "1", GIT_CONFIG_KEY_0: "url.file://" + remote + ".insteadOf", GIT_CONFIG_VALUE_0: "https://github.com/fixture/pnport-tap.git" };
  const result = spawnSync("bash", args, { cwd: root, env, encoding: "utf8" });
  assert.notEqual(result.status, 0, "older release unexpectedly updated the tap");
  assert.match(result.stderr, /refusing pnport Homebrew downgrade from 0\.2\.0 to 0\.1\.0/u);
  assert.equal(execFileSync("git", ["show", "main:Formula/pnport.rb"], { cwd: remote, encoding: "utf8" }), current);
});

test("signed GitHub release resumes an identical partial draft and rejects conflicting bytes", async () => {
  const plan = { project: "pnport", version: "0.1.0", revision, tag: "pnport@v0.1.0" };
  const files = new Map([["pnport-darwin-arm64.tar.gz", Buffer.from("native archive")]]);
  const release = { id: 7, tag_name: plan.tag, target_commitish: revision, draft: true, prerelease: false, assets: [] };
  const bytes = new Map();
  const uploads = [];
  let interrupt = true;
  const deps = {
    api: async (method, route, body) => {
      if (route.includes("/git/ref/tags/")) return { object: { type: "commit", sha: revision } };
      if (route.includes("/releases/tags/")) return release.assets.length ? release : null;
      if (method === "POST") return release;
      if (method === "PATCH") { release.draft = body.draft; return release; }
      return release;
    },
    download: async ({ name }) => bytes.get(name),
    upload: async (_release, name, value) => {
      if (name.endsWith(".sigstore.json") && interrupt) { interrupt = false; throw new Error("interrupted upload"); }
      uploads.push(name);
      release.assets.push({ name });
      bytes.set(name, Buffer.from(value));
    },
    sign: async () => Buffer.from("signed bundle"),
    verify: async (_name, value, bundle) => { assert.ok(value.length && bundle.length); },
    report: () => {},
  };
  await assert.rejects(publishGithub({ plan, files }, deps), /interrupted upload/u);
  assert.equal(release.draft, true);
  assert.deepEqual(uploads, ["pnport-darwin-arm64.tar.gz"]);
  await publishGithub({ plan, files }, deps);
  assert.equal(release.draft, false);
  assert.deepEqual(uploads, ["pnport-darwin-arm64.tar.gz", "pnport-darwin-arm64.tar.gz.sigstore.json"]);
  await publishGithub({ plan, files }, deps);
  assert.equal(uploads.length, 2);
  await assert.rejects(publishGithub({ plan, files: new Map([["pnport-darwin-arm64.tar.gz", Buffer.from("other bytes")]]) }, deps), /Conflicting immutable asset/u);
  assert.equal(uploads.length, 2);
});

test("POSIX installer verifies the complete archive before changing the active version", (t) => {
  const target = selectTarget();
  if (!target || target.os === "win32") return t.skip("POSIX host required");
  const temporary = fixture(t);
  const release = path.join(temporary, "release");
  const install = path.join(temporary, "bin");
  mkdirSync(release);
  const binary = Buffer.from("#!/bin/sh\nprintf 'pnport 0.0.0\\n'\n");
  const archive = nativeArchive(target, binary, Buffer.from("companion"));
  const name = `pnport-${target.suffix}.tar.gz`;
  writeFileSync(path.join(release, name), archive);
  writeFileSync(path.join(release, "SHA256SUMS"), `${sha256(archive)}  ${name}\n`);
  const command = [path.join(root, "scripts/install/pnport.sh"), "--version", "0.0.0", "--source-dir", release, "--install-dir", install];
  const installed = spawnSync("bash", command, { encoding: "utf8" });
  assert.equal(installed.status, 0, installed.stderr);
  assert.equal(execFileSync(path.join(install, "pnport"), ["--version"], { encoding: "utf8" }).trim(), "pnport 0.0.0");
  assert.equal(readFileSync(path.join(install, ".pnport/versions/0.0.0", target.os === "darwin" ? "libpnport_preload.dylib" : "libpnport_preload.so"), "utf8"), "companion");
  writeFileSync(path.join(release, "SHA256SUMS"), `${"0".repeat(64)}  ${name}\n`);
  const rejected = spawnSync("bash", command, { encoding: "utf8" });
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /checksum mismatch/u);
  assert.equal(execFileSync(path.join(install, "pnport"), ["--version"], { encoding: "utf8" }).trim(), "pnport 0.0.0");
});
