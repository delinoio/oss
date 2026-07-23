import { createHash } from "node:crypto";
import { readdir, readFile } from "node:fs/promises";
import { join, relative, resolve } from "node:path";

import { runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const distRoot = join(appRoot, "dist");

async function filesUnder(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const files = [];

  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await filesUnder(path)));
    } else if (entry.isFile()) {
      files.push(path);
    }
  }

  return files.toSorted();
}

async function artifactDigest() {
  const digest = createHash("sha256");
  const files = await filesUnder(distRoot);

  for (const file of files) {
    digest.update(relative(distRoot, file).replaceAll("\\", "/"));
    digest.update("\0");
    digest.update(await readFile(file));
    digest.update("\0");
  }

  return {
    digest: digest.digest("hex"),
    files: files.map((file) => relative(distRoot, file).replaceAll("\\", "/")),
  };
}

await runPackageManager(["run", "build"], { cwd: appRoot });
const first = await artifactDigest();
await runPackageManager(["run", "build"], { cwd: appRoot });
const second = await artifactDigest();

if (JSON.stringify(first) !== JSON.stringify(second)) {
  throw new Error(
    `frontend rebuild was not deterministic:\n${JSON.stringify({ first, second }, null, 2)}`,
  );
}

console.log(
  JSON.stringify({
    check: "devhud-frontend-reproducibility",
    status: "passed",
    ...second,
  }),
);
