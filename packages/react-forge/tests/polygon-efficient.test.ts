import assert from "node:assert/strict";
import { test } from "node:test";
import { inflateSync } from "node:zlib";
import {
  curvedSheet,
  paintedQuad,
  paintedTexture,
} from "../examples/polygon-efficient-assets.js";

function pixel(bytes: Buffer, x: number, y: number): number[] {
  assert.deepEqual(
    [...bytes.subarray(0, 8)],
    [137, 80, 78, 71, 13, 10, 26, 10],
  );
  assert.equal(bytes.readUInt32BE(16), 16);
  assert.equal(bytes.readUInt32BE(20), 16);
  const idatLength = bytes.readUInt32BE(33);
  assert.equal(bytes.toString("ascii", 37, 41), "IDAT");
  const rows = inflateSync(bytes.subarray(41, 41 + idatLength));
  const offset = y * 65 + 1 + x * 4;
  assert.equal(rows[y * 65], 0);
  return [...rows.subarray(offset, offset + 4)];
}

test("curved sheets keep the selected vertex and triangle budgets", () => {
  for (const [u, v, vertices, triangles, closedShell] of [
    [6, 16, 119, 192, 472],
    [20, 60, 1281, 2400, 5120],
  ]) {
    const geometry = curvedSheet(u, v);
    assert.equal(geometry.positions.length / 3, vertices);
    assert.equal(geometry.indices.length / 3, triangles);
    assert.equal(4 * u * v + 4 * (u + v), closedShell);
    for (let i = 0; i < vertices; i++) {
      const n = geometry.normals.subarray(i * 3, i * 3 + 3);
      const t = geometry.tangents!.subarray(i * 4, i * 4 + 3);
      assert.ok(Math.abs(Math.hypot(...n) - 1) < 1e-6);
      assert.ok(Math.abs(Math.hypot(...t) - 1) < 1e-6);
      assert.ok(Math.abs(n[0] * t[0] + n[1] * t[1] + n[2] * t[2]) < 1e-6);
    }
    for (let i = 0; i < geometry.indices.length; i += 3) {
      const [a, b, c] = geometry.indices.subarray(i, i + 3);
      const x1 = geometry.positions[b * 3] - geometry.positions[a * 3];
      const y1 = geometry.positions[b * 3 + 1] - geometry.positions[a * 3 + 1];
      const x2 = geometry.positions[c * 3] - geometry.positions[a * 3];
      const y2 = geometry.positions[c * 3 + 1] - geometry.positions[a * 3 + 1];
      assert.ok(x1 * y2 - y1 * x2 > 0);
    }
  }
});

test("opaque and alpha artwork reuse one four-vertex quad", () => {
  const quad = paintedQuad();
  assert.deepEqual([...quad.positions], [0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]);
  assert.deepEqual([...quad.uv!], [0, 1, 1, 1, 1, 0, 0, 0]);
  assert.deepEqual([...quad.indices], [0, 1, 2, 0, 2, 3]);
  const opaque = paintedTexture(false);
  const alpha = paintedTexture(true);
  assert.deepEqual(opaque, paintedTexture(false));
  assert.deepEqual(alpha, paintedTexture(true));
  assert.deepEqual(pixel(opaque, 0, 0), [128, 24, 32, 255]);
  assert.deepEqual(pixel(opaque, 15, 15), [24, 32, 255, 255]);
  assert.deepEqual(pixel(opaque, 7, 7), [16, 220, 40, 255]);
  for (let y = 0; y < 16; y++) {
    for (let x = 0; x < 16; x++) {
      assert.equal(pixel(opaque, x, y)[3], 255);
      assert.deepEqual(pixel(alpha, x, y), [
        255,
        255,
        255,
        Math.round((255 * x) / 15),
      ]);
    }
  }
});
