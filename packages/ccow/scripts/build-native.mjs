import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
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
// different checkout, and Go's enclosing-VCS lookup can otherwise stamp that
// parent's revision. Keep linker flags in build metadata for package verification
// (-trimpath omits those flags).
const gitDirectory = execFileSync("git", ["rev-parse", "--absolute-git-dir"], {
  cwd: root,
  encoding: "utf8",
}).trim();
const output = path.resolve(values.output);
mkdirSync(output, { recursive: false });
for (const target of targets) {
  const directory = path.join(output, target.suffix);
  mkdirSync(directory);
  execFileSync(
    "go",
    [
      "build",
      "-buildvcs=true",
      "-ldflags",
      `-X main.version=${version}`,
      "-o",
      path.join(directory, target.binary),
      "./cmds/cachecow",
    ],
    {
      cwd: root,
      env: {
        ...process.env,
        GIT_DIR: gitDirectory,
        GIT_WORK_TREE: root,
        CGO_ENABLED: "0",
        GOOS: target.goos,
        GOARCH: target.goarch,
      },
      stdio: "inherit",
    },
  );
}
console.log(
  JSON.stringify({
    event: "ccow_cross_build",
    version,
    targets: targets.map((t) => t.suffix),
    validation: "compilation only; native acceptance required",
  }),
);
