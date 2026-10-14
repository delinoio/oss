import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import validator from "gltf-validator";
import task from "../examples/polygon-efficient.tsx";
import { SurfaceVariant } from "../examples/polygon-efficient-assets.ts";

const expected = new Map([
  [SurfaceVariant.Low, { vertices: 119, triangles: 192, textures: 0 }],
  [SurfaceVariant.Dense, { vertices: 1281, triangles: 2400, textures: 0 }],
  [SurfaceVariant.Badge, { vertices: 4, triangles: 2, textures: 1 }],
  [SurfaceVariant.Alpha, { vertices: 4, triangles: 2, textures: 1 }],
]);

function accessorValues(bytes, document, index) {
  const accessor = document.accessors[index];
  const view = document.bufferViews[accessor.bufferView];
  const width = { SCALAR: 1, VEC2: 2, VEC3: 3, VEC4: 4 }[accessor.type];
  const componentSize = { 5121: 1, 5123: 2, 5125: 4, 5126: 4 }[
    accessor.componentType
  ];
  assert.ok(width && componentSize);
  const start =
    28 +
    bytes.readUInt32LE(12) +
    (view.byteOffset ?? 0) +
    (accessor.byteOffset ?? 0);
  const stride = view.byteStride ?? width * componentSize;
  const values = [];
  for (let i = 0; i < accessor.count; i++) {
    for (let component = 0; component < width; component++) {
      const offset = start + i * stride + component * componentSize;
      values.push(
        accessor.componentType === 5121
          ? bytes.readUInt8(offset)
          : accessor.componentType === 5123
            ? bytes.readUInt16LE(offset)
            : accessor.componentType === 5125
              ? bytes.readUInt32LE(offset)
              : bytes.readFloatLE(offset),
      );
    }
  }
  return values;
}
const directory = await mkdtemp(join(tmpdir(), "react-forge-polygon-"));
const report = [];
try {
  const fbxFiles = [];
  for (const [variant, counts] of expected) {
    for (const format of ["glb", "fbx"]) {
      const session = await task({ data: { format, variant } });
      try {
        const snapshot = await session.snapshot();
        assert.equal(
          snapshot.targets.filter((target) => target.kind === "mesh").length,
          1,
        );
        const bytes = await session.exportBuffer();
        if (format === "glb") {
          const validation = await validator.validateBytes(bytes);
          assert.equal(
            validation.issues.numErrors,
            0,
            JSON.stringify(validation.issues),
          );
          const document = JSON.parse(
            bytes.subarray(20, 20 + bytes.readUInt32LE(12)).toString(),
          );
          assert.equal(document.meshes.length, 1);
          assert.equal(document.materials.length, 1);
          assert.equal(document.textures?.length ?? 0, counts.textures);
          const primitive = document.meshes[0].primitives[0];
          assert.equal(
            document.accessors[primitive.attributes.POSITION].count,
            counts.vertices,
          );
          assert.equal(
            document.accessors[primitive.indices].count / 3,
            counts.triangles,
          );
          assert.ok(primitive.attributes.NORMAL !== undefined);
          assert.ok(primitive.attributes.TEXCOORD_0 !== undefined);
          const positions = accessorValues(
            bytes,
            document,
            primitive.attributes.POSITION,
          );
          const normals = accessorValues(
            bytes,
            document,
            primitive.attributes.NORMAL,
          );
          const uv = accessorValues(
            bytes,
            document,
            primitive.attributes.TEXCOORD_0,
          );
          const indices = accessorValues(bytes, document, primitive.indices);
          assert.ok(normals.every(Number.isFinite));
          assert.ok(uv.every(Number.isFinite));
          const [a, b, c] = indices;
          const crossZ =
            (positions[b * 3] - positions[a * 3]) *
              (positions[c * 3 + 1] - positions[a * 3 + 1]) -
            (positions[b * 3 + 1] - positions[a * 3 + 1]) *
              (positions[c * 3] - positions[a * 3]);
          assert.ok(crossZ > 0);
          if (
            variant === SurfaceVariant.Badge ||
            variant === SurfaceVariant.Alpha
          ) {
            assert.deepEqual(positions, [0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]);
            assert.deepEqual(uv, [
              1 / 32,
              31 / 32,
              31 / 32,
              31 / 32,
              31 / 32,
              1 / 32,
              1 / 32,
              1 / 32,
            ]);
            assert.deepEqual(normals, [0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]);
          }
          assert.equal(
            document.materials[0].alphaMode ?? "OPAQUE",
            variant === SurfaceVariant.Alpha ? "BLEND" : "OPAQUE",
          );
          report.push({ variant, format, ...counts, validatorErrors: 0 });
        } else {
          const path = join(directory, `${variant}.fbx`);
          await writeFile(path, bytes);
          fbxFiles.push(path);
        }
      } finally {
        await session.dispose();
      }
    }
  }
  const inspection = spawnSync(
    "cargo",
    [
      "run",
      "--quiet",
      "--locked",
      "-p",
      "forge-fbx",
      "--example",
      "inspect",
      "--",
      ...fbxFiles,
    ],
    {
      cwd: new URL("../../..", import.meta.url),
      encoding: "utf8",
    },
  );
  assert.equal(inspection.status, 0, inspection.stderr);
  const imported = JSON.parse(inspection.stdout);
  for (const result of imported) {
    const variant = result.file.replace(/\.fbx$/, "");
    const counts = expected.get(variant);
    assert.ok(counts, variant);
    assert.equal(result.meshes, 1);
    assert.equal(result.materials, 1);
    assert.equal(result.vertices, counts.vertices);
    assert.equal(result.triangles, counts.triangles);
    assert.equal(result.textures, counts.textures);
    assert.equal(result.hasUvs, true);
    assert.equal(result.opacityTexture, variant === SurfaceVariant.Alpha);
    report.push({
      variant,
      format: "fbx",
      ...counts,
      ufbxParsed: true,
      opacityTexture: result.opacityTexture,
    });
  }
  console.log(JSON.stringify(report, null, 2));
} finally {
  await rm(directory, { recursive: true, force: true });
}
