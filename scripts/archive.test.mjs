import assert from "node:assert/strict";
import { gzipSync, gunzipSync } from "node:zlib";
import { test } from "node:test";
import { packArchive, unpackArchive } from "./archive.mjs";

test("archives preserve bytes and executable permissions deterministically", () => {
  const entries = [{ name: "bin/tool", data: Buffer.from([0, 255, 7]), mode: 0o755 }, { name: "LICENSE", data: "license" }];
  const archive = packArchive(entries);
  assert.deepEqual(archive, packArchive(entries));
  const extracted = unpackArchive(archive);
  assert.deepEqual(extracted[0].data, entries[0].data);
  assert.equal(extracted[0].mode, 0o755);
  assert.equal(extracted[1].data.toString(), "license");
});

test("archive creation rejects unsafe paths and duplicate entries", () => {
  for (const name of ["../tool", "/tool", "a/../tool", "a\\tool", "a//tool", "./tool"]) assert.throws(() => packArchive([{ name, data: "x" }]));
  assert.throws(() => packArchive([{ name: "tool", data: "x" }, { name: "tool", data: "y" }]));
});

test("archive extraction rejects corrupt headers, links and truncation", () => {
  const original = gunzipSync(packArchive([{ name: "tool", data: "x" }]));
  const corrupt = Buffer.from(original);
  corrupt[0] ^= 1;
  assert.throws(() => unpackArchive(gzipSync(corrupt)), /checksum/);
  const link = Buffer.from(original);
  link[156] = 50;
  assert.throws(() => unpackArchive(gzipSync(link)), /unsupported/);
  assert.throws(() => unpackArchive(gzipSync(original.subarray(0, 600))), /truncated/);
  assert.throws(() => unpackArchive(gzipSync(original.subarray(0, 1024))), /end marker/);
});
