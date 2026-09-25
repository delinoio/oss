import assert from "node:assert/strict";
import test from "node:test";
import { manifest, nativeName, normalizedTextBytes, platforms } from "./package.mjs";
import { publishArtifacts } from "./publish.mjs";

test("public manifests select exact-version native packages without install hooks", () => {
  const revision = "a".repeat(40);
  const main = manifest(null, revision);
  assert.equal(main.private, undefined);
  assert.equal(main.license, "Apache-2.0");
  assert.equal(main.publishConfig.access, "public");
  assert.equal(main.gitHead, revision);
  assert.deepEqual(Object.keys(main.optionalDependencies), platforms.map(nativeName));
  assert.ok(Object.values(main.optionalDependencies).every((version) => version === main.version));
  for (const host of platforms) {
    const native = manifest(host, revision);
    assert.equal(native.name, nativeName(host));
    assert.deepEqual(native.os, [host.platform]);
    assert.deepEqual(native.cpu, [host.architecture]);
    assert.deepEqual(native.libc, host.platform === "linux" ? ["glibc"] : undefined);
    assert.equal(native.scripts, undefined);
  }
});

test("license, notice and README bytes are stable across Windows checkouts", () => {
  assert.equal(normalizedTextBytes(Buffer.from("first\r\nsecond\n")).toString("utf8"), "first\nsecond\n");
});

const artifacts = (version) => [...platforms.map((host) => ({ name: nativeName(host), version, integrity: `sha512-${host.id}` })), { name: "@delino/react-forge", version, integrity: "sha512-main" }];

test("first release checks conflicts before writes and publishes native packages first", async () => {
  const set = artifacts("0.1.0");
  const published = new Map([[set[0].name, set[0].integrity]]);
  const writes = [];
  const lookup = async (...args) => {
    assert.equal(args.length, 1);
    return published.get(args[0].name) ?? null;
  };
  const publish = async (artifact) => { writes.push(artifact.name); published.set(artifact.name, artifact.integrity); };
  await publishArtifacts(set, { lookup, publish, report: () => {} });
  assert.deepEqual(writes, set.slice(1).map((item) => item.name));
  published.set(set[4].name, "sha512-conflict");
  await assert.rejects(publishArtifacts(set, { lookup, publish, report: () => {} }), /Conflicting published integrity/u);
  assert.equal(writes.length, 6);
});
