// SPDX-License-Identifier: Apache-2.0
import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const buf = fileURLToPath(
  new URL("../node_modules/@bufbuild/buf/bin/buf", import.meta.url),
);
const revision = execFileSync(
  "git",
  ["rev-parse", "--verify", "--end-of-options", `${process.argv[2]}^{commit}`],
  { encoding: "utf8" },
).trim();
const temporary = mkdtempSync(join(tmpdir(), "proto-baseline-"));
const run = (...args) =>
  execFileSync(process.execPath, [buf, ...args], {
    stdio: "inherit",
    env: { ...process.env, GIT_LFS_SKIP_SMUDGE: "1" },
  });
try {
  const original = join(temporary, "original.json");
  const projected = join(temporary, "projected.json");
  run(
    "build",
    `.git#ref=${revision}`,
    "--as-file-descriptor-set",
    "--output",
    original,
  );
  const baseline = JSON.parse(readFileSync(original, "utf8"));
  // DevHud and DeliDev retired their APIs; Delcore and bundlewhy moved repositories. Keep FILE
  // checks for owned packages. Remove this projection once all baselines
  // omit the retired and transferred schemas.
  baseline.file = baseline.file.filter(
    (file) =>
      !["delidev.v1", "delcore.v1", "devhud.v1", "bundlewhy.v1"].includes(
        file.package,
      ),
  );
  // Retired services can be the only consumers of a protobuf well-known type.
  // Drop those orphan imports too, while retaining every owned schema and all
  // well-known types still reachable through its imports.
  const files = new Map(baseline.file.map((file) => [file.name, file]));
  const reachable = new Set();
  const visit = (file) => {
    if (!file || reachable.has(file.name)) return;
    reachable.add(file.name);
    for (const dependency of file.dependency ?? [])
      visit(files.get(dependency));
  };
  for (const file of baseline.file) {
    if (file.package !== "google.protobuf") visit(file);
  }
  baseline.file = baseline.file.filter((file) => reachable.has(file.name));
  writeFileSync(projected, JSON.stringify(baseline));
  run("breaking", "--against", projected);
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
