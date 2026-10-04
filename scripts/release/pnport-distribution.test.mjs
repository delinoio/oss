import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";
import { buildPackage, inspectTarball, nativeArchive, packageNames, tarEntries } from "../../packages/pnport/scripts/package.mjs";
import { publishArtifacts, registryTags, registryPreviewTags, validatePreviewTags } from "../../packages/pnport/scripts/publish.mjs";
import { findRelease, publish as publishGithub } from "../../packages/pnport/scripts/github-release.mjs";
import { metadata, npm, requireReleaseReady, requirePublicationReady, sourceText } from "../../packages/pnport/scripts/common.mjs";
import { companion, nativeManifest } from "../../packages/pnport/scripts/manifests.mjs";
import { isPreviewVersion, publicationChannel } from "../../packages/pnport/scripts/version.mjs";

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

test("Homebrew pnport formula declares the macOS 15 floor and version test", () => {
  let formula = readFileSync(path.join(root, "packaging/homebrew/templates/pnport.rb.tmpl"), "utf8");
  const replacements = { __VERSION__: "0.1.0" };
  for (const platform of ["darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"]) {
    const key = platform.replace("-", "_").toUpperCase();
    replacements[`__${key}_URL__`] = `https://example.invalid/pnport-${platform}.tar.gz`;
    replacements[`__${key}_SHA256__`] = "a".repeat(64);
  }
  for (const [placeholder, value] of Object.entries(replacements)) formula = formula.replaceAll(placeholder, value);
  assert.doesNotMatch(formula, /__[A-Z0-9_]+__/u);
  assert.match(formula, /on_macos do\n    depends_on macos: :sequoia/u);
  assert.match(formula, /test do\n    assert_match version\.to_s, shell_output\("#\{bin\}\/pnport --version"\)/u);
});

test("launcher tarball has exact source text, version and executable mode", (t) => {
  const output = fixture(t);
  const artifact = buildPackage({ output, sourceRevision: revision });
  assert.equal(artifact.version, metadata().version);
  const files = tarEntries(readFileSync(path.join(output, "tarballs", artifact.filename)));
  assert.equal(files.get("package.json")?.mode & 0o111, 0);
  assert.equal(files.get("bin/pnport.cjs")?.mode & 0o111, 0o111);
  assert.deepEqual(JSON.parse(files.get("package.json").bytes).optionalDependencies, Object.fromEntries(targets.map(({ name }) => [name, metadata().version])));
});

test("preview launcher and native tarballs retain exact version and immutable identity", (t) => {
  const output = fixture(t);
  const version = "0.1.0-next.1";
  const read = (file) => {
    const source = sourceText(file);
    if (file === "packages/pnport/package.json") return JSON.stringify({ ...JSON.parse(source), version });
    return source.replace(/^version = "[^"]+"$/mu, `version = "${version}"`);
  };
  const launcher = buildPackage({ output, sourceRevision: revision, read });
  assert.equal(launcher.version, version);
  const packed = JSON.parse(tarEntries(readFileSync(path.join(output, "tarballs", launcher.filename))).get("package.json").bytes);
  assert.deepEqual(packed.optionalDependencies, Object.fromEntries(targets.map(({ name }) => [name, version])));
  assert.equal(packed.gitHead, revision);
  const target = selectTarget();
  if (target?.os !== "win32" && target) {
    const binary = path.join(output, "native-fixture");
    const preload = path.join(output, "preload-fixture");
    writeFileSync(binary, `#!/bin/sh\nprintf 'pnport ${version}\\n'\n`);
    chmodSync(binary, 0o755);
    writeFileSync(preload, target.os === "darwin" ? "PNPORT_PRELOAD_0.1.0_FORMAT_3_READY" : "PNPORT_PRELOAD_0.1.0_FORMAT_1_READY");
    const native = buildPackage({ target, binary, preload, output, sourceRevision: revision, read });
    assert.equal(native.version, version);
    assert.equal(inspectTarball(path.join(output, "tarballs", native.filename), version, revision).integrity, native.integrity);
  }
  assert.throws(() => buildPackage({ output, sourceRevision: revision, read: (file) => read(file).replaceAll(version, "0.1.0-next.01") }), /Unsupported/u);
});

test("macOS native package inspection rejects companions without constructor leases", (t) => {
  if (process.platform === "win32") {
    t.skip("This macOS tarball fixture requires POSIX executable modes");
    return;
  }
  const output = fixture(t);
  const { version } = metadata();
  for (const target of targets.filter(({ os }) => os === "darwin")) {
    const directory = path.join(output, target.suffix);
    mkdirSync(path.join(directory, "bin"), { recursive: true });
    const value = nativeManifest(target.suffix, version, revision);
    value.repository = { ...value.repository, directory: "packages/pnport" };
    value.publishConfig = { access: "public", registry: "https://registry.npmjs.org" };
    value.files = [`bin/${target.binary}`, `bin/${companion(target)}`, "README.md", "LICENSE", "LICENSE.fspy"];
    writeFileSync(path.join(directory, "package.json"), JSON.stringify(value));
    for (const [name, source] of [["README.md", "packages/pnport/README.md"], ["LICENSE", "crates/pnport/LICENSE"], ["LICENSE.fspy", "crates/fspy/LICENSE"]]) {
      writeFileSync(path.join(directory, name), sourceText(source));
    }
    const binary = path.join(directory, "bin", target.binary);
    const preload = path.join(directory, "bin", companion(target));
    writeFileSync(binary, "native-inspection-fixture");
    chmodSync(binary, 0o755);
    // Inspect fixture bytes on any Unix host without claiming a native build
    // or bypassing buildPackage's actual target-host restriction.
    const pack = () => {
      const [packed] = JSON.parse(npm(["pack", "--json", "--ignore-scripts", "--pack-destination", output], { cwd: directory }));
      return path.join(output, packed.filename);
    };
    writeFileSync(preload, "PNPORT_PRELOAD_0.1.0_FORMAT_1_READY");
    assert.throws(() => inspectTarball(pack(), version, revision), /companion ABI mismatch/u);
    writeFileSync(preload, "PNPORT_PRELOAD_0.1.0_FORMAT_2_READY");
    assert.throws(() => inspectTarball(pack(), version, revision), /companion ABI mismatch/u);
    writeFileSync(preload, "PNPORT_PRELOAD_0.1.0_FORMAT_3_READY");
    assert.equal(inspectTarball(pack(), version, revision).name, target.name);
  }
});

test("native release archives contain one matched pair and exact license notices", () => {
  const pnportLicense = readFileSync(path.join(root, "crates/pnport/LICENSE"));
  const fspyLicense = readFileSync(path.join(root, "crates/fspy/LICENSE"));
  for (const target of targets) {
    const archive = tarEntries(nativeArchive(target, Buffer.from("binary"), Buffer.from("preload")), "");
    const companion = target.os === "darwin" ? "libpnport_preload.dylib" : target.os === "win32" ? "pnport_preload.dll" : "libpnport_preload.so";
    const notices = target.os === "win32" ? ["LICENSE"] : ["LICENSE", "LICENSE.fspy"];
    assert.deepEqual([...archive.keys()].sort(), [target.binary, companion, ...notices].sort());
    assert.equal(archive.get(target.binary).mode & 0o111, target.os === "win32" ? 0 : 0o111);
    assert.equal(archive.get(companion).bytes.toString(), "preload");
    assert.ok(archive.get("LICENSE").bytes.equals(pnportLicense));
    if (target.os !== "win32") assert.ok(archive.get("LICENSE.fspy").bytes.equals(fspyLicense));
  }
});

test("native install smoke executes the activated POSIX launcher", { skip: process.platform === "win32" }, (t) => {
  const output = fixture(t);
  const target = targets.find(({ os, cpu }) => os === process.platform && cpu === process.arch);
  assert.ok(target, "Current host needs a pnport target");
  const archives = path.join(output, "archives");
  mkdirSync(archives);
  const version = metadata().version;
  writeFileSync(path.join(archives, `pnport-${target.suffix}.tar.gz`), nativeArchive(target, Buffer.from(`#!/bin/sh\nprintf 'pnport ${version}\\n'\n`), Buffer.from("companion")));
  const result = spawnSync("node", ["packages/pnport/scripts/install-smoke.mjs", "--target", target.rust, "--directory", output], { cwd: root, encoding: "utf8" });
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
  assert.match(result.stdout, /"event":"pnport_install_smoke"/u);
});

test("POSIX installation rejects old macOS before discovery or filesystem mutation", { skip: process.platform === "win32" }, (t) => {
  const output = fixture(t);
  const tools = path.join(output, "tools");
  mkdirSync(tools);
  const sentinel = path.join(output, "unexpected-side-effect");
  for (const command of ["curl", "mktemp", "mkdir", "cp", "tar"]) {
    writeFileSync(path.join(tools, command), `#!/bin/sh\n: > '${sentinel}'\nexit 99\n`);
    chmodSync(path.join(tools, command), 0o755);
  }
  const install = path.join(output, "install");
  for (const kernel of ["22.6.0", "23.6.0", "unknown"]) {
    writeFileSync(path.join(tools, "uname"), `#!/bin/sh\ncase "$1" in -s) echo Darwin;; -r) echo '${kernel}';; -m) echo arm64;; esac\n`);
    chmodSync(path.join(tools, "uname"), 0o755);
    for (const version of ["latest", "0.1.0"]) {
      const result = spawnSync("/bin/bash", [path.join(root, "scripts/install/pnport.sh"), "--version", version, "--install-dir", install], { env: { ...process.env, PATH: tools }, encoding: "utf8" });
      assert.equal(result.status, 1);
      assert.match(result.stderr, /macOS 15 or newer is required/u);
      assert.throws(() => readFileSync(sentinel), { code: "ENOENT" });
      assert.throws(() => readFileSync(install), { code: "ENOENT" });
    }
  }
});

test("npm publication confirms four native dependencies before launcher and fails before writing on conflict", async () => {
  assert.deepEqual(packageNames("0.1.0"), [...targets.map(({ name }) => `delino-${name.split("/")[1]}-0.1.0.tgz`), "delino-pnport-0.1.0.tgz"]);
  const artifacts = [...targets.map(({ name }) => ({ name, version: "0.1.0", integrity: name })), { name: "@delino/pnport", version: "0.1.0", integrity: "main" }];
  const remote = new Map();
  const writes = [];
  const options = { dryRun: false, lookup: async ({ name }) => remote.get(name) ?? null, publish: async (artifact) => { writes.push(artifact.name); remote.set(artifact.name, artifact.integrity); }, delay: async () => {}, report: () => {} };
  await publishArtifacts(artifacts, options);
  assert.deepEqual(writes, artifacts.map(({ name }) => name));
  await publishArtifacts(artifacts, options);
  assert.equal(writes.length, 5);
  remote.set(artifacts[targets.length - 1].name, "conflicting-bytes");
  await assert.rejects(() => publishArtifacts(artifacts, options), /Conflicting npm integrity/u);
  assert.equal(writes.length, 5);
  await publishArtifacts(artifacts, { ...options, dryRun: true });
  assert.equal(writes.length, 5);
});

test("publication rejects incomplete, duplicated, extra, or misordered native sets before registry access", async () => {
  const artifacts = [...targets.map(({ name }) => ({ name })), { name: "@delino/pnport" }];
  const candidates = [artifacts.slice(1), [...artifacts, { name: "@delino/pnport-win32-x64-msvc" }], [artifacts.at(-1), ...artifacts.slice(0, -1)], [artifacts[0], artifacts[0], ...artifacts.slice(2)]];
  for (const candidate of candidates) {
    await assert.rejects(() => publishArtifacts(candidate, { dryRun: false, lookup: () => assert.fail("Incomplete inputs reached the registry"), publish: () => assert.fail("Incomplete inputs were published") }), /complete native package set/u);
  }
});

test("publication stays blocked until reviewed release acceptance, independently of source version preparation", () => {
  // Exercise both states independently of the reviewed gate in the live source.
  // A legitimate release preparation must not invalidate this contract test.
  for (const pnportReleaseReady of [undefined, false, "true"]) {
    assert.throws(() => requireReleaseReady(() => JSON.stringify({ version: "0.1.0", pnportReleaseReady })), /publication is blocked/u);
  }
  for (const version of [undefined, "", "0.0.0", "0.1.0-preview", "01.0.0"]) {
    assert.throws(() => requireReleaseReady(() => JSON.stringify({ version, pnportReleaseReady: true })), /publication is blocked/u);
  }
  requireReleaseReady(() => JSON.stringify({ version: "0.1.0", pnportReleaseReady: true }));
});

test("only the exact authorized next version bypasses stable acceptance", () => {
  const source = { version: "0.1.0-next.1", pnportPreviewVersion: "0.1.0-next.1", pnportReleaseReady: false };
  assert.deepEqual(requirePublicationReady(() => JSON.stringify(source)), { channel: "next", prerelease: true });
  assert.throws(() => requireReleaseReady(() => JSON.stringify(source)), /publication is blocked/u);
  for (const pnportPreviewVersion of [undefined, true, "0.1.0-next.2", "0.1.0"]) assert.throws(() => requirePublicationReady(() => JSON.stringify({ ...source, pnportPreviewVersion })), /exact reviewed/u);
  assert.throws(() => requirePublicationReady(() => JSON.stringify({ ...source, version: "0.1.0" })), /publication is blocked/u);
  for (const version of ["0.1.0-rc.1", "0.1.0-next.0", "0.1.0-next.01", "0.2.0-next.1", "0.1.0-next.1+build", "0.1.0-next.1\n", "0.1.0-next.18446744073709551616"]) {
    assert.equal(isPreviewVersion(version), false);
    assert.throws(() => publicationChannel(version), /Unsupported/u);
  }
});

test("preview npm tags preserve latest and reject a newer or foreign next", async () => {
  for (const tags of [{}, { next: "0.1.0-next.1" }, { latest: "0.1.0", next: "0.1.0-next.1" }]) validatePreviewTags(tags, "0.1.0-next.2");
  for (const tags of [{ next: "0.1.0-next.3" }, { next: "0.2.0" }, { latest: "0.1.0-next.1" }]) assert.throws(() => validatePreviewTags(tags, "0.1.0-next.2"));
  assert.deepEqual(await registryTags("@delino/pnport", async () => ({ status: 404 })), {});
  await assert.rejects(registryTags("@delino/pnport", async () => ({ ok: true, json: async () => ({ name: "@delino/pnport", "dist-tags": [] }) })), /Invalid npm/u);
});

test("preview bootstrap preserves only the inspected npm placeholder and still rejects foreign next", async () => {
  const name = "@delino/pnport";
  const tags = { latest: "0.0.0-stage", bootstrap: "0.0.0-stage" };
  const placeholder = { name, version: "0.0.0-stage", stub: true, description: "Temporary package placeholder for staged publishing", dist: { integrity: "sha512-2rsp47hGeDF0hUQ6N5HDSmzVVt4249qECxd9nDqOZHJ/WUDW3UyjmjgPWr4H1MZMRTz14fzLRClmgQ0opekvAw==" } };
  const request = (selectedTags, selectedPlaceholder, status = 200) => async (url, options) => {
    assert.equal(options.redirect, "error");
    return { ok: status === 200, status, json: async () => ({ name, "dist-tags": selectedTags, versions: { "0.0.0-stage": selectedPlaceholder } }) };
  };
  assert.deepEqual(await registryPreviewTags(name, "0.1.0-next.1", request(tags, placeholder)), tags);
  // The generic channel check must never accept an unverified prerelease latest.
  assert.throws(() => validatePreviewTags(tags, "0.1.0-next.1"));
  for (const change of [{ name: "@delino/other" }, { version: "0.0.0" }, { stub: false }, { description: "Custom placeholder" }, { dist: {} }, { dist: { integrity: "sha512-foreign" } }]) {
    await assert.rejects(registryPreviewTags(name, "0.1.0-next.1", request(tags, { ...placeholder, ...change })), /Unrecognized npm bootstrap/u);
  }
  await assert.rejects(registryPreviewTags(name, "0.1.0-next.1", request(tags, placeholder, 500)), /npm channel inspection failed/u);
  for (const next of ["0.1.0-next.2", "0.2.0", "0.1.0-rc.1"]) await assert.rejects(registryPreviewTags(name, "0.1.0-next.1", request({ ...tags, next }, placeholder)), /newer or foreign/u);
  assert.deepEqual(await registryPreviewTags(name, "0.1.0-next.1", request({ ...tags, next: "0.1.0-next.1" }, placeholder)), { ...tags, next: "0.1.0-next.1" });
  const foreignName = "@delino/other";
  await assert.rejects(registryPreviewTags(foreignName, "0.1.0-next.1", async url => ({ ok: true, status: 200, json: async () => ({ name: foreignName, "dist-tags": tags, versions: { "0.0.0-stage": { ...placeholder, name: foreignName } } }) })), /Unrecognized npm bootstrap/u);
});

test("ordinary preview channels need no bootstrap exception", async () => {
  const name = "@delino/pnport";
  for (const tags of [{}, { latest: "0.1.0" }]) {
    let requests = 0;
    const result = await registryPreviewTags(name, "0.1.0-next.1", async () => { requests++; return { ok: true, json: async () => ({ name, "dist-tags": tags }) }; });
    assert.deepEqual(result, tags);
    assert.equal(requests, 1);
  }
  await assert.rejects(registryPreviewTags(name, "0.1.0-next.1", async () => ({ ok: true, json: async () => ({ name, "dist-tags": { latest: "0.1.0-next.1" } }) })));
});

test("native channel confirmation failure prevents launcher publication", async () => {
  const artifacts = [...targets.map(({ name }) => ({ name, version: "0.1.0-next.1", integrity: name })), { name: "@delino/pnport", version: "0.1.0-next.1", integrity: "main" }];
  const remote = new Map();
  const writes = [];
  await assert.rejects(publishArtifacts(artifacts, { dryRun: false, lookup: async ({ name }) => remote.get(name) ?? null, publish: async (artifact) => { writes.push(artifact.name); remote.set(artifact.name, artifact.integrity); }, confirm: async () => { throw new Error("channel mismatch"); }, report: () => {} }), /channel mismatch/u);
  assert.deepEqual(writes, [artifacts[0].name]);
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

for (const version of ["0.1.0", "0.1.0-next.1"]) test(`signed GitHub ${version} resumes an identical partial draft and rejects conflicting bytes`, async () => {
  const prerelease = publicationChannel(version).prerelease;
  const plan = { project: "pnport", version, revision, tag: `pnport@v${version}` };
  const files = new Map([["pnport-darwin-arm64.tar.gz", Buffer.from("native archive")]]);
  const release = { id: 7, tag_name: plan.tag, target_commitish: revision, draft: true, prerelease, assets: [] };
  const bytes = new Map();
  const uploads = [];
  let interrupt = true;
  const deps = {
    api: async (method, route, body) => {
      if (route.includes("/git/ref/tags/")) return { object: { type: "commit", sha: revision } };
      if (route.includes("/releases?")) return release.assets.length ? [release] : [];
      if (method === "POST") {
        assert.equal(body.prerelease, prerelease);
        if (prerelease) { assert.equal(body.make_latest, "false"); assert.match(body.body, /acceptance is incomplete/u); }
        return release;
      }
      if (method === "PATCH") { if (prerelease) assert.equal(body.make_latest, "false"); release.draft = body.draft; return release; }
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
  release.prerelease = !prerelease;
  await assert.rejects(publishGithub({ plan, files }, deps), /Conflicting release identity/u);
  assert.equal(uploads.length, 2);
});

test("GitHub preview discovery includes later drafts and rejects ambiguous or uncertain state", async () => {
  const tag = "pnport@v0.1.0-next.1";
  const draft = { id: 1, tag_name: tag };
  const unrelated = Array.from({ length: 100 }, (_, index) => ({ id: index + 100, tag_name: `other@v${index}` }));
  assert.deepEqual(await findRelease(async (_method, route) => route.endsWith("page=1") ? unrelated : [draft], tag), draft);
  await assert.rejects(findRelease(async (_method, route) => route.endsWith("page=1") ? [draft, ...unrelated.slice(1)] : [{ ...draft, id: 2 }], tag), /Ambiguous/u);
  for (const response of [null, {}, [null], [{ id: "1", tag_name: tag }]]) await assert.rejects(findRelease(async () => response, tag), /Invalid release discovery/u);
});

for (const version of ["0.0.0", "0.1.0-next.1"]) test(`POSIX installer verifies ${version} before changing the active version`, (t) => {
  const target = selectTarget();
  if (!target || target.os === "win32") return t.skip("POSIX host required");
  const temporary = fixture(t);
  const release = path.join(temporary, "release");
  const install = path.join(temporary, "bin");
  mkdirSync(release);
  const binary = Buffer.from(`#!/bin/sh\nprintf 'pnport ${version}\\n'\n`);
  const archive = nativeArchive(target, binary, Buffer.from("companion"));
  const name = `pnport-${target.suffix}.tar.gz`;
  writeFileSync(path.join(release, name), archive);
  writeFileSync(path.join(release, "SHA256SUMS"), `${sha256(archive)}  ${name}\n`);
  const command = [path.join(root, "scripts/install/pnport.sh"), "--version", version, "--source-dir", release, "--install-dir", install];
  const installed = spawnSync("bash", command, { encoding: "utf8" });
  assert.equal(installed.status, 0, installed.stderr);
  assert.equal(execFileSync(path.join(install, "pnport"), ["--version"], { encoding: "utf8" }).trim(), `pnport ${version}`);
  assert.equal(readFileSync(path.join(install, ".pnport/versions", version, target.os === "darwin" ? "libpnport_preload.dylib" : "libpnport_preload.so"), "utf8"), "companion");
  writeFileSync(path.join(release, "SHA256SUMS"), `${"0".repeat(64)}  ${name}\n`);
  const rejected = spawnSync("bash", command, { encoding: "utf8" });
  assert.notEqual(rejected.status, 0);
  assert.match(rejected.stderr, /checksum mismatch/u);
  assert.equal(execFileSync(path.join(install, "pnport"), ["--version"], { encoding: "utf8" }).trim(), `pnport ${version}`);
});

test("POSIX latest search scans all release pages and selects the highest pnport version", (t) => {
  const target = selectTarget();
  if (!target || target.os === "win32") return t.skip("POSIX host required");
  const temporary = fixture(t);
  const mockBin = path.join(temporary, "mock-bin");
  const pages = path.join(temporary, "pages");
  const requestLog = path.join(temporary, "curl-requests.txt");
  mkdirSync(mockBin);
  mkdirSync(pages);
  writeFileSync(path.join(pages, "1.json"), JSON.stringify(Array.from({ length: 100 }, (_, index) => ({ tag_name: `other@v${index}.0.0` })), null, 2));
  writeFileSync(path.join(pages, "2.json"), JSON.stringify([{ tag_name: "pnport@v0.1.0" }], null, 2));
  writeFileSync(path.join(pages, "3.json"), JSON.stringify([{ tag_name: "pnport@v0.2.0" }, { tag_name: "pnport@v0.1.0-next.9", prerelease: true }], null, 2));
  writeFileSync(path.join(pages, "4.json"), "[]\n");
  const curl = path.join(mockBin, "curl");
  writeFileSync(curl, `#!/bin/sh
set -eu
url=
for value do case "$value" in https://*) url="$value" ;; esac; done
printf '%s\\n' "$url" >> "$PNPORT_CURL_LOG"
case "$url" in
  *page=1) cat "$PNPORT_CURL_PAGES/1.json" ;;
  *page=2) cat "$PNPORT_CURL_PAGES/2.json" ;;
  *page=3) cat "$PNPORT_CURL_PAGES/3.json" ;;
  *page=4) cat "$PNPORT_CURL_PAGES/4.json" ;;
  *) exit 22 ;;
esac
`);
  chmodSync(curl, 0o755);
  const result = spawnSync("bash", [path.join(root, "scripts/install/pnport.sh"), "--install-dir", path.join(temporary, "install")], {
    encoding: "utf8",
    env: { ...process.env, PATH: `${mockBin}${path.delimiter}${process.env.PATH}`, PNPORT_CURL_LOG: requestLog, PNPORT_CURL_PAGES: pages },
  });
  assert.equal(result.status, 22, result.stderr);
  const requests = readFileSync(requestLog, "utf8").trim().split("\n");
  assert.deepEqual(requests.slice(0, 4).map((url) => new URL(url).searchParams.get("page")), ["1", "2", "3", "4"]);
  assert.equal(requests.length, 5);
  assert.match(requests[4], /\/releases\/download\/pnport@v0\.2\.0\/pnport-/u);
});
