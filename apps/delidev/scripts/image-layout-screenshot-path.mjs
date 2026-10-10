// SPDX-License-Identifier: Apache-2.0
import { lstat, realpath } from "node:fs/promises";
import { basename, dirname, isAbsolute, relative, resolve, sep } from "node:path";

const contains = (root, target) => {
  const distance = relative(root, target);
  return distance === "" || (!isAbsolute(distance) && distance !== ".." && !distance.startsWith(`..${sep}`));
};

// Resolve missing descendants without creating them. Reject dangling links rather
// than treating them as missing directories that could later redirect output.
export async function screenshotDirectory(value, checkout, cwd = process.cwd()) {
  if (!value) return undefined;
  const requested = resolve(cwd, value);
  const roots = [resolve(checkout), await realpath(checkout)];
  const ensureExternal = target => {
    if (roots.some(root => contains(root, target))) throw new Error("Screenshot destination must remain outside the checkout");
  };
  ensureExternal(requested);
  let ancestor = requested;
  const missing = [];
  while (true) {
    try {
      const destination = resolve(await realpath(ancestor), ...missing.slice().reverse());
      ensureExternal(destination);
      return destination;
    } catch (error) {
      if (error.code !== "ENOENT") throw error;
      // lstat still sees a dangling symlink: fail before any directory writes.
      let entry;
      try { entry = await lstat(ancestor); }
      catch (entryError) { if (entryError.code !== "ENOENT") throw entryError; }
      if (entry) throw error;
      const parent = dirname(ancestor);
      if (parent === ancestor) throw error;
      missing.push(basename(ancestor));
      ancestor = parent;
    }
  }
}
