// SPDX-License-Identifier: Apache-2.0
import { randomUUID } from "node:crypto";
import { readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";

export function writeLocalizationOutput(path, value, check = false) {
  let existing;
  try { existing = readFileSync(path, "utf8"); }
  catch (error) { if (error.code !== "ENOENT" || check) throw error; }
  if (existing === value) return;
  if (check) throw new Error(`Stale localization output: ${path}`);
  // Concurrent frontend/widget preparation must never truncate a catalog while
  // TypeScript or a native compiler reads it. Publish each reconciled output by
  // same-directory replacement; unchanged output does not need publication.
  const temporary = `${path}.${randomUUID()}.tmp`;
  try {
    writeFileSync(temporary, value, { flag: "wx" });
    renameSync(temporary, path);
  } finally { rmSync(temporary, { force: true }); }
}
