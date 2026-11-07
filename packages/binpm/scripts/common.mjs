// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import platforms from "../src/platforms.cjs";
import { execFileSync } from "node:child_process";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const packageRoot = fileURLToPath(new URL("..", import.meta.url));
export const root = path.resolve(packageRoot, "../..");
export const registry = "https://registry.npmjs.org";
export const repository = "git+https://github.com/delinoio/oss.git";
export const homepage = "https://oss.delino.io/binpm";
// npm trusted publishing binds a package to one repository and workflow file.
export const trustedRepository = "delinoio/oss";
export const trustedWorkflow = "release-binpm-npm.yml";
// The unscoped `binpm` name is unavailable: npm rejects it because it is too
// similar to the existing `bin_pm` package. Revisit if npm support grants it.
export const LAUNCHER_NAME = "@delino/binpm";

export const { targets } = platforms;

export const nativeName = (target) => `@delino/binpm-${target.suffix}`;

export function ensure(condition, message) {
  if (!condition) throw new Error(message);
}

export function npm(args, options = {}) {
  // npm.cmd cannot be spawned without a shell on Windows. Use npm's Node entry
  // point in the standard Node installation, keeping all dynamic argv literal.
  const command = process.platform === "win32" ? process.execPath : "npm";
  const prefix =
    process.platform === "win32"
      ? [
          path.join(
            path.dirname(process.execPath),
            "node_modules/npm/bin/npm-cli.js",
          ),
        ]
      : [];
  return execFileSync(command, [...prefix, ...args], {
    encoding: "utf8",
    stdio: "pipe",
    ...options,
  });
}

export function event(action, details) {
  console.log(JSON.stringify({ event: `binpm_npm_${action}`, ...details }));
}

export function isMain(url) {
  return (
    process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(url)
  );
}

// Read canonical LF bytes even on a Windows checkout with core.autocrlf.
export function sourceText(file) {
  return readFileSync(path.join(root, file), "utf8").replaceAll("\r\n", "\n");
}

// Derive every generated npm version directly from Cargo. The private tooling
// workspace version is not a product version and needs no coordinator bump.
export function metadata(read = sourceText) {
  const cargo = read("crates/binpm/Cargo.toml").match(
    /^\[package\]\s*\n([\s\S]*?)(?=^\[|$(?![\s\S]))/mu,
  )?.[1];
  const version = cargo?.match(/^version = "([^"]+)"$/mu)?.[1];
  ensure(
    cargo?.includes('name = "binpm"\n') &&
      /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u.test(version ?? "") &&
      version !== "0.0.0",
    "Expected a non-placeholder binpm source version",
  );
  return {
    version,
    description: "Node-free binary package manager for release assets",
  };
}

export function revision() {
  return execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: root,
    encoding: "utf8",
  }).trim();
}

export function releaseIdentity(
  env = process.env,
  version = metadata().version,
  commit = revision(),
) {
  ensure(
    env.GITHUB_REPOSITORY === trustedRepository && env.GITHUB_SHA === commit,
    "Release requires the first-party repository and exact source commit",
  );
  ensure(
    ["push", "workflow_dispatch"].includes(env.GITHUB_EVENT_NAME),
    "Unsupported release event",
  );
  const tag = `refs/tags/binpm@v${version}`;
  const dryRun =
    env.GITHUB_EVENT_NAME === "push"
      ? false
      : env.REQUESTED_DRY_RUN !== "false";
  if (env.GITHUB_EVENT_NAME === "push" || !dryRun)
    ensure(
      env.GITHUB_REF === tag,
      "Publication requires the exact binpm version tag",
    );
  return { version, revision: commit, dry_run: String(dryRun) };
}
