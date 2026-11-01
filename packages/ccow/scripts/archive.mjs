import { gzipSync, gunzipSync } from "node:zlib";
import assert from "node:assert/strict";

// A closed regular-file ustar format avoids invoking an extractor on input paths.
export function archive(files) {
  const blocks = [];
  for (const [name, { bytes, mode = 0o644 }] of Object.entries(files)) {
    assert(
      /^[a-zA-Z0-9._/-]+$/.test(name) &&
        !name.split("/").includes("..") &&
        !name.startsWith("/") &&
        name.length < 100,
    );
    const header = Buffer.alloc(512);
    header.write(name);
    for (const [offset, length, value] of [
      [100, 8, mode],
      [108, 8, 0],
      [116, 8, 0],
      [124, 12, bytes.length],
      [136, 12, 0],
    ])
      header.write(
        value.toString(8).padStart(length - 1, "0") + "\0",
        offset,
        length,
        "ascii",
      );
    header.fill(32, 148, 156);
    header[156] = 48;
    header.write("ustar\0", 257);
    header.write("00", 263);
    header.write(
      header
        .reduce((a, b) => a + b, 0)
        .toString(8)
        .padStart(6, "0") + "\0 ",
      148,
    );
    blocks.push(
      header,
      bytes,
      Buffer.alloc((512 - (bytes.length % 512)) % 512),
    );
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}

export function entries(bytes) {
  const tar = gunzipSync(bytes, { maxOutputLength: 128 * 1024 * 1024 });
  const result = new Map();
  let offset = 0;
  const text = (bytes) => bytes.toString("utf8").replace(/\0.*$/s, "");
  for (; offset + 512 <= tar.length;) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    assert.equal(
      header.reduce(
        (sum, byte, i) => sum + (i >= 148 && i < 156 ? 32 : byte),
        0,
      ),
      parseInt(text(header.subarray(148, 156)), 8),
      "Invalid tar checksum",
    );
    const name = text(header.subarray(0, 100));
    assert(
      /^[a-zA-Z0-9._/-]+$/.test(name) &&
        !name.startsWith("/") &&
        !name.split("/").some((part) => ["", ".", ".."].includes(part)),
      "Unsafe archive path",
    );
    assert(
      !result.has(name) &&
        header[156] === 48 &&
        header.subarray(345, 500).every((byte) => byte === 0),
      "Unexpected archive entry",
    );
    const size = parseInt(text(header.subarray(124, 136)), 8);
    assert(
      Number.isSafeInteger(size) &&
        size >= 0 &&
        offset + 512 + size <= tar.length,
      "Invalid archive size",
    );
    result.set(name, {
      bytes: tar.subarray(offset + 512, offset + 512 + size),
      mode: parseInt(text(header.subarray(100, 108)), 8),
    });
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  assert(
    offset + 1024 <= tar.length &&
      tar.subarray(offset).every((byte) => byte === 0),
    "Invalid archive ending",
  );
  return result;
}
