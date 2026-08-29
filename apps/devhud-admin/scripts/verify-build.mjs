import { readFile, readdir } from "node:fs/promises";
import { resolve } from "node:path";

const dist = resolve("dist");
const index = await readFile(resolve(dist, "index.html"), "utf8");
if (!index.includes("/admin/")) {
  throw new Error("Production assets are not rooted at /admin/.");
}

async function files(directory) {
  const result = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const target = resolve(directory, entry.name);
    if (entry.isDirectory()) result.push(...(await files(target)));
    else result.push(target);
  }
  return result;
}

const output = await files(dist);
if (output.some((file) => file.endsWith(".map"))) {
  throw new Error("Production source maps must not be emitted.");
}
if (!output.some((file) => /index[.][a-f0-9]{8}[.]js$/.test(file))) {
  throw new Error("Hashed administrator JavaScript asset is missing.");
}
