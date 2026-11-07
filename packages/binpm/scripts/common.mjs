// SPDX-License-Identifier: Apache-2.0
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
// Until the launcher and native packages exist, the only publishable version is
// the reservation placeholder. Remove this pin when the real packages land.
export const placeholderVersion = "0.0.0";

// The unscoped `binpm` name is unavailable: npm rejects it because it is too
// similar to the existing `bin_pm` package. Revisit if npm support grants it.
export const LAUNCHER_NAME = "@delino/binpm";

// One entry per first-party binpm release target in the release workflow
// (`release-binpm.yml`). `libc` is set only for Linux, as for the other
// repository npm distributions.
export const targets = Object.freeze([
  { suffix: "darwin-x64", os: "darwin", cpu: "x64" },
  { suffix: "darwin-arm64", os: "darwin", cpu: "arm64" },
  { suffix: "win32-x64-msvc", os: "win32", cpu: "x64" },
  { suffix: "win32-arm64-msvc", os: "win32", cpu: "arm64" },
  { suffix: "linux-x64-gnu", os: "linux", cpu: "x64", libc: "glibc" },
  { suffix: "linux-arm64-gnu", os: "linux", cpu: "arm64", libc: "glibc" },
]);

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
