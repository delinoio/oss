import { ForgeError } from "../errors.js";
import { ErrorCode, limits } from "../types.js";
import { AnimationInterpolation, AnimationPath, type AnimationSamplerInput, type GeometryInput } from "./components.js";

const paths = [AnimationPath.Translation, AnimationPath.Rotation, AnimationPath.Scale, AnimationPath.Weights];
const interpolations = [AnimationInterpolation.Step, AnimationInterpolation.Linear, AnimationInterpolation.Cubic];
export const maxMorphTargets = 64;
export const malformed = () => new ForgeError(ErrorCode.MalformedInput, "Malformed scene asset data.");
export function budget(length: number, available: number): void {
  if (!Number.isSafeInteger(length) || length < 0 || length > limits.geometryBytes || length > available) {
    throw new ForgeError(ErrorCode.ResourceLimit, "Scene asset data exceeds its byte budget.");
  }
}
function keys(value: object, allowed: string[]): void {
  if (Object.keys(value).some(key => !allowed.includes(key))) throw malformed();
}
export function animationWidth(path: AnimationPath, components?: number): number {
  if (!paths.includes(path)) throw malformed();
  const width = path === AnimationPath.Weights ? components : path === AnimationPath.Rotation ? 4 : 3;
  if (!Number.isInteger(width) || width! < 1 || width! > maxMorphTargets) throw malformed();
  return width!;
}
export function samplerBytes(input: AnimationSamplerInput, available: number): Buffer {
  if (!input || typeof input !== "object") throw malformed();
  keys(input, ["path", "times", "values", "interpolation", "inTangents", "outTangents"]);
  const mode = input.interpolation ?? AnimationInterpolation.Linear;
  if (!(input.times instanceof Float32Array) || !(input.values instanceof Float32Array) || !input.times.length || !interpolations.includes(mode)) throw malformed();
  const width = animationWidth(input.path, input.values.length / input.times.length);
  if (input.values.length !== input.times.length * width) throw malformed();
  const cubic = mode === AnimationInterpolation.Cubic;
  if (cubic ? (!(input.inTangents instanceof Float32Array) || !(input.outTangents instanceof Float32Array) || input.inTangents.length !== input.values.length || input.outTangents.length !== input.values.length || input.times.length < 2) : (input.inTangents !== undefined || input.outTangents !== undefined)) throw malformed();
  const arrays = [input.times, input.values, ...(cubic ? [input.inTangents!, input.outTangents!] : [])];
  const length = 20 + arrays.reduce((n, array) => n + array.byteLength, 0);
  budget(length, available);
  const bytes = Buffer.allocUnsafe(length);
  bytes.write("FSA1"); bytes.writeUInt32LE(paths.indexOf(input.path), 4); bytes.writeUInt32LE(interpolations.indexOf(mode), 8);
  bytes.writeUInt32LE(width, 12); bytes.writeUInt32LE(input.times.length, 16);
  let offset = 20;
  for (const array of arrays) for (const value of array) { bytes.writeFloatLE(value, offset); offset += 4; }
  return bytes;
}

export function geometryBytes(input: GeometryInput, available: number): Buffer {
  if (!input || typeof input !== "object") throw malformed();
  keys(input, ["positions", "normals", "tangents", "uv", "indices", "skin", "morphTargets"]);
  const vertices = input.positions?.length / 3;
  if (!Number.isInteger(vertices) || vertices < 3 || !(input.positions instanceof Float32Array) || !(input.normals instanceof Float32Array) || !(input.indices instanceof Uint32Array) || input.normals.length !== vertices * 3 || (input.uv !== undefined && (!(input.uv instanceof Float32Array) || input.uv.length !== vertices * 2)) || (input.tangents !== undefined && (!(input.tangents instanceof Float32Array) || input.tangents.length !== vertices * 4))) throw malformed();
  const arrays = [input.positions, input.normals, ...(input.tangents ? [input.tangents] : []), ...(input.uv ? [input.uv] : []), input.indices];
  const baseLength = 16 + arrays.reduce((n, a) => n + a.byteLength, 0);
  if (input.skin !== undefined) {
    if (!input.skin || typeof input.skin !== "object") throw malformed();
    keys(input.skin, ["joints", "weights"]);
    if (!(input.skin.joints instanceof Uint16Array) || !(input.skin.weights instanceof Float32Array) || input.skin.joints.length !== vertices * 4 || input.skin.weights.length !== vertices * 4) throw malformed();
  }
  if (input.morphTargets !== undefined && (!Array.isArray(input.morphTargets) || input.morphTargets.length > maxMorphTargets)) throw malformed();
  const morphs = (input.morphTargets ?? []).map(target => {
    if (!target || typeof target !== "object") throw malformed();
    keys(target, ["name", "positions", "normals"]);
    if (typeof target.name !== "string" || !target.name.length || target.name.includes("\0") || Buffer.byteLength(target.name) > 256 || !(target.positions instanceof Float32Array) || target.positions.length !== vertices * 3 || (target.normals !== undefined && (!(target.normals instanceof Float32Array) || target.normals.length !== vertices * 3))) throw malformed();
    return target;
  });
  if (new Set(morphs.map(m => m.name)).size !== morphs.length) throw malformed();
  const extended = !!input.skin || morphs.length > 0;
  const length = baseLength + (extended ? 16 : 0) + (input.skin ? vertices * 24 : 0) + morphs.reduce((n, m) => n + 8 + Buffer.byteLength(m.name) + m.positions.byteLength + (m.normals?.byteLength ?? 0), 0);
  budget(length, available);
  const bytes = Buffer.allocUnsafe(length);
  let offset = 0;
  if (extended) {
    bytes.write("FSG2"); bytes.writeUInt32LE(baseLength, 4); bytes.writeUInt32LE(input.skin ? 1 : 0, 8); bytes.writeUInt32LE(morphs.length, 12); offset = 16;
  }
  bytes.write("FSG1", offset); bytes.writeUInt32LE((input.uv ? 1 : 0) | (input.tangents ? 2 : 0), offset + 4); bytes.writeUInt32LE(vertices, offset + 8); bytes.writeUInt32LE(input.indices.length, offset + 12); offset += 16;
  const floats = (array: Float32Array) => { for (const value of array) { bytes.writeFloatLE(value, offset); offset += 4; } };
  for (const array of arrays) {
    if (array instanceof Uint32Array) for (const value of array) { bytes.writeUInt32LE(value, offset); offset += 4; }
    else floats(array);
  }
  if (input.skin) {
    for (const value of input.skin.joints) { bytes.writeUInt16LE(value, offset); offset += 2; }
    floats(input.skin.weights);
  }
  for (const morph of morphs) {
    const nameLength = Buffer.byteLength(morph.name);
    bytes.writeUInt32LE(nameLength, offset); bytes.writeUInt32LE(morph.normals ? 1 : 0, offset + 4); offset += 8;
    bytes.write(morph.name, offset); offset += nameLength; floats(morph.positions); if (morph.normals) floats(morph.normals);
  }
  return bytes;
}
