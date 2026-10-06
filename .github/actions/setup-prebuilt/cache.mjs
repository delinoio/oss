import { appendFile } from "node:fs/promises";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { dependencyPaths } from "../../../scripts/prebuilt-dependencies.mjs";

export function prebuiltCache(root, id, options) {
  const paths = dependencyPaths(root, id, options);
  return {
    path: paths.root,
    key: `prebuilt-v1-${id}-${paths.dependency.release}-${paths.target}-${paths.asset.sha256}-${paths.asset.binarySha256}`,
  };
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url) && process.env.GITHUB_OUTPUT) {
  const cache = prebuiltCache(resolve(process.env.GITHUB_WORKSPACE), "tauri-cli");
  await appendFile(process.env.GITHUB_OUTPUT, `path=${cache.path}\nkey=${cache.key}\n`);
}
