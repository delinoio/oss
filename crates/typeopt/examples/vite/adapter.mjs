// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";

const sha256 = (text) => createHash("sha256").update(text).digest("hex");
const DIGEST = /^[0-9a-f]{64}$/;

/** Reads a text input the way the compiler does (a UTF-8 BOM is not part of the text). */
async function digestOf(file, { stripBom }) {
  let content;
  try {
    content = await readFile(file, stripBom ? "utf8" : undefined);
  } catch (error) {
    if (error.code === "ENOENT") return null;
    throw error;
  }
  return sha256(stripBom ? content.replace(/^﻿/, "") : content);
}

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
            entry.kind !== "passthrough" &&
            !DIGEST.test(entry.source_sha256 ?? ""),
        )
      ) {
        throw new Error(
          "typeopt module mapping lacks source digests; regenerate with the matching CLI",
        );
      }
      // Optimized modules may depend on facts from any analyzed module,
      // configuration or declaration. Validate the whole analysis snapshot
      // before consuming any output, so a changed dependency invalidates
      // every result that could depend on it.
      const inputs = manifest.inputs ?? [];
      if (!Array.isArray(inputs) || inputs.some((i) => !DIGEST.test(i.sha256 ?? "")))
        throw new Error(
          "typeopt module mapping has an invalid input snapshot; regenerate with the matching CLI",
        );
      const stale = [];
      await Promise.all([
        ...manifest.modules
          .filter((entry) => entry.kind !== "passthrough" || entry.source_sha256)
          .map(async (entry) => {
            const digest = await digestOf(entry.source, { stripBom: true });
            if (digest !== entry.source_sha256) stale.push(entry.source);
          }),
        ...inputs.map(async (input) => {
          const digest = await digestOf(input.path, {
            stripBom: input.kind !== "configuration",
          });
          if (digest !== input.sha256) stale.push(input.path);
        }),
      ]);
      if (stale.length)
        throw new Error(
          `typeopt output is stale: ${stale.sort()[0]}${stale.length > 1 ? ` and ${stale.length - 1} more input(s)` : ""} changed; rerun typeopt into a new output directory`,
        );
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
      // changed after the build started instead of bundling outdated code.
      const digest = await digestOf(id, { stripBom: true });
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
