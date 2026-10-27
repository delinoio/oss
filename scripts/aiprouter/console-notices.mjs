// SPDX-License-Identifier: Apache-2.0
// Include complete available license/notice files for installed console build inputs.
import { readdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

const [store, output] = process.argv.slice(2);
if (!store || !output)
  throw new Error("Usage: console-notices.mjs <pnpm-store> <output>");
const notices = [];
for (const entry of await readdir(store, { withFileTypes: true })) {
  if (!entry.isDirectory() || entry.name === "node_modules") continue;
  const modules = join(store, entry.name, "node_modules");
  let entries;
  try {
    entries = await readdir(modules, { withFileTypes: true });
  } catch (error) {
    if (error.code === "ENOENT") continue;
    throw error;
  }
  const packages = [];
  for (const item of entries) {
    if (!item.isDirectory()) continue;
    if (item.name.startsWith("@")) {
      for (const scoped of await readdir(join(modules, item.name), {
        withFileTypes: true,
      })) {
        if (scoped.isDirectory())
          packages.push(join(modules, item.name, scoped.name));
      }
    } else packages.push(join(modules, item.name));
  }
  for (const directory of packages) {
    const manifest = JSON.parse(
      await readFile(join(directory, "package.json"), "utf8"),
    );
    notices.push(
      `\n=== ${manifest.name}@${manifest.version} (${JSON.stringify(manifest.license ?? "license not declared")}) ===\n`,
    );
    // Retain nested vendor licenses as well as each package's own license.
    async function collect(path) {
      for (const file of await readdir(path, { withFileTypes: true })) {
        if (
          file.isDirectory() &&
          file.name !== "node_modules" &&
          file.name !== ".git"
        )
          await collect(join(path, file.name));
        else if (
          file.isFile() &&
          /^(licen[cs]e|notice|copying)([._-]|$)/i.test(file.name)
        ) {
          notices.push(
            `\n--- ${join(path, file.name).slice(directory.length + 1)} ---\n`,
          );
          notices.push(await readFile(join(path, file.name), "utf8"));
        }
      }
    }
    await collect(directory);
  }
}
if (!notices.length) throw new Error("No console dependency notices found");
await writeFile(output, notices.join(""));
