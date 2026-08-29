import { cp, mkdir, readFile, readdir, rm } from "node:fs/promises";
import { relative, resolve } from "node:path";

const source = resolve("dist");
const destination = resolve("../../servers/devhud-api/internal/adminassets/dist");
const mode = process.argv[2];

if (!new Set(["--check", "--write"]).has(mode)) {
  throw new Error("usage: embedded-assets.mjs <--check|--write>");
}

async function inventory(root) {
  async function walk(directory) {
    const output = [];
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const path = resolve(directory, entry.name);
      if (entry.isDirectory()) output.push(...await walk(path));
      else output.push(relative(root, path).replaceAll("\\", "/"));
    }
    return output;
  }
  return (await walk(root)).sort();
}

if (mode === "--write") {
  await rm(destination, { recursive: true, force: true });
  await mkdir(destination, { recursive: true });
  await cp(source, destination, { recursive: true });
  process.stderr.write("[devhud.admin] refreshed embedded administrator assets\n");
} else {
  const sourceFiles = await inventory(source);
  const destinationFiles = await inventory(destination);
  if (JSON.stringify(sourceFiles) !== JSON.stringify(destinationFiles)) {
    throw new Error("embedded administrator asset inventory is stale; run pnpm --filter devhud-admin build:embedded");
  }
  for (const path of sourceFiles) {
    const [generated, committed] = await Promise.all([
      readFile(resolve(source, path)),
      readFile(resolve(destination, path)),
    ]);
    if (!generated.equals(committed)) {
      throw new Error(`embedded administrator asset is stale: ${path}`);
    }
  }
  process.stderr.write(`[devhud.admin] verified ${sourceFiles.length} embedded assets\n`);
}
