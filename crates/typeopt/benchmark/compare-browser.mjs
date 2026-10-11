// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { isDeepStrictEqual } from "node:util";
import { readFile } from "node:fs/promises";
import path from "node:path";

// Browser acceptance passes only when the baseline and optimized runs used the
// pinned runtime and produced identical normalized scenes and exports.
if (process.argv.length !== 4)
  throw new Error(
    "Usage: node compare-browser.mjs <baseline-evidence> <optimized-evidence>",
  );
const pin = JSON.parse(
  await readFile(new URL("./pin.json", import.meta.url), "utf8"),
);
async function evidence(directory) {
  try {
    return JSON.parse(
      await readFile(path.join(directory, "browser.json"), "utf8"),
    );
  } catch (error) {
    throw new Error(
      `${directory} lacks browser.json; record it with benchmark/browser.mjs`,
      { cause: error },
    );
  }
}
const baseline = await evidence(process.argv[2]);
const optimized = await evidence(process.argv[3]);
const mismatches = [];
for (const [name, run] of [
  ["baseline", baseline],
  ["optimized", optimized],
]) {
  if (run.schema_version !== 0) mismatches.push(`${name}.schema_version`);
  if (run.checks_passed !== true) mismatches.push(`${name}.checks_passed`);
  if (run.node !== pin.node) mismatches.push(`${name}.node`);
  if (run.playwright !== pin.playwright) mismatches.push(`${name}.playwright`);
  if (run.browser !== pin.chromium) mismatches.push(`${name}.browser`);
}
const checks = [
  ...new Set([
    ...Object.keys(baseline.observations ?? {}),
    ...Object.keys(optimized.observations ?? {}),
  ]),
].sort();
for (const check of checks)
  if (
    !isDeepStrictEqual(
      baseline.observations?.[check],
      optimized.observations?.[check],
    )
  )
    mismatches.push(`observations.${check}`);
for (const format of ["png", "svg"])
  if (
    baseline.exports?.[format] === undefined ||
    !isDeepStrictEqual(baseline.exports[format], optimized.exports?.[format])
  )
    mismatches.push(`exports.${format}`);
const passed = checks.length > 0 && mismatches.length === 0;
console.log(
  JSON.stringify({ schema_version: 0, checks, mismatches, passed }, null, 2),
);
if (!passed) process.exitCode = 1;
