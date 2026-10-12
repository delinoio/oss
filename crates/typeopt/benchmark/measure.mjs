// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { gzipSync, brotliCompressSync, constants } from "node:zlib";
// Vite appends an 8-character content hash to chunk names. Content changes
// alter the hash, so asset sets are compared by name without it.
const CONTENT_HASH = /-[A-Za-z0-9_-]{8}(?=\.(?:js|mjs|cjs)$)/;
const logical = (file) => file.replace(CONTENT_HASH, "");
function assetSetDifference(from, to) {
  const counts = new Map();
  for (const file of from.files)
    counts.set(logical(file.path), (counts.get(logical(file.path)) ?? 0) + 1);
  for (const file of to.files)
    counts.set(logical(file.path), (counts.get(logical(file.path)) ?? 0) - 1);
  const removed = [];
  const added = [];
  for (const [name, count] of [...counts].sort(([a], [b]) =>
    a.localeCompare(b, "en"),
  )) {
    for (let i = 0; i < count; i++) removed.push(name);
    for (let i = 0; i < -count; i++) added.push(name);
  }
  return { removed, added };
}
async function measure(root) {
  const result = { files: [], raw: 0, gzip: 0, brotli: 0, texts: new Map() };
  async function walk(directory) {
    for (const entry of (
      await readdir(directory, { withFileTypes: true })
    ).sort((a, b) => a.name.localeCompare(b.name, "en"))) {
      const full = path.join(directory, entry.name);
      if (entry.isDirectory()) await walk(full);
      else if (entry.isFile() && /\.(?:js|mjs|cjs|html)$/.test(entry.name)) {
        const content = await readFile(full);
        const relative = path.relative(root, full).split(path.sep).join("/");
        result.texts.set(relative, content.toString("utf8"));
        if (entry.name.endsWith(".html")) continue;
        const file = {
          path: relative,
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
// A chunk reference is any quoted relative or root-relative `.js` path in an
// HTML entry or JS chunk. References that name a logical asset of any build
// must resolve to a file of this build; otherwise a removed chunk is still
// loaded somewhere. Unrelated strings that name no asset are ignored.
const REFERENCE =
  /["'`(=]((?:\.{1,2}\/|\/)?(?:[\w.@-]+\/)*[\w.@-]+\.m?js)(?=["'`)\s>])/g;
function brokenReferences(build, knownLogical) {
  const present = new Set(build.files.map((f) => f.path));
  const broken = new Set();
  for (const [file, text] of build.texts) {
    for (const match of text.matchAll(REFERENCE)) {
      const reference = match[1];
      const candidates = reference.startsWith("/")
        ? [reference.slice(1)]
        : reference.startsWith(".")
          ? [
              path.posix.normalize(
                path.posix.join(path.posix.dirname(file), reference),
              ),
            ]
          : [reference, path.posix.join(path.posix.dirname(file), reference)];
      if (candidates.some((c) => present.has(c))) continue;
      if (candidates.some((c) => knownLogical.has(logical(c))))
        broken.add(`${file} -> ${reference}`);
    }
  }
  return [...broken].sort();
}
const usage =
  "Usage: node measure.mjs <original-build> <optimized-build> | <original-build> <existing-typeopt-build> <extended-typeopt-build>";
const roots = process.argv.slice(2);
if (roots.length !== 2 && roots.length !== 3) throw new Error(usage);
// Size totals are a valid comparison only when all builds come from the
// same verified source and build settings.
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
const builds = await Promise.all(roots.map(provenance));
const names =
  roots.length === 2
    ? ["original", "optimized"]
    : ["original", "existing", "extended"];
const mismatches = [];
for (let i = 1; i < builds.length; i++)
  for (const key of PROVENANCE)
    if (JSON.stringify(builds[0][key]) !== JSON.stringify(builds[i][key]))
      mismatches.push(`${names[i]}.${key}`);
if (builds[0].optimized !== false) mismatches.push("original.optimized");
for (let i = 1; i < builds.length; i++)
  if (builds[i].optimized !== true) mismatches.push(`${names[i]}.optimized`);
for (let i = 1; i < builds.length; i++) {
  // The optimizer binary must come from a recorded, clean source revision.
  const optimizer = builds[i].optimizer;
  if (!/^[0-9a-f]{40}$/.test(optimizer?.source_revision ?? ""))
    mismatches.push(`${names[i]}.optimizer.source_revision`);
  if (optimizer?.source_dirty !== false)
    mismatches.push(`${names[i]}.optimizer.source_dirty`);
}
if (builds.length === 3) {
  // The existing optimizer is the pinned revision and writes report v0; the
  // extended one writes v1.
  if (builds[1].optimizer?.report_schema_version !== 0)
    mismatches.push("existing.optimizer");
  if (builds[1].optimizer?.source_revision !== pin.existing_typeopt_revision)
    mismatches.push("existing.optimizer.revision");
  if (!(builds[2].optimizer?.report_schema_version >= 1))
    mismatches.push("extended.optimizer");
  if (builds[2].optimizer?.source_revision === pin.existing_typeopt_revision)
    mismatches.push("extended.optimizer.revision");
}
const measured = await Promise.all(roots.map(measure));
const knownLogical = new Set(
  measured.flatMap((m) => m.files.map((f) => logical(f.path))),
);
const broken = Object.fromEntries(
  measured.map((m, i) => [names[i], brokenReferences(m, knownLogical)]),
);
const smaller = (a, b) =>
  ["raw", "gzip", "brotli"].every((key) => a[key] < b[key]);
const last = measured.at(-1);
const comparisons = measured.slice(0, -1).map((baseline, i) => ({
  baseline: names[i],
  ...assetSetDifference(baseline, last),
  strictly_smaller: smaller(last, baseline),
}));
const loadingOk = Object.values(broken).every((list) => !list.length);
const passed =
  !mismatches.length &&
  loadingOk &&
  comparisons.every((c) => c.strictly_smaller);
const summary = (m) => ({
  raw: m.raw,
  gzip: m.gzip,
  brotli: m.brotli,
  files: m.files,
});
console.log(
  JSON.stringify(
    {
      schema_version: 1,
      node: process.version,
      zlib: process.versions.zlib,
      brotli: process.versions.brotli,
      revision: builds[0].revision,
      source_tree: builds[0].source_tree,
      optimizers: Object.fromEntries(
        builds.map((b, i) => [names[i], b.optimizer ?? null]),
      ),
      provenance_mismatches: mismatches,
      builds: Object.fromEntries(
        measured.map((m, i) => [names[i], summary(m)]),
      ),
      // Removed chunks are legitimate when nothing still loads them.
      comparisons,
      broken_chunk_references: broken,
      passed,
    },
    null,
    2,
  ),
);
if (!passed) process.exitCode = 1;
