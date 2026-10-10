// SPDX-License-Identifier: Apache-2.0
import { closeSync, openSync, readSync, realpathSync, statSync } from "node:fs";
import { extname, isAbsolute, join, relative, resolve, sep, win32 } from "node:path";

function readPrefix(path, limit) {
  const fd = openSync(path, "r");
  try {
    const bytes = Buffer.alloc(limit);
    let length = 0;
    while (length < limit) {
      const count = readSync(fd, bytes, length, limit - length, null);
      if (!count) break;
      length += count;
    }
    return bytes.subarray(0, length);
  } finally {
    closeSync(fd);
  }
}

// Resolve the installed package contract instead of depending on its current
// layout or a Windows .cmd shim. Keep this boundary for future Buf bin changes.
export function resolveBufEntry(root) {
  const packageRoot = realpathSync(join(root, "node_modules/@bufbuild/buf"));
  const manifestText = readPrefix(join(packageRoot, "package.json"), 64 * 1024 + 1);
  if (manifestText.length > 64 * 1024) throw new Error("Installed Buf package manifest exceeds its limit");
  const manifest = JSON.parse(manifestText.toString("utf8"));
  const bin = manifest?.bin?.buf;
  if (manifest?.name !== "@bufbuild/buf" || typeof bin !== "string" || !bin || bin.length > 4096 || bin.includes("\\") || bin.includes("\0") || isAbsolute(bin) || win32.parse(bin).root) {
    throw new Error("Installed Buf package has no valid bin.buf entry");
  }
  const contained = (path) => {
    const location = relative(packageRoot, path);
    return location && location !== ".." && !location.startsWith(`..${sep}`) && !isAbsolute(location);
  };
  const declared = resolve(packageRoot, bin);
  if (!contained(declared)) throw new Error("Installed Buf bin.buf escapes its package");
  const entry = realpathSync(declared);
  if (!contained(entry) || !statSync(entry).isFile()) throw new Error("Installed Buf bin.buf is not a contained file");
  const header = readPrefix(entry, 1024).toString("utf8");
  if (![".js", ".cjs", ".mjs"].includes(extname(entry)) && !/^#!(?:\/usr\/bin\/env[ \t]+(?:-S[ \t]+)?node|\/(?:[^ \t\r\n]+\/)?node)(?:[ \t]|[\r\n]|$)/u.test(header)) {
    throw new Error("Installed Buf bin.buf is not a Node entry");
  }
  return entry;
}
