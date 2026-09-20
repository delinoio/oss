import { stage } from '../scripts/github-release.mjs';
import { extractExecutable } from '../../../scripts/release/linux-packages/model.mjs';
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs, { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
import platforms from "../src/platforms.cjs";
import { metadata, npm, sourceText } from "../scripts/common.mjs";
import { buildPackage, executableTarball, inspectTarball, integrity, packageManifest, tarballName, tarEntries, verifySet } from "../scripts/package.mjs";
import { publishArtifacts, registryIntegrity } from "../scripts/publish.mjs";

const sourceRevision = "1".repeat(40);
const { version } = metadata();
const names = [...platforms.targets.map(({ name }) => name), "@delino/clibox"];
const artifacts = names.map((name) => ({ name, version, revision: sourceRevision, filename: tarballName(name, version), integrity: integrity(Buffer.from(name)) }));
const quiet = () => {};

test("source version checks reject drift before packaging", () => {
  for (const eol of ["\n", "\r\n"]) {
    const read = (file) => sourceText(file).replaceAll("\n", eol);
    assert.deepEqual(metadata(read), metadata());
    assert.throws(() => metadata((file) => file.endsWith("package.json") ? read(file).replace(`"version": "${version}"`, '"version": "99.0.0"') : read(file)), /version mismatch/u);
    assert.throws(() => metadata((file) => file.endsWith("Cargo.toml") ? read(file).replace('name = "clibox"', 'name = "unexpected"') : read(file)), /identity mismatch/u);
  }
  assert.throws(() => packageManifest(undefined, version, "main"), /exact source commit/u);
});

test("CRLF checkouts produce canonical LF tarballs verifiable from another checkout", (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "clibox CRLF source "));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const files = [
    "crates/clibox/Cargo.toml", "crates/clibox/LICENSE", "packages/clibox/package.json",
    "packages/clibox/README.md", "packages/clibox/bin/clibox.cjs", "packages/clibox/src/launcher.cjs",
    "packages/clibox/src/platforms.cjs", "packages/clibox/scripts/common.mjs", "packages/clibox/scripts/package.mjs",
  ];
  const packed = [];
  for (const eol of ["\n", "\r\n"]) {
    const checkout = path.join(directory, eol === "\n" ? "lf" : "crlf");
    for (const file of files) {
      mkdirSync(path.dirname(path.join(checkout, file)), { recursive: true });
      writeFileSync(path.join(checkout, file), sourceText(file).replaceAll("\n", eol));
    }
    const output = path.join(checkout, "output");
    const module = pathToFileURL(path.join(checkout, "packages/clibox/scripts/package.mjs")).href;
    execFileSync(process.execPath, ["--input-type=module", "-e", `import { buildPackage } from ${JSON.stringify(module)}; buildPackage(${JSON.stringify({ output, sourceRevision })});`], { encoding: "utf8" });
    const file = path.join(output, "tarballs", tarballName("@delino/clibox", version));
    packed.push(inspectTarball(file, { version, sourceRevision }).integrity);
    for (const { bytes } of tarEntries(readFileSync(file)).values()) assert.equal(bytes.includes(Buffer.from("\r\n")), false);
  }
  assert.equal(packed[0], packed[1]);
});

test("generated metadata pins exact versions, platforms, public access and no lifecycle scripts", () => {
  const main = packageManifest(undefined, version, sourceRevision);
  assert.equal(main.private, undefined);
  assert.equal(main.scripts, undefined);
  assert.deepEqual(main.optionalDependencies, Object.fromEntries(platforms.targets.map(({ name }) => [name, version])));
  assert.deepEqual(main.bin, { clibox: "bin/clibox.cjs" });
  for (const target of platforms.targets) {
    const manifest = packageManifest(target, version, sourceRevision);
    assert.deepEqual(manifest.os, [target.os]);
    assert.deepEqual(manifest.cpu, [target.cpu]);
    assert.deepEqual(manifest.libc, target.libc ? [target.libc] : undefined);
    assert.equal(manifest.publishConfig.access, "public");
    assert.equal(manifest.gitHead, sourceRevision);
    assert.equal(manifest.scripts, undefined);
  }
});

test("pack preserves executable shim mode when host chmod cannot set execute bits", (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "clibox-no-execute-bits-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  // Model the NTFS chmod limitation on every CI host, including Unix machines.
  t.mock.method(fs, "chmodSync", () => {});
  syncBuiltinESMExports();
  try {
    const artifact = buildPackage({ output: directory, sourceRevision });
    const file = path.join(directory, "tarballs", artifact.filename);
    assert.equal(tarEntries(readFileSync(file)).get("bin/clibox.cjs").mode & 0o777, 0o755);
    assert.equal(artifact.integrity, integrity(readFileSync(file)));
  } finally {
    t.mock.restoreAll();
    syncBuiltinESMExports();
  }
});

test("pack validates native executable versions and the complete nine-tarball boundary", (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), "clibox-artifacts-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  assert.throws(() => buildPackage({ target: platforms.selectTarget(), binary: process.execPath, output: directory, sourceRevision }), /Executable\/source version mismatch/u);
  const main = buildPackage({ output: directory, sourceRevision });
  const tarballs = path.join(directory, "tarballs");
  const mainFile = path.join(tarballs, main.filename);
  const entries = tarEntries(readFileSync(mainFile));
  assert.equal(entries.size, 6);
  assert.equal(entries.get("bin/clibox.cjs").mode & 0o111, 0o111);
  assert.throws(() => verifySet(tarballs, sourceRevision), /exactly nine/u);
  assert.throws(() => inspectTarball(mainFile, { version: "99.0.0", sourceRevision }), /metadata mismatch/u);
  assert.throws(() => inspectTarball(mainFile, { version, sourceRevision: "2".repeat(40) }), /metadata mismatch/u);
  // Non-host binaries are inert fixture payloads. They test package metadata and
  // archive validation only; release jobs execute real native binaries.
  for (const target of platforms.targets) {
    const fixture = path.join(directory, target.suffix);
    mkdirSync(path.join(fixture, "bin"), { recursive: true });
    writeFileSync(path.join(fixture, "package.json"), JSON.stringify(packageManifest(target, version, sourceRevision)));
    const payload = Buffer.from("inert\r\nfixture\0binary");
    writeFileSync(path.join(fixture, "bin", target.binary), payload);
    writeFileSync(path.join(fixture, "LICENSE"), sourceText("crates/clibox/LICENSE"));
    writeFileSync(path.join(fixture, "README.md"), sourceText("packages/clibox/README.md"));
    npm(["pack", "--ignore-scripts", "--pack-destination", tarballs], { cwd: fixture });
    const file = path.join(tarballs, tarballName(target.name, version));
    // Start with non-executable files on every host, including Windows where
    // chmod cannot set POSIX execute bits. Windows PE packages accept that mode;
    // Unix packages must reject it until creation finalizes the archive header.
    assert.equal(tarEntries(readFileSync(file)).get(`bin/${target.binary}`).mode & 0o111, 0);
    if (target.os !== "win32") {
      const original = readFileSync(file);
      assert.throws(() => inspectTarball(file, { version, sourceRevision }), /Missing executable mode/u);
      assert.deepEqual(readFileSync(file), original);
      writeFileSync(file, executableTarball(original, `bin/${target.binary}`));
    }
    inspectTarball(file, { version, sourceRevision });
    assert.deepEqual(tarEntries(readFileSync(file)).get(`bin/${target.binary}`).bytes, payload);
  }
  assert.deepEqual(verifySet(tarballs, sourceRevision).map(({ name }) => name), names);
  const staged = stage(tarballs, path.join(directory, "github"), sourceRevision, () => []);
  for (const [cpu, arch] of [["x64", "amd64"], ["arm64", "arm64"]]) {
    const artifact = verifySet(tarballs, sourceRevision).find(({ name }) => name === `@delino/clibox-linux-${cpu}-gnu`);
    const binary = tarEntries(readFileSync(path.join(tarballs, artifact.filename))).get("bin/clibox").bytes;
    assert.deepEqual(extractExecutable(staged.files.get(`clibox-linux-${arch}.tar.gz`), "clibox"), binary);
  }
  writeFileSync(path.join(tarballs, "unexpected.txt"), "unexpected");
  assert.throws(() => verifySet(tarballs, sourceRevision), /exactly nine/u);
});

test("dry-run never contacts npm or invokes publication", async () => {
  const forbidden = () => { throw new Error("unexpected external call"); };
  await publishArtifacts(artifacts, { lookup: forbidden, publish: forbidden, report: quiet });
});

test("publication confirms every platform before the main package and recovers partial writes", async () => {
  const registry = new Map([[artifacts[0].name, artifacts[0].integrity]]);
  const written = [];
  const lookup = async ({ name }) => registry.get(name) ?? null;
  const publish = async (artifact) => {
    if (artifact.name === "@delino/clibox") assert.equal(registry.size, 8);
    registry.set(artifact.name, artifact.integrity);
    written.push(artifact.name);
    if (written.length === 3) throw new Error("interrupted after upload");
  };
  await assert.rejects(publishArtifacts(artifacts, { dryRun: false, lookup, publish, report: quiet }), /interrupted/u);
  await publishArtifacts(artifacts, { dryRun: false, lookup, publish, report: quiet });
  assert.deepEqual(written, names.slice(1));
  assert.equal(registry.size, 9);
});

test("publication waits for delayed registry metadata without repeating uploads", async () => {
  const uploaded = new Set();
  const reads = new Map();
  const waits = [];
  const reports = [];
  await publishArtifacts(artifacts, {
    dryRun: false,
    lookup: async ({ name, integrity }) => {
      if (!uploaded.has(name)) return null;
      const count = (reads.get(name) ?? 0) + 1;
      reads.set(name, count);
      return count <= 100 ? null : integrity;
    },
    publish: async ({ name }) => {
      assert.ok(!uploaded.has(name), "a successful upload must not be repeated");
      if (name === "@delino/clibox") {
        assert.equal(uploaded.size, 8);
        assert.ok([...reads.values()].every((count) => count === 101));
      }
      uploaded.add(name);
    },
    delay: async (milliseconds) => waits.push(milliseconds),
    report: (action, details) => reports.push({ action, ...details }),
  });
  assert.equal(uploaded.size, 9);
  assert.equal(waits.length, 9 * 100);
  assert.ok(waits.every((milliseconds) => milliseconds === 10000));
  assert.equal(reports.filter(({ action }) => action === "publish_pending").length, waits.length);
});

test("conflicting remote bytes and unconfirmed uploads stop before main publication", async () => {
  await assert.rejects(publishArtifacts(artifacts, { dryRun: false, lookup: async ({ name }) => name === "@delino/clibox" ? "different" : null, publish: () => assert.fail("must preflight all packages"), report: quiet }), /Conflicting/u);
  const written = [];
  const waits = [];
  let reads = 0;
  await assert.rejects(publishArtifacts(artifacts, { dryRun: false, lookup: async () => { reads++; return null; }, publish: async ({ name }) => written.push(name), delay: async (milliseconds) => waits.push(milliseconds), report: quiet }), /not confirmed/u);
  assert.deepEqual(written, [names[0]]);
  assert.equal(reads, artifacts.length + 1 + 121);
  assert.equal(waits.length, 120);
  assert.equal(waits.reduce((sum, milliseconds) => sum + milliseconds, 0), 1200000);
});

test("registry inspection distinguishes absent, unauthorized and malformed responses", async () => {
  const artifact = artifacts[0];
  assert.equal(await registryIntegrity(artifact, async () => ({ status: 404 })), null);
  await assert.rejects(registryIntegrity(artifact, async () => ({ status: 403 })), /HTTP 403/u);
  await assert.rejects(registryIntegrity(artifact, async () => ({ ok: true, json: async () => ({}) })), /Invalid registry metadata/u);
  assert.equal(await registryIntegrity(artifact, async (url, options) => {
    assert.ok(url.startsWith("https://registry.npmjs.org/%40delino%2Fclibox-"));
    assert.equal(options.redirect, "error");
    return { ok: true, json: async () => ({ name: artifact.name, version, dist: { integrity: artifact.integrity } }) };
  }), artifact.integrity);
});
