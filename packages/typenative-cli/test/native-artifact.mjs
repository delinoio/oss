import { readFile } from "node:fs/promises";
import { isNativeExecutable } from "../dist/core.js";

export async function readNativeArtifact(path, target) {
  try {
    const bytes = await readFile(path);
    if (isNativeExecutable(bytes, target)) return bytes;
  } catch {}
  throw new Error("TN_NATIVE_ARTIFACT_INVALID");
}
