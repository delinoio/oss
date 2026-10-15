import { deflateSync } from "node:zlib";
import type { GeometryInput } from "@delino/react-forge/glb";

export enum SurfaceVariant {
  Low = "low",
  Dense = "dense",
  Badge = "badge",
  Alpha = "alpha",
}

export const sheetSegments = {
  [SurfaceVariant.Low]: [6, 16],
  [SurfaceVariant.Dense]: [20, 60],
} as const;

export function curvedSheet(
  uSegments: number,
  vSegments: number,
): GeometryInput {
  if (
    !Number.isInteger(uSegments) ||
    !Number.isInteger(vSegments) ||
    uSegments < 1 ||
    vSegments < 1
  ) {
    throw new RangeError("Sheet segment counts must be positive integers.");
  }
  const positions: number[] = [];
  const normals: number[] = [];
  const tangents: number[] = [];
  const uv: number[] = [];
  const indices: number[] = [];
  const stride = uSegments + 1;
  for (let v = 0; v <= vSegments; v++) {
    const y = v / vSegments;
    for (let u = 0; u <= uSegments; u++) {
      const x = u / uSegments;
      const z = 0.1 * Math.sin(Math.PI * x) * Math.sin(Math.PI * y);
      const dzdx =
        0.1 * Math.PI * Math.cos(Math.PI * x) * Math.sin(Math.PI * y);
      const dzdy =
        0.1 * Math.PI * Math.sin(Math.PI * x) * Math.cos(Math.PI * y);
      const normalLength = Math.hypot(dzdx, dzdy, 1);
      const tangentLength = Math.hypot(1, dzdx);
      positions.push(x, y, z);
      normals.push(
        -dzdx / normalLength,
        -dzdy / normalLength,
        1 / normalLength,
      );
      // V is flipped to match glTF's top-left UV convention, so the tangent
      // frame's bitangent must use the opposite handedness.
      tangents.push(1 / tangentLength, 0, dzdx / tangentLength, -1);
      uv.push(x, 1 - y);
    }
  }
  for (let v = 0; v < vSegments; v++) {
    for (let u = 0; u < uSegments; u++) {
      const a = v * stride + u;
      const b = a + 1;
      const c = a + stride;
      const d = c + 1;
      indices.push(a, b, c, b, d, c);
    }
  }
  return {
    positions: new Float32Array(positions),
    normals: new Float32Array(normals),
    tangents: new Float32Array(tangents),
    uv: new Float32Array(uv),
    indices: new Uint32Array(indices),
  };
}

export function paintedQuad(): GeometryInput {
  // Sample texel centers so repeat wrapping and linear filtering cannot blend
  // the first and last columns at the geometry boundary.
  const texelInset = 0.5 / 16;
  const texelEdge = 1 - texelInset;
  return {
    positions: new Float32Array([0, 0, 0, 1, 0, 0, 1, 1, 0, 0, 1, 0]),
    normals: new Float32Array([0, 0, 1, 0, 0, 1, 0, 0, 1, 0, 0, 1]),
    uv: new Float32Array([
      texelInset,
      texelEdge,
      texelEdge,
      texelEdge,
      texelEdge,
      texelInset,
      texelInset,
      texelInset,
    ]),
    indices: new Uint32Array([0, 1, 2, 0, 2, 3]),
  };
}

const pngSignature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);

function crc32(bytes: Uint8Array): number {
  let crc = 0xffffffff;
  for (const byte of bytes) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit++) {
      crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0);
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

function pngChunk(type: string, data: Buffer): Buffer {
  const name = Buffer.from(type, "ascii");
  const length = Buffer.alloc(4);
  const checksum = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  checksum.writeUInt32BE(crc32(Buffer.concat([name, data])));
  return Buffer.concat([length, name, data, checksum]);
}

export function paintedTexture(transparent: boolean): Buffer {
  const size = 16;
  const rows = Buffer.alloc(size * (1 + size * 4));
  for (let y = 0; y < size; y++) {
    for (let x = 0; x < size; x++) {
      const offset = y * (1 + size * 4) + 1 + x * 4;
      const greenStripe = x >= 6 && x < 10 && y >= 7 && y < 9;
      const color = transparent
        ? [255, 255, 255, Math.round((255 * x) / 15)]
        : greenStripe
          ? [16, 220, 40, 255]
          : y < 8
            ? [128 + Math.round((127 * x) / 15), 24, 32, 255]
            : [24, 32, 128 + Math.round((127 * x) / 15), 255];
      rows.set(color, offset);
    }
  }
  const header = Buffer.alloc(13);
  header.writeUInt32BE(size);
  header.writeUInt32BE(size, 4);
  header[8] = 8;
  header[9] = 6;
  return Buffer.concat([
    pngSignature,
    pngChunk("IHDR", header),
    pngChunk("IDAT", deflateSync(rows, { level: 9 })),
    pngChunk("IEND", Buffer.alloc(0)),
  ]);
}
