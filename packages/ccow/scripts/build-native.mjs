import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync } from "node:fs";
import path from "node:path";
import { parseArgs } from "node:util";
import assert from "node:assert/strict";
import platforms from "../src/platforms.cjs";
import { root, version } from "./package.mjs";
const { values } = parseArgs({
  options: { output: { type: "string" }, target: { type: "string" } },
});
assert(
  values.output,
  "Usage: --output <new-directory> [--target darwin-arm64|linux-x64|windows-x64]",
);
const targets = values.target
  ? platforms.targets.filter((t) => t.suffix === values.target)
  : platforms.targets;
assert(targets.length, "Unsupported target");
// Bind Git discovery to this checkout: a linked worktree can live inside a
// different checkout, whose revision must never be embedded instead.
const gitDirectory = execFileSync("git", ["rev-parse", "--absolute-git-dir"], {
  cwd: root,
  encoding: "utf8",
}).trim();
const git = (...args) =>
  execFileSync("git", args, {
    cwd: root,
    encoding: "utf8",
    env: { ...process.env, GIT_DIR: gitDirectory, GIT_WORK_TREE: root },
  }).trim();
const revision = git("rev-parse", "HEAD");
const dirty = git("status", "--porcelain", "--untracked-files=normal") !== "";
const output = path.resolve(values.output);
mkdirSync(output, { recursive: false });
for (const target of targets) {
  const directory = path.join(output, target.suffix);
  mkdirSync(directory);
  // The crate's build script embeds these values in the static
  // `ccow-build-info:` marker that package.mjs verifies.
  execFileSync(
    "cargo",
    [
      "build",
      "--release",
      "--locked",
      "-p",
      "cachecow",
      "--bin",
      "ccow",
      "--target",
      target.rust,
    ],
    {
      cwd: root,
      env: {
        ...process.env,
        CCOW_VERSION: version,
        CCOW_BUILD_REVISION: revision,
        CCOW_BUILD_DIRTY: String(dirty),
        ...(target.os === "darwin" ? { MACOSX_DEPLOYMENT_TARGET: "15.0" } : {}),
      },
      stdio: "inherit",
    },
  );
  copyFileSync(
    path.join(
      path.resolve(root, process.env.CARGO_TARGET_DIR || "target"),
      target.rust,
      "release",
      target.binary,
    ),
    path.join(directory, target.binary),
  );
}
console.log(
  JSON.stringify({
    event: "ccow_native_build",
    version,
    revision,
    dirty,
    targets: targets.map((t) => t.suffix),
    validation: "compilation only; native acceptance required",
  }),
);
