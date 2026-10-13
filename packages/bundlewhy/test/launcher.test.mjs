import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const launcher = path.join(root, "bin/bundlewhy.cjs");
const { PLATFORM_PACKAGES, resolveBinary } = require(launcher);

test("supports exactly the three release platforms", () => {
  assert.deepEqual(Object.keys(PLATFORM_PACKAGES).sort(), [
    "darwin-arm64",
    "linux-x64",
    "win32-x64",
  ]);
});

test("rejects unsupported platforms and musl with a structured error", () => {
  assert.equal(
    resolveBinary({}, "linux", "arm64", false).error.code,
    "BW_PLATFORM_UNSUPPORTED",
  );
  assert.equal(
    resolveBinary({}, "linux", "x64", true).error.code,
    "BW_PLATFORM_UNSUPPORTED",
  );
  assert.equal(
    resolveBinary({}, "darwin", "arm64", false).error.code,
    "BW_PLATFORM_PACKAGE_MISSING",
  );
});

test("forwards arguments and exit codes to the binary without a shell", () => {
  const result = spawnSync(process.execPath, [launcher, "a b", "--flag"], {
    env: { ...process.env, BUNDLEWHY_BINARY: process.execPath },
    input: "",
    encoding: "utf8",
  });
  // node treats "a b" as a script path that does not exist and exits 1.
  assert.equal(result.status, 1);
  assert.match(result.stderr, /a b/);
});
