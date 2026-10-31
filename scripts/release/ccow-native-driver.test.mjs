// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import {
  mkdtempSync,
  readFileSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import os from "node:os";
import {
  blockers,
  command,
  createFixture,
  execute,
  parsePeakMemory,
  snapshot,
} from "./ccow-native-driver.mjs";
import {
  checks,
  projectProgress,
  validateMeasurements,
} from "./ccow-acceptance.mjs";

// These exercise fixture generation and orchestration only. They are not native
// acceptance records and must never be supplied to the release gate.
function temporary(t) {
  const root = realpathSync(
    mkdtempSync(path.join(os.tmpdir(), "ccow-driver-fixture-")),
  );
  t.after(() => rmSync(root, { recursive: true, force: true }));
  return root;
}
test("memory units come from native resource reports and absent data fails", () => {
  assert.equal(
    parsePeakMemory("Maximum resident set size (kbytes): 123", "linux"),
    125952,
  );
  assert.equal(
    parsePeakMemory(" 456 maximum resident set size\n", "darwin"),
    456,
  );
  for (const text of ["", "Maximum resident set size (kbytes): 0", "100 bytes"])
    assert.throws(() => parsePeakMemory(text, "linux"));
});
test("fixture commands select real tool entrypoints and persistent caches", (t) => {
  const root = temporary(t);
  for (const preset of ["cargo", "rspack", "rsbuild"]) {
    const dir = path.join(root, preset);
    const fixture = createFixture(dir, preset);
    assert(!fixture.argv.includes("--allow-copy"));
    if (preset === "cargo") {
      assert.deepEqual(fixture.argv, ["cargo", "build", "--offline"]);
      assert.match(
        readFileSync(path.join(dir, "src/main.rs"), "utf8"),
        /ccow-original/u,
      );
    } else {
      assert.equal(
        JSON.parse(readFileSync(path.join(dir, "package.json"))).dependencies[
          `@${preset}/core`
        ],
        "2.3.0",
      );
      assert.match(
        readFileSync(path.join(dir, `${preset}.config.cjs`), "utf8"),
        /persistent/u,
      );
      assert.equal(fixture.cache, ".cache");
    }
  }
});
test("snapshots detect mutation and reject external symlinks", (t) => {
  const root = temporary(t);
  writeFileSync(path.join(root, "file"), "original");
  const before = snapshot(root);
  writeFileSync(path.join(root, "file"), "modified");
  assert.notDeepEqual(snapshot(root), before);
  if (process.platform !== "win32") {
    symlinkSync(os.tmpdir(), path.join(root, "external"));
    assert.throws(() => snapshot(root));
  }
});
test("command failures omit child diagnostics and preserve literal argv", () => {
  const marker = "secret fixture value";
  assert.equal(
    command(process.execPath, [
      "-e",
      "process.stdout.write(process.argv[1])",
      marker,
    ]).stdout,
    marker,
  );
  assert.throws(
    () =>
      command(process.execPath, [
        "-e",
        `console.error(${JSON.stringify(marker)});process.exit(9)`,
      ]),
    (error) => !String(error).includes(marker),
  );
});
test("missing native prerequisites return blocked typed evidence, never acceptance", async (t) => {
  const root = temporary(t);
  const result = await execute({
    lane: "invalid-fixture-lane",
    artifacts: root,
    workspace: root,
    revision: "a".repeat(40),
  });
  assert.deepEqual(result.unperformed, checks);
  assert.deepEqual(result.measurements, []);
  assert.throws(() => validateMeasurements(result));
  assert(result.blockers.some((item) => item.id === "host-and-artifacts"));
  assert.deepEqual(
    Object.keys(blockers).sort(),
    [
      "physical-sharing",
      "freshness",
      "locking",
      "native-install-no-node",
      "privacy",
      "process-tree",
      "reflink-required",
      "replacement-recovery",
      "rollback",
      "shell-activation",
      "shims",
    ].sort(),
  );
  assert.deepEqual(readFileSyncNames(root), []);
});
function readFileSyncNames(root) {
  // Reuse snapshot so a leaked disposable file is visible to this assertion.
  return Object.keys(snapshot(root));
}
test("blocked evidence projects only fixed checks and source-owned reasons", () => {
  const result = projectProgress(
    {
      checks: { "npm-install": true, "secret-value": true },
      blockers: [{ id: "privacy", reason: "secret-value" }],
      stdout: "secret-value",
    },
    blockers,
  );
  assert.deepEqual(result.completedChecks, ["npm-install"]);
  assert(!result.unperformed.includes("npm-install"));
  assert(!JSON.stringify(result).includes("secret-value"));
  assert(
    result.blockers.some(
      (item) => item.id === "rollback" && item.reason === blockers.rollback,
    ),
  );
});

test("frontend discovery shares configuration without writing or logging", async (t) => {
  const root = temporary(t);
  const fixture = createFixture(root, "rspack");
  const before = snapshot(root);
  const program = `const discover = require(process.argv[1]); discover(JSON.parse(process.argv[2])).then(value => process.stdout.write(JSON.stringify(value))).catch(() => {process.exitCode=1});`;
  const input = { preset: "rspack", argv: fixture.argv, cwd: root, env: {} };
  const result = JSON.parse(
    command(process.execPath, [
      "-e",
      program,
      path.join(root, ".ccow/discover.cjs"),
      JSON.stringify(input),
    ]).stdout,
  );
  assert.equal(result.schema, 1);
  assert.deepEqual(result.caches, [
    { type: "persistent", path: path.join(root, ".cache") },
  ]);
  assert.deepEqual(result.outputs, [path.join(root, "out")]);
  assert(!result.settings.resolved.includes(root));
  assert.deepEqual(snapshot(root), before);
  assert.throws(() =>
    command(process.execPath, [
      "-e",
      program,
      path.join(root, ".ccow/discover.cjs"),
      JSON.stringify({ ...input, argv: [...fixture.argv, "--unknown"] }),
    ]),
  );
});

test(
  "FIEMAP accounting uses extent union and rejects hardlinks as sharing",
  { skip: process.platform === "win32" },
  () => {
    const helper = new URL("./ccow-extents.py", import.meta.url).pathname;
    const program = `
import importlib.util, json, stat
from unittest.mock import patch
spec = importlib.util.spec_from_file_location('ccow_extents', ${JSON.stringify(helper)})
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
class Info:
 st_mode = stat.S_IFREG
 st_dev = 1
 st_ino = 7
with patch.object(m.os, 'walk', return_value=[('/fixture', [], ['one'])]), patch.object(m.os, 'lstat', return_value=Info()), patch.object(m, 'extents', return_value=[[100, 200, True]]):
 same = m.measure(['a','b'])
 assert same['physicalBytes'] == 100 and same['shared'] is False
 other = Info()
 other.st_ino = 8
 with patch.object(m.os, 'lstat', side_effect=[Info(), other]):
  separate = m.measure(['a','b'])
  assert separate['physicalBytes'] == 100 and separate['shared'] is True
print('fixture-pass')
`;
    assert.equal(
      command("python3", ["-B", "-c", program]).stdout.trim(),
      "fixture-pass",
    );
  },
);
