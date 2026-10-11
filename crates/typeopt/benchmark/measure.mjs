// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { gzipSync, brotliCompressSync, constants } from "node:zlib";
// Vite appends an 8-character content hash to chunk names. Content changes
// alter the hash, so asset sets are compared by name without it.
const CONTENT_HASH = /-[A-Za-z0-9_-]{8}(?=\.(?:js|mjs|cjs)$)/;
function logicalAssets(result) {
  return result.files.map((file) => file.path.replace(CONTENT_HASH, "")).sort();
}
function assetSetDifference(baseline, optimized) {
  const counts = new Map();
  for (const name of logicalAssets(baseline))
    counts.set(name, (counts.get(name) ?? 0) + 1);
  for (const name of logicalAssets(optimized))
    counts.set(name, (counts.get(name) ?? 0) - 1);
  const missing = [];
  const extra = [];
  for (const [name, count] of [...counts].sort(([a], [b]) =>
    a.localeCompare(b, "en"),
  )) {
    for (let i = 0; i < count; i++) missing.push(name);
    for (let i = 0; i < -count; i++) extra.push(name);
  }
  return { missing, extra };
}
async function measure(root) {
  const result = { files: [], raw: 0, gzip: 0, brotli: 0 };
  async function walk(directory) {
    for (const entry of (
      await readdir(directory, { withFileTypes: true })
    ).sort((a, b) => a.name.localeCompare(b.name, "en"))) {
      const full = path.join(directory, entry.name);
      if (entry.isDirectory()) await walk(full);
      else if (entry.isFile() && /\.(?:js|mjs|cjs)$/.test(entry.name)) {
        const content = await readFile(full);
        const file = {
          path: path.relative(root, full),
          raw: content.length,
          gzip: gzipSync(content, { level: 9 }).length,
          brotli: brotliCompressSync(content, {
            params: { [constants.BROTLI_PARAM_QUALITY]: 11 },
          }).length,
        };
        result.files.push(file);
        for (const key of ["raw", "gzip", "brotli"]) result[key] += file[key];
      }
    }
  }
  await walk(root);
  if (!result.files.length)
    throw new Error("No production JavaScript assets found");
  return result;
}
if (process.argv.length !== 4)
  throw new Error("Usage: node measure.mjs <baseline-build> <optimized-build>");
// Size totals are a valid before/after comparison only when both builds come
// from the same verified source and build settings.
const PROVENANCE = [
  "schema_version",
  "revision",
  "source_tree",
  "yarn_lock_sha256",
  "node",
  "vite",
  "mode",
  "sourcemap",
  "environment",
];
async function provenance(root) {
  try {
    return JSON.parse(
      await readFile(path.join(root, "typeopt-build.json"), "utf8"),
    );
  } catch (error) {
    throw new Error(
      `${root} lacks typeopt-build.json; build it with benchmark/build.mjs`,
      { cause: error },
    );
  }
}
// gzip and Brotli ship with Node, so the pinned Node fixes the compressors.
const pin = JSON.parse(
  await readFile(new URL("./pin.json", import.meta.url), "utf8"),
);
if (process.versions.node !== pin.node)
  throw new Error(`Measure with pinned Node ${pin.node}`);
const baselineBuild = await provenance(process.argv[2]);
const optimizedBuild = await provenance(process.argv[3]);
const provenanceMismatches = PROVENANCE.filter(
  (key) =>
    JSON.stringify(baselineBuild[key]) !== JSON.stringify(optimizedBuild[key]),
);
if (baselineBuild.optimized !== false)
  provenanceMismatches.push("baseline.optimized");
if (optimizedBuild.optimized !== true)
  provenanceMismatches.push("optimized.optimized");
const baseline = await measure(process.argv[2]);
const optimized = await measure(process.argv[3]);
// Size totals are comparable only when both builds emit the same logical
// JavaScript asset set; a missing chunk must not count as a reduction.
const assetSet = assetSetDifference(baseline, optimized);
const assetSetMatches = !assetSet.missing.length && !assetSet.extra.length;
const passed =
  !provenanceMismatches.length &&
  assetSetMatches &&
  ["raw", "gzip", "brotli"].every((key) => optimized[key] < baseline[key]);
console.log(
  JSON.stringify(
    {
      schema_version: 0,
      node: process.version,
      zlib: process.versions.zlib,
      brotli: process.versions.brotli,
      revision: baselineBuild.revision,
      source_tree: baselineBuild.source_tree,
      provenance_mismatches: provenanceMismatches,
      baseline,
      optimized,
      asset_set_matches: assetSetMatches,
      missing_from_optimized: assetSet.missing,
      extra_in_optimized: assetSet.extra,
      passed,
    },
    null,
    2,
  ),
);
if (!passed) process.exitCode = 1;
