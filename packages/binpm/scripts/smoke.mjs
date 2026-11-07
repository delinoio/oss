// SPDX-License-Identifier: Apache-2.0
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import platforms from "../src/platforms.cjs";
import { buildPackage, tarballName } from "./package.mjs";
import { ensure, metadata, npm, event } from "./common.mjs";

const { values } = parseArgs({ options: { directory: { type: "string" } } });
ensure(values.directory, "Expected --directory containing this host's tarball");
const target = platforms.selectTarget();
ensure(target, "Unsupported smoke-test host");
const directory = path.resolve(values.directory);
const { version } = metadata();
const temporary = mkdtempSync(path.join(tmpdir(), "binpm npm consumer "));
try {
  buildPackage({ output: temporary });
  writeFileSync(path.join(temporary, "package.json"), '{"private":true}');
  npm(
    [
      "install",
      "--offline",
      "--ignore-scripts",
      "--omit=optional",
      "--no-audit",
      "--no-fund",
      path.join(temporary, "tarballs", tarballName("@delino/binpm", version)),
      path.join(directory, tarballName(target.name, version)),
    ],
    { cwd: temporary },
  );
  // npm exec resolves the installed bin shim on both Windows and Unix.
  const run = (args) =>
    npm(["exec", "--offline", "--", "binpm", ...args], { cwd: temporary });
  ensure(
    run(["--version"]).trim() === `binpm ${version}`,
    "Installed npm version mismatch",
  );
  ensure(run(["--help"]).includes("Usage:"), "Installed npm help failed");
  let failure;
  try {
    run(["--binpm-invalid-smoke-argument"]);
  } catch (error) {
    failure = error;
  }
  ensure(
    failure?.status === 2,
    "Launcher did not preserve the native error exit code",
  );
  event("consumer_verified", { target: target.suffix, version });
} finally {
  rmSync(temporary, { recursive: true, force: true });
}
