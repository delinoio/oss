// SPDX-License-Identifier: Apache-2.0
import { lstat, readlink, realpath } from "node:fs/promises";
import {
  basename,
  dirname,
  isAbsolute,
  join,
  relative,
  resolve,
  sep,
} from "node:path";

async function canonicalPath(path) {
  const absolute = resolve(path);
  try {
    return await realpath(absolute);
  } catch (error) {
    if (error.code !== "ENOENT") throw error;
    const parent = dirname(absolute);
    if (parent === absolute) throw error;
    // realpath cannot resolve a missing final component. Follow an existing
    // dangling symlink too, then resolve the nearest existing ancestor before
    // appending missing components. Other filesystem errors remain failures.
    try {
      if ((await lstat(absolute)).isSymbolicLink()) {
        return canonicalPath(resolve(parent, await readlink(absolute)));
      }
    } catch (statError) {
      if (statError.code !== "ENOENT") throw statError;
    }
    return join(await canonicalPath(parent), basename(absolute));
  }
}

export async function externalBuildPath(repository, path) {
  const root = await realpath(repository);
  const canonical = await canonicalPath(path);
  const suffix = relative(root, canonical);
  if (
    suffix === "" ||
    (suffix !== ".." && !suffix.startsWith(".." + sep) && !isAbsolute(suffix))
  ) {
    throw new Error(
      "Keep dependencies and native build artifacts outside the repository.",
    );
  }
  return canonical;
}
