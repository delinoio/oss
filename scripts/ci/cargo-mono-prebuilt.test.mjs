import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { existsSync, mkdtempSync, readdirSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { packArchive } from "../archive.mjs";
import { cargoMonoRelease, prepareCargoMono } from "./cargo-mono-prebuilt.mjs";

const digest = bytes => createHash("sha256").update(bytes).digest("hex");
function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "cargo-mono-download-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const binary = Buffer.from("fixture executable\n");
  const bytes = packArchive([{ name: "cargo-mono", data: binary, mode: 0o755 }]);
  const release = { ...cargoMonoRelease, size: bytes.length, sha256: digest(bytes), binarySize: binary.length, binarySha256: digest(binary) };
  const config = { platform: "linux", arch: "x64", directory, release, fetchAsset: async () => new Response(bytes), run: (file, args) => { assert.equal(readFileSync(file).toString(), binary.toString()); assert.deepEqual(args, ["--version"]); return release.verificationOutput; } };
  return { directory, bytes, config };
}
test("first-party release identity and both pinned digests are explicit", () => {
  assert.equal(cargoMonoRelease.version, "0.6.9");
  assert.equal(cargoMonoRelease.verificationOutput, "cargo mono 0.6.9");
  assert.match(cargoMonoRelease.url, /delinoio\/oss\/releases\/download\/cargo-mono%40v0\.6\.9\/cargo-mono-linux-amd64\.tar\.gz$/u);
  for (const key of ["sha256", "binarySha256"]) assert.match(cargoMonoRelease[key], /^[0-9a-f]{64}$/u);
});
test("download verifies archive, executable and version without building source", async t => {
  const f = fixture(t);
  let calls = 0;
  const result = await prepareCargoMono({ ...f.config, fetchAsset: async (url, options) => { calls++; assert.equal(url, cargoMonoRelease.url); assert.ok(options.signal); return new Response(f.bytes, { headers: { "content-length": String(f.bytes.length) } }); } });
  assert.equal(calls, 1); assert.ok(existsSync(result.executable));
});
for (const [name, override] of [
  ["HTTP error", f => ({ fetchAsset: async () => new Response("", { status: 503 }) })],
  ["network error", f => ({ fetchAsset: async () => { throw new Error("offline"); } })],
  ["archive hash", f => ({ release: { ...f.config.release, sha256: "0".repeat(64) } })],
  ["executable hash", f => ({ release: { ...f.config.release, binarySha256: "0".repeat(64) } })],
  ["declared size", f => ({ fetchAsset: async () => new Response(f.bytes, { headers: { "content-length": "1" } }) })],
  ["oversized body", f => ({ fetchAsset: async () => new Response(Buffer.concat([f.bytes, Buffer.from("extra")])) })],
  ["version mismatch", f => ({ run: () => "cargo mono 0.0.0" })],
  ["execution failure", f => ({ run: () => { throw new Error("cannot execute"); } })],
]) test(`${name} fails and removes private staging`, async t => {
  const f = fixture(t);
  await assert.rejects(prepareCargoMono({ ...f.config, ...override(f) }));
  assert.deepEqual(readdirSync(f.directory), []);
});
test("unsupported hosts fail before download", async () => {
  await assert.rejects(prepareCargoMono({ platform: "win32", arch: "x64", fetchAsset: () => { throw new Error("must not download"); } }), /Unsupported/u);
});
