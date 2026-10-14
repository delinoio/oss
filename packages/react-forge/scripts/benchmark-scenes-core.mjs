import { createHash } from "node:crypto";

export const SCENE_CASES = Object.freeze(
  ["glb", "fbx"].flatMap((format) =>
    ["small", "dense", "shared", "overlay"].map((workload) => ({
      format,
      workload,
    })),
  ),
);

export function workloadShape(workload) {
  switch (workload) {
    case "small":
      return { cells: 8, geometries: 1, meshes: 1 };
    case "dense":
      return { cells: 256, geometries: 1, meshes: 1 };
    case "shared":
      return { cells: 32, geometries: 1, meshes: 128 };
    case "overlay":
      return { cells: 32, geometries: 2, meshes: 2 };
    default:
      throw new Error("Invalid scene benchmark workload.");
  }
}

export function expectedTopology(workload) {
  const shape = workloadShape(workload);
  const vertices = (shape.cells + 1) ** 2;
  const triangles = 2 * shape.cells ** 2;
  const fixtureBytes = shape.geometries * (16 + 32 * vertices + 12 * triangles);
  if (fixtureBytes >= 64 * 1024 * 1024)
    throw new Error("Scene benchmark geometry exceeds the asset limit.");
  return {
    ...shape,
    uniqueVertexCount: shape.geometries * vertices,
    uniqueTriangleCount: shape.geometries * triangles,
    instanceVertexCount: shape.meshes * vertices,
    instanceTriangleCount: shape.meshes * triangles,
    registeredGeometryBytes: fixtureBytes,
  };
}

export function grid(cells, z = 0) {
  if (![8, 32, 256].includes(cells) || ![0, 0.001].includes(z))
    throw new Error("Invalid scene benchmark grid.");
  const vertices = (cells + 1) ** 2;
  const positions = new Float32Array(vertices * 3);
  const normals = new Float32Array(vertices * 3);
  const uv = new Float32Array(vertices * 2);
  const indices = new Uint32Array(cells * cells * 6);
  for (let j = 0; j <= cells; j++) {
    for (let i = 0; i <= cells; i++) {
      const v = j * (cells + 1) + i;
      positions.set([i / cells, j / cells, z], 3 * v);
      normals.set([0, 0, 1], 3 * v);
      uv.set([i / cells, 1 - j / cells], 2 * v);
    }
  }
  for (let j = 0; j < cells; j++) {
    for (let i = 0; i < cells; i++) {
      const a = j * (cells + 1) + i;
      const b = a + 1;
      const c = a + cells + 1;
      const d = c + 1;
      indices.set([a, b, d, a, d, c], 6 * (j * cells + i));
    }
  }
  return { positions, normals, uv, indices };
}

export function fixtureHash(geometries) {
  const hash = createHash("sha256");
  for (const geometry of geometries) {
    for (const stream of ["positions", "normals", "uv", "indices"])
      hash.update(
        Buffer.from(
          geometry[stream].buffer,
          geometry[stream].byteOffset,
          geometry[stream].byteLength,
        ),
      );
  }
  return hash.digest("hex");
}

export function inspectGlb(bytes) {
  if (
    bytes.length < 20 ||
    bytes.toString("utf8", 0, 4) !== "glTF" ||
    bytes.readUInt32LE(8) !== bytes.length ||
    bytes.toString("utf8", 16, 20) !== "JSON"
  )
    throw new Error("Invalid GLB container.");
  const jsonLength = bytes.readUInt32LE(12);
  if (20 + jsonLength > bytes.length)
    throw new Error("Truncated GLB JSON chunk.");
  const glb = JSON.parse(bytes.toString("utf8", 20, 20 + jsonLength));
  const unique = new Map();
  let meshes = 0;
  let instanceVertices = 0;
  let instanceTriangles = 0;
  for (const node of glb.nodes ?? []) {
    if (node.mesh === undefined) continue;
    const primitives = glb.meshes?.[node.mesh]?.primitives;
    if (!Array.isArray(primitives) || primitives.length !== 1)
      throw new Error("Unexpected GLB mesh primitives.");
    const primitive = primitives[0];
    if (
      (primitive.mode !== undefined && primitive.mode !== 4) ||
      primitive.attributes?.NORMAL === undefined ||
      primitive.attributes?.TEXCOORD_0 === undefined
    )
      throw new Error("Unexpected GLB benchmark attributes.");
    const vertices = glb.accessors?.[primitive.attributes.POSITION]?.count;
    const indices = glb.accessors?.[primitive.indices]?.count;
    if (
      !Number.isSafeInteger(vertices) ||
      !Number.isSafeInteger(indices) ||
      indices % 3
    )
      throw new Error("Invalid GLB accessor topology.");
    const key = `${primitive.attributes.POSITION}:${primitive.indices}`;
    unique.set(key, { vertices, triangles: indices / 3 });
    meshes++;
    instanceVertices += vertices;
    instanceTriangles += indices / 3;
  }
  return {
    geometries: unique.size,
    meshes,
    uniqueVertexCount: [...unique.values()].reduce((n, x) => n + x.vertices, 0),
    uniqueTriangleCount: [...unique.values()].reduce(
      (n, x) => n + x.triangles,
      0,
    ),
    instanceVertexCount: instanceVertices,
    instanceTriangleCount: instanceTriangles,
  };
}

export function assertTopology(actual, expected) {
  for (const key of [
    "geometries",
    "meshes",
    "uniqueVertexCount",
    "uniqueTriangleCount",
    "instanceVertexCount",
    "instanceTriangleCount",
  ]) {
    if (actual[key] !== expected[key])
      throw new Error(
        `Scene topology mismatch for ${key}: ${actual[key]} != ${expected[key]}.`,
      );
  }
}
