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
const output = path.resolve(values.output);
mkdirSync(output, { recursive: false });
for (const target of targets) {
  const directory = path.join(output, target.suffix);
  mkdirSync(directory);
  execFileSync(
    "go",
    [
      "build",
      "-trimpath",
      "-buildvcs=true",
      "-ldflags",
      `-X main.version=${version}`,
      "-o",
      path.join(directory, target.binary),
      "./cmds/cowcache",
    ],
    {
      cwd: root,
      env: {
        ...process.env,
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
    event: "cowc_cross_build",
    version,
    targets: targets.map((t) => t.suffix),
    validation: "compilation only; native acceptance required",
  }),
);
