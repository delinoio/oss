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
class RootInfo:
 st_mode = stat.S_IFDIR
class Info:
 st_mode = stat.S_IFREG
 st_dev = 1
 st_ino = 7
with patch.object(m.os, 'walk', return_value=[('/fixture', [], ['one'])]), patch.object(m.os, 'lstat', side_effect=lambda p: RootInfo() if p in ['a','b'] else Info()), patch.object(m, 'extents', return_value=[[100, 200, True]]):
 same = m.measure(['a','b'])
 assert same['physicalBytes'] == 100 and same['shared'] is False
 other = Info()
 other.st_ino = 8
 with patch.object(m.os, 'lstat', side_effect=[RootInfo(), Info(), RootInfo(), other]):
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

test("cold primary phase bootstraps with run and never prepares a missing source", async () => {
  const { preparePhase } = await import("./ccow-native-driver.mjs");
  const calls = [];
  const run = (cwd, args) => {
    calls.push({ cwd, args });
    return { ms: 12 };
  };
  assert.deepEqual(
    preparePhase(
      "cold",
      "primary",
      "primary",
      "cargo",
      ["cargo", "build"],
      run,
    ),
    { ms: 0 },
  );
  assert.equal(calls.length, 0);
  preparePhase(
    "warm-source",
    "primary",
    "primary",
    "cargo",
    ["cargo", "build"],
    run,
  );
  preparePhase(
    "cloned-worktree",
    "derived",
    "primary",
    "cargo",
    ["cargo", "build"],
    run,
  );
  assert.deepEqual(calls, [
    {
      cwd: "primary",
      args: ["prepare", "--preset", "cargo", "--", "cargo", "build"],
    },
    {
      cwd: "derived",
      args: [
        "prepare",
        "--preset",
        "cargo",
        "--source",
        "primary",
        "--",
        "cargo",
        "build",
      ],
    },
  ]);
});

test("Windows settings normalize before JSON escapes backslashes", async () => {
  const { serializeSettings } = await import("./ccow-native-driver.mjs");
  const source = String.raw`C:\acceptance\source 한글`;
  const derived = String.raw`C:\acceptance\derived 한글`;
  const config = (cwd) => [
    {
      context: cwd,
      output: { path: cwd + String.raw`\out` },
      entry: [cwd + String.raw`\src\index.js`],
      pattern: /test/u,
    },
  ];
  assert.equal(
    serializeSettings(config(source), source),
    serializeSettings(config(derived), derived),
  );
  assert.equal(
    JSON.parse(serializeSettings(config(source), source))[0].context,
    "$WORKTREE",
  );
});

test("frontend command and discovery resolve in the requested source worktree", (t) => {
  const root = temporary(t);
  const source = path.join(root, "source");
  const derived = path.join(root, "derived");
  const a = createFixture(source, "rspack"),
    b = createFixture(derived, "rspack");
  assert.deepEqual(a.argv, b.argv);
  assert.equal(a.argv[1], ".ccow/build.cjs");
  const program = `require(process.argv[1])(JSON.parse(process.argv[2])).then(v=>process.stdout.write(JSON.stringify(v)))`;
  const discover = (cwd) =>
    JSON.parse(
      command(
        process.execPath,
        [
          "-e",
          program,
          path.join(cwd, ".ccow/discover.cjs"),
          JSON.stringify({ preset: "rspack", argv: b.argv, cwd, env: {} }),
        ],
        { cwd },
      ).stdout,
    );
  const first = discover(source),
    second = discover(derived);
  assert.deepEqual(first.settings, second.settings);
  assert.equal(first.caches[0].path, path.join(source, ".cache"));
  assert.equal(second.caches[0].path, path.join(derived, ".cache"));
});

test("native negative scenario rejects unexpected success and preserves diagnostics privately", async () => {
  const { expectFailure } = await import("./ccow-native-scenarios.mjs");
  assert.throws(() =>
    expectFailure(process.execPath, ["-e", "process.exit(0)"], {}, 125),
  );
  const r = expectFailure(
    process.execPath,
    ["-e", "console.error('private-marker');process.exit(125)"],
    {},
    125,
  );
  assert.equal(r.status, 125);
});

test("physical extent probe rejects missing roots and emits only a fixed failure", (t) => {
  const root = temporary(t);
  const helper = new URL("./ccow-extents.py", import.meta.url).pathname;
  assert.throws(() =>
    command("python3", [helper, path.join(root, "missing-private-path")]),
  );
});

test(
  "APFS physical mapping fixture decodes packed offsets and rejects unknown mappings",
  { skip: process.platform === "win32" },
  () => {
    const helper = new URL("./ccow-extents.py", import.meta.url).pathname;
    const program = `
import importlib.util, struct
from unittest.mock import patch, mock_open
spec=importlib.util.spec_from_file_location('ccow_extents',${JSON.stringify(helper)})
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
class FS: f_bsize=4096
class Info: st_size=5000
responses=[struct.pack('=Iqq',0,4096,8192),struct.pack('=Iqq',0,4096,16384)]
with patch('builtins.open',mock_open()),patch.object(m.os,'statvfs',return_value=FS()),patch.object(m.os,'fsync'),patch.object(m.os,'fstat',return_value=Info()),patch.object(m.fcntl,'fcntl',side_effect=responses):
 assert m.darwin_extents('fixture') == [[8192,12288,True],[16384,20480,True]]
with patch('builtins.open',mock_open()),patch.object(m.os,'statvfs',return_value=FS()),patch.object(m.os,'fsync'),patch.object(m.os,'fstat',return_value=Info()),patch.object(m.fcntl,'fcntl',return_value=struct.pack('=Iqq',0,0,-1)):
 try: m.darwin_extents('fixture'); raise AssertionError('accepted unknown mapping')
 except ValueError: pass
print('fixture-pass')
`;
    assert.equal(
      command("python3", ["-B", "-c", program]).stdout.trim(),
      "fixture-pass",
    );
  },
);

// Opt-in development smoke tests use a locally built native executable. They
// exercise actual Cargo and process ownership, but do not produce release-gate
// evidence or substitute for the packaged minimum-OS matrix.
test(
  "native Cargo orchestration and process scenarios",
  { skip: !process.env.CCOW_NATIVE_TEST_BINARY, timeout: 180_000 },
  async (t) => {
    const { mkdirSync } = await import("node:fs");
    const {
      nodeFreeScenario,
      unknownVersionScenario,
      replacementScenario,
      privacyScenario,
      shimScenarios,
      lifecycleScenarios,
      probeSource,
    } = await import("./ccow-native-scenarios.mjs");
    const root = realpathSync(
      mkdtempSync(path.join(os.tmpdir(), "ccow-native-regression-")),
    );
    const home = path.join(root, "home"),
      source = path.join(root, "source"),
      derived = path.join(root, "derived");
    mkdirSync(home, { mode: 0o700 });
    const env = {
      PATH: process.env.PATH,
      HOME: home,
      USERPROFILE: home,
      SystemRoot: process.env.SystemRoot,
      LOCALAPPDATA: path.join(home, "local"),
      APPDATA: path.join(home, "roaming"),
      XDG_CONFIG_HOME: path.join(home, "config"),
      RUSTUP_HOME:
        process.env.RUSTUP_HOME ||
        path.join(process.env.HOME || process.env.USERPROFILE, ".rustup"),
      RUSTUP_TOOLCHAIN: "1.99.1",
      CARGO_HOME: path.join(home, "cargo"),
      NO_COLOR: "1",
      LC_ALL: "C",
    };
    let retain = false;
    const binary = path.resolve(process.env.CCOW_NATIVE_TEST_BINARY);
    try {
      const fixture = createFixture(source, "cargo");
      const git = (args) => command("git", args, { cwd: source, env });
      git(["init"]);
      git(["add", "."]);
      git([
        "-c",
        "user.name=CacheCow acceptance",
        "-c",
        "user.email=acceptance@example.invalid",
        "-c",
        "commit.gpgsign=false",
        "commit",
        "-m",
        "fixture",
      ]);
      git(["worktree", "add", "--detach", derived]);
      const context = {
        binary,
        source,
        derived,
        root,
        cwd: source,
        fixture,
        env,
        command,
        snapshot,
      };
      command(binary, ["run", "--preset", "cargo", "--", ...fixture.argv], {
        cwd: source,
        env,
      });
      command(
        binary,
        [
          "prepare",
          "--preset",
          "cargo",
          "--source",
          source,
          "--",
          ...fixture.argv,
        ],
        { cwd: derived, env },
      );
      nodeFreeScenario(context);
      command(binary, ["run", "--preset", "cargo", "--", ...fixture.argv], {
        cwd: source,
        env,
      });
      unknownVersionScenario(context);
      replacementScenario(context);
      shimScenarios(context);
      privacyScenario(context);
      for (const cwd of [source, derived]) {
        mkdirSync(path.join(cwd, "src/bin"), { recursive: true });
        writeFileSync(path.join(cwd, "src/bin/ccow-probe.rs"), probeSource);
      }
      await lifecycleScenarios({
        ...context,
        attempt: async (id, action) => {
          await action();
          t.diagnostic(
            `${id}: passed (development host, not release acceptance)`,
          );
          return true;
        },
      });
    } catch (error) {
      retain = Boolean(error.processOwnershipUnverified);
      throw error;
    } finally {
      if (!retain) rmSync(root, { recursive: true, force: true });
    }
  },
);

test(
  "native bounded disk-full recovery",
  {
    skip:
      !process.env.CCOW_NATIVE_FAILURE_TESTS ||
      !process.env.CCOW_NATIVE_TEST_BINARY,
    timeout: 180_000,
  },
  async () => {
    const { mkdirSync } = await import("node:fs");
    const { diskFullScenario } = await import("./ccow-native-scenarios.mjs");
    const root = realpathSync(
      mkdtempSync(path.join(os.tmpdir(), "ccow-native-exhaustion-")),
    );
    const home = path.join(root, "home"),
      source = path.join(root, "source");
    mkdirSync(home, { mode: 0o700 });
    const env = {
      PATH: process.env.PATH,
      HOME: home,
      XDG_CONFIG_HOME: path.join(home, "config"),
      RUSTUP_HOME:
        process.env.RUSTUP_HOME || path.join(process.env.HOME, ".rustup"),
      RUSTUP_TOOLCHAIN: "1.99.1",
      CARGO_HOME: path.join(home, "cargo"),
      NO_COLOR: "1",
      LC_ALL: "C",
    };
    let retain = false;
    try {
      const fixture = createFixture(source, "cargo");
      for (const args of [
        ["init"],
        ["add", "."],
        [
          "-c",
          "user.name=CacheCow acceptance",
          "-c",
          "user.email=acceptance@example.invalid",
          "-c",
          "commit.gpgsign=false",
          "commit",
          "-m",
          "fixture",
        ],
      ])
        command("git", args, { cwd: source, env });
      diskFullScenario({
        binary: path.resolve(process.env.CCOW_NATIVE_TEST_BINARY),
        source,
        fixture,
        root,
        env,
        command,
        snapshot,
      });
    } catch (error) {
      retain = Boolean(error.processOwnershipUnverified);
      throw error;
    } finally {
      if (!retain) rmSync(root, { recursive: true, force: true });
    }
  },
);
