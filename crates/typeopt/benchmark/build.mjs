// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import {
  mkdtemp,
  readFile,
  writeFile,
  realpath,
  rm,
  stat,
} from "node:fs/promises";
import { tmpdir } from "node:os";
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
// Verify the checkout identity before building so the evidence records the
// source that was actually built, not only the pinned value.
const revision = execFileSync("git", ["-C", checkout, "rev-parse", "HEAD"], {
  encoding: "utf8",
}).trim();
if (revision !== pin.revision)
  throw new Error(
    `Checkout revision ${revision} does not match pinned ${pin.revision}`,
  );
// The compatibility patch makes a dirty worktree expected, so compare the
// whole worktree (tracked and untracked, non-ignored files) with HEAD plus
// exactly that patch. Temporary indexes leave the checkout's index untouched.
const patch = fileURLToPath(new URL(pin.compatibility_patch, import.meta.url));
const indexDirectory = await mkdtemp(path.join(tmpdir(), "typeopt-bench-"));
let sourceTree;
try {
  const treeOf = (name, steps) => {
    const env = {
      ...process.env,
      GIT_INDEX_FILE: path.join(indexDirectory, name),
    };
    const git = (...args) =>
      execFileSync("git", ["-C", checkout, ...args], { encoding: "utf8", env });
    git("read-tree", "HEAD");
    steps(git);
    return git("write-tree").trim();
  };
  const expected = treeOf("expected", (git) => git("apply", "--cached", patch));
  sourceTree = treeOf("actual", (git) => git("add", "--all"));
  if (sourceTree !== expected)
    throw new Error(
      `Checkout worktree ${sourceTree} does not equal the pinned revision plus ${pin.compatibility_patch} (${expected}); run git -C <checkout> status`,
    );
} finally {
  await rm(indexDirectory, { recursive: true, force: true });
}
const yarnLockSha256 = createHash("sha256")
  .update(await readFile(path.join(checkout, "yarn.lock")))
  .digest("hex");
if (yarnLockSha256 !== pin.yarn_lock_sha256)
  throw new Error(
    `yarn.lock SHA-256 ${yarnLockSha256} does not match pinned ${pin.yarn_lock_sha256}`,
  );
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
      revision,
      source_tree: sourceTree,
      yarn_lock_sha256: yarnLockSha256,
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
