// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";

/** Substitute code without redirecting module identity or asset resolution. */
export function typeopt(outputDirectory) {
  const root = path.resolve(outputDirectory);
  let modules;
  return {
    name: "typeopt-example",
    enforce: "pre",
    apply: "build",
    async buildStart() {
      const manifest = JSON.parse(
        await readFile(path.join(root, "modules.json"), "utf8"),
      );
      if (manifest.schema_version !== 0 || !Array.isArray(manifest.modules)) {
        throw new Error(
          "Unsupported typeopt module mapping; regenerate with the matching CLI",
        );
      }
      if (
        manifest.modules.some(
          (entry) =>
            entry.kind === "optimized" &&
            !/^[0-9a-f]{64}$/.test(entry.source_sha256 ?? ""),
        )
      ) {
        throw new Error(
          "typeopt module mapping lacks source digests; regenerate with the matching CLI",
        );
      }
      modules = new Map(
        manifest.modules.map((entry) => [path.normalize(entry.source), entry]),
      );
    },
    async load(id) {
      // Queries can request raw text, URLs or worker transformations. They must
      // keep their original loader semantics, rather than receive emitted JS.
      if (id.includes("?") || id.startsWith("\0")) return null;
      const entry = modules.get(path.normalize(id));
      if (!entry || entry.kind !== "optimized") return null;
      const resolveOutput = (file) => {
        const resolved = path.resolve(root, file);
        if (!resolved.startsWith(root + path.sep))
          throw new Error("typeopt output escapes its directory");
        return resolved;
      };
      // The optimizer and bundler run separately. Reject output whose source
      // changed afterward instead of silently bundling outdated code.
      const current = (await readFile(id, "utf8")).replace(/^\uFEFF/, "");
      const digest = createHash("sha256").update(current).digest("hex");
      if (digest !== entry.source_sha256)
        throw new Error(
          `typeopt output is stale for ${id}; rerun typeopt into a new output directory`,
        );
      const [code, map] = await Promise.all([
        readFile(resolveOutput(entry.output), "utf8"),
        readFile(resolveOutput(entry.source_map), "utf8"),
      ]);
      return { code, map: JSON.parse(map) };
    },
  };
}
