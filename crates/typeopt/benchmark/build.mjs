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

const [checkoutArg, destinationArg, optimizedArg, optimizerSourceArg] =
  process.argv.slice(2);
if (!checkoutArg || !destinationArg || (optimizedArg && !optimizerSourceArg))
  throw new Error(
    "Usage: node build.mjs <checkout> <new-build-dir> [<typeopt-output> <typeopt-source-checkout>]",
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
// Ignored node_modules are outside the verified tree, so check the bundler
// that will actually run.
const vitePackage = require.resolve("vite/package.json");
const viteVersion = JSON.parse(await readFile(vitePackage, "utf8")).version;
if (viteVersion !== pin.vite)
  throw new Error(
    `Installed Vite ${viteVersion} does not match pinned ${pin.vite}`,
  );
const { build, loadConfigFromFile } = await import(
  pathToFileURL(path.join(path.dirname(vitePackage), "dist/node/index.js")).href
);
const root = path.join(checkout, "excalidraw-app");
process.chdir(root);
const loaded = await loadConfigFromFile(
  { command: "build", mode: "production" },
  path.join(root, "vite.config.mts"),
);
if (!loaded) throw new Error("Excalidraw Vite configuration was not found");
// Vite embeds prefixed variables and reads .env files. Allow only the pinned
// environment so both builds share one recorded effective environment.
// Errors list names only; values may be secrets.
const envPrefixes = [loaded.config.envPrefix ?? "VITE_"].flat();
const buildVariable = (name) =>
  name === "NODE_ENV" || envPrefixes.some((prefix) => name.startsWith(prefix));
const unpinned = Object.keys(process.env)
  .filter((name) => buildVariable(name) && !(name in pin.environment))
  .sort();
if (unpinned.length)
  throw new Error(
    `Unset build variables that are not pinned: ${unpinned.join(", ")}`,
  );
const envFiles = execFileSync(
  "git",
  [
    "-C",
    checkout,
    "ls-files",
    "--others",
    "--ignored",
    "--exclude-standard",
    "--directory",
    "-z",
  ],
  { encoding: "utf8" },
)
  .split("\0")
  .filter((file) => /^\.env/.test(path.basename(file)));
if (envFiles.length)
  throw new Error(
    `Remove ignored .env files outside the verified tree: ${envFiles.join(", ")}`,
  );
const environment = Object.fromEntries(
  Object.keys(process.env)
    .filter(buildVariable)
    .sort()
    .map((name) => [name, process.env[name]]),
);
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

// Record which optimizer produced the substituted modules so measurements
// can tell the existing and extended optimizer builds apart.
let optimizer = null;
if (optimized) {
  const report = JSON.parse(
    await readFile(path.join(optimized, "report.json"), "utf8"),
  );
  // Reports do not identify the optimizer's source revision, so record the
  // checkout the typeopt binary was built from. measure.mjs compares it with
  // the pins; a dirty checkout cannot reproduce the binary.
  const optimizerSource = await realpath(optimizerSourceArg);
  const sourceGit = (...args) =>
    execFileSync("git", ["-C", optimizerSource, ...args], {
      encoding: "utf8",
    }).trim();
  optimizer = {
    report_schema_version: report.schema_version,
    tool_version: report.tool_version,
    compiler_revision: report.compiler_revision,
    disabled_passes: report.passes?.disabled ?? null,
    source_revision: sourceGit("rev-parse", "HEAD"),
    source_dirty:
      sourceGit(
        "status",
        "--porcelain",
        "--",
        "crates/typeopt",
        "Cargo.lock",
      ) !== "",
  };
}
await writeFile(
  path.join(destination, "typeopt-build.json"),
  JSON.stringify(
    {
      schema_version: 1,
      revision,
      source_tree: sourceTree,
      yarn_lock_sha256: yarnLockSha256,
      node: process.versions.node,
      vite: viteVersion,
      mode: "production",
      sourcemap: true,
      environment,
      optimized: Boolean(optimized),
      optimizer,
      elapsed_ms: performance.now() - started,
      peak_rss_bytes: process.resourceUsage().maxRSS * 1024,
    },
    null,
    2,
  ),
);
