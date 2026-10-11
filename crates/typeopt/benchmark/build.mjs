// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import { readFile, writeFile, realpath, stat } from "node:fs/promises";
import path from "node:path";
import { typeopt } from "../examples/vite/adapter.mjs";

const [checkoutArg, destinationArg, optimizedArg] = process.argv.slice(2);
if (!checkoutArg || !destinationArg)
  throw new Error(
    "Usage: node build.mjs <checkout> <new-build-dir> [typeopt-output]",
  );
const pin = JSON.parse(
  await readFile(new URL("./pin.json", import.meta.url), "utf8"),
);
if (process.versions.node !== pin.node)
  throw new Error(`Use pinned Node ${pin.node}`);
Object.assign(process.env, pin.environment);
const started = performance.now();
const checkout = await realpath(checkoutArg);
const destination = path.resolve(destinationArg);
const optimized = optimizedArg && path.resolve(optimizedArg);
try {
  await stat(destination);
  throw new Error("Build destination already exists");
} catch (error) {
  if (error.code !== "ENOENT") throw error;
}
const require = createRequire(path.join(checkout, "package.json"));
const { build, loadConfigFromFile } = await import(
  pathToFileURL(
    path.join(
      path.dirname(require.resolve("vite/package.json")),
      "dist/node/index.js",
    ),
  ).href
);
const root = path.join(checkout, "excalidraw-app");
process.chdir(root);
const loaded = await loadConfigFromFile(
  { command: "build", mode: "production" },
  path.join(root, "vite.config.mts"),
);
if (!loaded) throw new Error("Excalidraw Vite configuration was not found");
await build({
  ...loaded.config,
  root,
  configFile: false,
  mode: "production",
  plugins: [
    ...(optimized ? [typeopt(optimized)] : []),
    ...(loaded.config.plugins ?? []),
  ],
  build: {
    ...loaded.config.build,
    outDir: destination,
    emptyOutDir: false,
    sourcemap: true,
  },
});

await writeFile(
  path.join(destination, "typeopt-build.json"),
  JSON.stringify(
    {
      schema_version: 0,
      revision: pin.revision,
      node: process.versions.node,
      mode: "production",
      sourcemap: true,
      environment: pin.environment,
      optimized: Boolean(optimized),
      elapsed_ms: performance.now() - started,
      peak_rss_bytes: process.resourceUsage().maxRSS * 1024,
    },
    null,
    2,
  ),
);
