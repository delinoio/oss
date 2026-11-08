// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";
import {
  identity,
  verifyRelease,
} from "../../packages/ccow/scripts/package.mjs";

export function homebrewArguments(directory, revision, publish = false) {
  const release = verifyRelease(directory, identity(revision, false));
  const artifact = release.artifacts.find((a) => a.target === "darwin-arm64");
  assert(artifact, "macOS ARM64 archive required");
  return [
    "scripts/release/update-homebrew.sh",
    "--project",
    "ccow",
    "--version",
    release.version,
    "--darwin-arm64-url",
    `https://github.com/delinoio/oss/releases/download/${release.tag}/${artifact.name}`,
    "--darwin-arm64-sha256",
    artifact.sha256,
    ...(publish ? [] : ["--dry-run"]),
  ];
}
if (
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  const [directory, revision, mode = "dry-run"] = process.argv.slice(2);
  assert(["dry-run", "publish"].includes(mode), "Unsupported Homebrew mode");
  execFileSync(
    "bash",
    homebrewArguments(directory, revision, mode === "publish"),
    { stdio: "inherit" },
  );
}
