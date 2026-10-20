// SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

export function sha256(bytes) {
  return createHash("sha256").update(bytes).digest("hex");
}

export async function snapshotBuildInputs(repository, paths) {
  const files = new Map();
  const hashes = {};
  for (const path of paths) {
    const bytes = await readFile(join(repository, path));
    files.set(path, bytes);
    hashes[path] = sha256(bytes);
  }
  // Callers copy/transform these same buffers, rather than rereading a source
  // which may have changed after its digest was computed.
  return { files, hashes };
}
