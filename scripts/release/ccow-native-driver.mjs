// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import {
  chmodSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  realpathSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { performance } from "node:perf_hooks";
import { entries } from "../../packages/ccow/scripts/archive.mjs";
import {
  identity,
  verifyRelease,
  version,
} from "../../packages/ccow/scripts/package.mjs";
import platforms from "../../packages/ccow/src/platforms.cjs";
import {
  lifecycleScenarios,
  nodeFreeScenario,
  privacyScenario,
  probeSource,
  stateDirectory,
  expectFailure,
  unknownVersionScenario,
  replacementScenario,
  shellScenarios,
  shimScenarios,
  crossVolumeScenario,
} from "./ccow-native-scenarios.mjs";
import { checks, phases, probeHost } from "./ccow-acceptance.mjs";

// CLI integration dependencies: documented run/prepare/clean --preset, --source,
// --replace, --json, shim and shell commands; normal OS home/config selectors.
// Frontend discovery uses the version-1 .ccow/discover.cjs protocol.
// No private runtime state schema or test-only success switches are assumed.
// Remaining acceptance assertions are listed by fixed IDs in blockers below.
export const blockers = Object.freeze({
  "physical-sharing": "native-physical-extent-mapping-required",
  freshness:
    "tracked-and-untracked-changes-must-match-independent-clean-builds",
  rollback:
    "previous-release-artifacts-and-unknown-record-version-scenario-required",
  "reflink-required": "unsupported-volume-and-cross-volume-fixtures-required",
  "replacement-recovery":
    "disk-full-permission-and-partial-copy-injection-required",
  locking: "same-cache-cross-cache-nested-and-clean-wait-scenarios-required",
  "process-tree":
    "watch-cancellation-five-second-escalation-and-orphan-writer-scenarios-required",
  shims:
    "managed-build-dynamic-path-conflict-uninstall-and-nesting-scenarios-required",
  "shell-activation":
    "interactive-confirmation-new-shell-and-failed-write-scenarios-required",
  privacy: "persisted-state-acl-and-sensitive-log-audit-required",
  "native-install-no-node":
    "node-free-toolchain-path-and-native-cargo-build-required",
});
const hash = (bytes) => createHash("sha256").update(bytes).digest("hex");
export function snapshot(root) {
  const result = {};
  if (!existsSync(root)) return result;
  function visit(dir) {
    for (const name of readdirSync(dir).sort()) {
      const file = path.join(dir, name);
      const stat = lstatSync(file);
      assert(
        !stat.isSymbolicLink(),
        "Cache links need a separate boundary scenario",
      );
      if (stat.isDirectory()) visit(file);
      else {
        assert(stat.isFile());
        result[path.relative(root, file)] = hash(readFileSync(file));
      }
    }
  }
  visit(root);
  return result;
}
export function parsePeakMemory(text, platform) {
  const match =
    platform === "linux"
      ? /Maximum resident set size \(kbytes\):\s*(\d+)/u.exec(text)
      : /^\s*(\d+)\s+maximum resident set size\s*$/mu.exec(text);
  assert(match, "Native time did not report peak RSS");
  const bytes = Number(match[1]) * (platform === "linux" ? 1024 : 1);
  assert(Number.isSafeInteger(bytes) && bytes > 0);
  return bytes;
}
export function command(command, args, options = {}) {
  const begin = performance.now();
  const result = spawnSync(command, args, {
    encoding: "utf8",
    timeout: 180_000,
    maxBuffer: 16 * 1024 * 1024,
    stdio: ["ignore", "pipe", "pipe"],
    ...options,
  });
  // Do not expose command arguments, stdout, stderr, paths or environment in errors.
  if (result.error || result.signal || result.status !== 0) {
    const error = new Error("Native command failed");
    error.processOwnershipUnverified = Boolean(
      result.signal || (result.error && result.error.code !== "ENOENT"),
    );
    throw error;
  }
  return {
    stdout: result.stdout,
    stderr: result.stderr,
    ms: performance.now() - begin,
  };
}
function npm(args, options) {
  // Execute the npm JavaScript entry directly, including on Windows where a
  // .cmd file is not a CreateProcess executable. Never interpolate shell code.
  const candidates = [
    path.join(
      path.dirname(process.execPath),
      "node_modules/npm/bin/npm-cli.js",
    ),
  ];
  for (const dir of (process.env.PATH || "").split(path.delimiter)) {
    if (!dir) continue;
    const file = path.join(dir, "npm");
    if (existsSync(file)) candidates.push(realpathSync(file));
  }
  const entry = candidates.find(
    (file) => existsSync(file) && file.endsWith("npm-cli.js"),
  );
  assert(entry, "npm JavaScript entry point unavailable");
  return command(process.execPath, [entry, ...args], options);
}
function write(root, name, value) {
  const file = path.join(root, name);
  mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  writeFileSync(file, value, { mode: 0o600 });
}
export function serializeSettings(configs, cwd) {
  return JSON.stringify(configs, (_, value) => {
    if (typeof value === "function" || value instanceof RegExp)
      value = value.toString();
    return typeof value === "string"
      ? value.split(cwd).join("$WORKTREE")
      : value;
  });
}
export function preparePhase(phase, cwd, source, preset, argv, run) {
  // Zero means no separate operation; cold preparation is included in run.
  if (phase === "cold") return { ms: 0 };
  return run(cwd, [
    "prepare",
    "--preset",
    preset,
    ...(cwd === source ? [] : ["--source", source]),
    "--",
    ...argv,
  ]);
}
export function createFixture(root, preset) {
  write(root, ".gitignore", "target/\nnode_modules/\n.cache/\nout/\n");
  if (preset === "cargo") {
    write(
      root,
      "Cargo.toml",
      '[package]\nname = "ccow-acceptance"\nversion = "0.0.0"\nedition = "2021"\n',
    );
    write(
      root,
      ".cargo/config.toml",
      '[build]\ntarget-dir = "target"\nbuild-dir = "target"\n',
    );
    write(root, "src/main.rs", 'fn main() { println!("ccow-original"); }\n');
    return {
      cache: "target",
      output: path.join(
        "target",
        "debug",
        process.platform === "win32"
          ? "ccow-acceptance.exe"
          : "ccow-acceptance",
      ),
      argv: ["cargo", "build", "--offline"],
    };
  }
  assert(["rspack", "rsbuild"].includes(preset));
  const dependencies =
    preset === "rspack"
      ? { "@rspack/core": "2.3.0", "@rspack/cli": "2.3.0" }
      : { "@rsbuild/core": "2.3.0" };
  write(root, "package.json", JSON.stringify({ private: true, dependencies }));
  write(root, "src/index.js", 'console.log("ccow-original");\n');
  const cache =
    "{ type: 'persistent', storage: { type: 'filesystem', directory: require('node:path').resolve(__dirname, '.cache') } }";
  write(
    root,
    `${preset}.config.cjs`,
    preset === "rspack"
      ? `module.exports = { mode: 'development', entry: './src/index.js', cache: ${cache}, output: { path: require('node:path').resolve(__dirname, 'out'), filename: 'main.js' } };\n`
      : `module.exports = { source: { entry: { index: './src/index.js' } }, output: { distPath: { root: 'out' }, minify: false }, tools: { rspack: { cache: ${cache} } } };\n`,
  );
  // Both the build and discovery use this resolver. Rsbuild resolves its real
  // compiler configuration without building; Rspack uses the explicit fixture
  // configuration unchanged. Unknown invocations fail rather than guess flags.
  write(
    root,
    ".ccow/resolve.cjs",
    `
const path = require('node:path');
exports.resolve = async () => {
  const config = require('../${preset}.config.cjs');
  ${preset === "rspack" ? "return { configs: [config] };" : "const {createRsbuild} = require('@rsbuild/core'); const instance = await createRsbuild({cwd: path.resolve(__dirname, '..'), rsbuildConfig: config}); return {instance, configs: await instance.initConfigs({action:'build'})};"}
};
`,
  );
  write(
    root,
    ".ccow/build.cjs",
    `
(async () => {
 const {instance, configs} = await require('./resolve.cjs').resolve();
 ${preset === "rsbuild" ? "await instance.build();" : "const compiler = require('@rspack/core').rspack(configs[0]); await new Promise((resolve,reject) => compiler.run((error,stats) => compiler.close((closeError) => error || closeError || stats.hasErrors() ? reject(error || closeError || new Error('build failed')) : resolve())));"}
})().catch(() => {process.exitCode=1});
`,
  );
  write(
    root,
    ".ccow/discover.cjs",
    `
const path = require('node:path');
module.exports = async ({preset, argv, cwd}) => {
 if (preset !== '${preset}' || argv.length !== 2 || path.resolve(cwd, argv[1]) !== path.join(cwd, '.ccow/build.cjs')) throw new Error('unsupported fixture invocation');
 const {configs} = await require('./resolve.cjs').resolve();
 const caches = configs.map(config => {
   const cache = config.cache;
   if (cache?.type !== 'persistent' || cache.storage?.type !== 'filesystem' || !path.isAbsolute(cache.storage.directory)) throw new Error('unverified cache');
   return {type:'persistent', path:cache.storage.directory};
 });
 const outputs = configs.map(config => {if (!path.isAbsolute(config.output.path)) throw new Error('unverified output'); return config.output.path;});
 const serialized = (${serializeSettings.toString()})(configs, cwd);
 return {schema:1, preset, caches, outputs, settings:{resolved: serialized}};
};
`,
  );
  return {
    cache: ".cache",
    output: "out",
    argv: [process.execPath, ".ccow/build.cjs"],
  };
}

// Every check starts false. Subchecks can pass without satisfying their full
// requirement; only complete groups promote checks. This keeps release blocked
// while CLI integration and native failure/process scenarios remain unfinished.
export async function execute({ lane, artifacts, workspace, revision }) {
  const result = {
    kind: "native",
    checks: Object.fromEntries(checks.map((id) => [id, false])),
    tools: {},
    measurements: [],
    unperformed: [],
    blockers: [],
    scenarios: [],
    measurementMethods: {
      physicalBytes: `${process.platform === "linux" ? "linux-fiemap" : process.platform === "darwin" ? "darwin-log2phys" : "windows-retrieval-pointers"}-data-extents-excluding-metadata`,
      peakMemoryBytes:
        "native-time-maximum-rss-not-summed-concurrent-process-rss",
    },
  };
  const block = (id, reason) => {
    if (
      !result.blockers.some((item) => item.id === id && item.reason === reason)
    )
      result.blockers.push({ id, reason });
  };
  let retainWorkspace = false;
  const attemptAsync = async (id, action) => {
    try {
      await action();
      result.scenarios.push({ id, status: "passed" });
      return true;
    } catch (error) {
      retainWorkspace ||= Boolean(error.processOwnershipUnverified);
      result.scenarios.push({ id, status: "blocked" });
      block(id, "scenario-assertion-or-prerequisite-failed");
      return false;
    }
  };
  const attempt = (id, action) => {
    try {
      action();
      result.scenarios.push({ id, status: "passed" });
      return true;
    } catch (error) {
      retainWorkspace ||= Boolean(error.processOwnershipUnverified);
      result.scenarios.push({ id, status: "blocked" });
      block(id, "scenario-assertion-or-prerequisite-failed");
      return false;
    }
  };
  for (const [id, reason] of Object.entries(blockers)) block(id, reason);
  let root;
  try {
    if (
      !attempt("host-and-artifacts", () => {
        probeHost(workspace, lane);
        verifyRelease(artifacts, identity(revision, false));
      })
    )
      return finish();
    root = realpathSync(mkdtempSync(path.join(workspace, ".ccow-native-")));
    chmodSync(root, 0o700);
    const home = path.join(root, "home");
    mkdirSync(home, { mode: 0o700 });
    // Allowlist toolchain selectors; do not forward arbitrary CI credentials,
    // compiler wrappers, Cargo target overrides or user Git/npm configuration.
    const env = {
      PATH: process.env.PATH,
      SystemRoot: process.env.SystemRoot,
      HOME: home,
      USERPROFILE: home,
      APPDATA: path.join(home, "roaming"),
      LOCALAPPDATA: path.join(home, "local"),
      XDG_CONFIG_HOME: path.join(home, "config"),
      XDG_CACHE_HOME: path.join(home, "cache"),
      XDG_STATE_HOME: path.join(home, "state"),
      CARGO_HOME: path.join(home, "cargo"),
      RUSTUP_TOOLCHAIN: "1.99.1",
      RUSTUP_HOME:
        process.env.RUSTUP_HOME ||
        path.join(process.env.HOME || process.env.USERPROFILE, ".rustup"),
      TMPDIR: root,
      TMP: root,
      TEMP: root,
      LC_ALL: "C",
      NO_COLOR: "1",
      NODE_ENV: "production",
      GIT_CONFIG_NOSYSTEM: "1",
      GIT_CONFIG_GLOBAL: path.join(home, "gitconfig"),
      npm_config_cache: path.join(home, "npm-cache"),
      npm_config_userconfig: path.join(home, "npmrc"),
    };
    const target = platforms.selectTarget();
    const install = path.join(root, "install");
    const binary = path.join(install, target.binary);
    if (
      !attempt("native-archive-install", () => {
        for (const [name, entry] of entries(
          readFileSync(
            path.join(artifacts, `ccow-${version}-${target.suffix}.tar.gz`),
          ),
        )) {
          write(install, name, entry.bytes);
          chmodSync(path.join(install, name), entry.mode);
        }
        assert(
          command(binary, ["--version"], { cwd: root, env }).stdout.includes(
            version,
          ),
        );
      })
    )
      return finish();
    const run = (cwd, args) => command(binary, args, { cwd, env });
    result.checks["npm-install"] = attempt(
      "npm-local-candidate-install",
      () => {
        const dir = path.join(root, "npm");
        write(dir, "package.json", '{"private":true}');
        npm(
          [
            "install",
            "--offline",
            "--ignore-scripts",
            "--no-audit",
            "--no-fund",
            path.resolve(
              artifacts,
              `delino-ccow-${target.suffix}-${version}.tgz`,
            ),
            path.resolve(artifacts, `delino-ccow-${version}.tgz`),
          ],
          { cwd: dir, env },
        );
        assert.equal(
          command(
            process.execPath,
            [
              path.join(dir, "node_modules/@delino/ccow/bin/ccow.cjs"),
              "--version",
            ],
            { cwd: dir, env },
          ).stdout,
          run(root, ["--version"]).stdout,
        );
      },
    );
    // A previous release is a concrete prerequisite, never a fabricated older
    // version linker flag. Only verified archive bytes from artifacts/previous
    // are used; absence leaves rollback unperformed.
    result.checks.rollback = attempt("native-reinstall-rollback", () => {
      const previous = path.join(artifacts, "previous");
      const manifest = JSON.parse(
        readFileSync(path.join(previous, "release.json")),
      );
      assert.equal(manifest.fixture, false);
      assert.match(manifest.sourceRevision, /^[0-9a-f]{40}$/u);
      assert.match(
        manifest.version,
        /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/u,
      );
      const oldParts = manifest.version.split(".").map(Number),
        newParts = version.split(".").map(Number);
      const first = oldParts.findIndex((value, i) => value !== newParts[i]);
      assert(first >= 0 && oldParts[first] < newParts[first]);
      const name = `ccow-${manifest.version}-${target.suffix}.tar.gz`;
      const artifact = manifest.artifacts.find(
        (item) =>
          item.name === name &&
          item.target === target.suffix &&
          item.kind === "native",
      );
      assert(artifact);
      const bytes = readFileSync(path.join(previous, name));
      assert.equal(hash(bytes), artifact.sha256);
      const files = entries(bytes),
        metadata = JSON.parse(files.get("release.json").bytes);
      assert.equal(metadata.fixture, false);
      assert.equal(metadata.version, manifest.version);
      assert.equal(metadata.sourceRevision, manifest.sourceRevision);
      assert.equal(metadata.target, target.suffix);
      assert.equal(hash(files.get(target.binary).bytes), metadata.sha256);
      const candidate = readFileSync(binary);
      const fixtureRoot = path.join(root, "rollback");
      const fixture = createFixture(fixtureRoot, "cargo");
      command("git", ["init"], { cwd: fixtureRoot, env });
      const build = () =>
        command(binary, ["run", "--preset", "cargo", "--", ...fixture.argv], {
          cwd: fixtureRoot,
          env,
        });
      build();
      try {
        writeFileSync(binary, files.get(target.binary).bytes);
        chmodSync(binary, 0o700);
        assert.equal(
          command(binary, ["--version"], { cwd: root, env }).stdout.trim(),
          `ccow ${manifest.version}`,
        );
        build();
        unknownVersionScenario({
          binary,
          cwd: fixtureRoot,
          env,
          command,
          snapshot,
        });
        const dir = path.join(root, "npm");
        const names = [
          `delino-ccow-${target.suffix}-${manifest.version}.tgz`,
          `delino-ccow-${manifest.version}.tgz`,
        ];
        for (const name of names) {
          const entry = manifest.artifacts.find(
            (item) => item.name === name && item.kind === "npm",
          );
          assert(entry);
          assert.equal(
            hash(readFileSync(path.join(previous, name))),
            entry.sha256,
          );
        }
        npm(
          [
            "install",
            "--offline",
            "--ignore-scripts",
            "--no-audit",
            "--no-fund",
            ...names.map((name) => path.resolve(previous, name)),
          ],
          { cwd: dir, env },
        );
        const launcher = path.join(
          dir,
          "node_modules/@delino/ccow/bin/ccow.cjs",
        );
        assert.equal(
          command(process.execPath, [launcher, "--version"], {
            cwd: fixtureRoot,
            env,
          }).stdout.trim(),
          `ccow ${manifest.version}`,
        );
        command(
          process.execPath,
          [launcher, "run", "--preset", "cargo", "--", ...fixture.argv],
          { cwd: fixtureRoot, env },
        );
      } finally {
        writeFileSync(binary, candidate);
        chmodSync(binary, 0o700);
      }
      build();
    });
    const baseEnv = env;
    for (const preset of ["cargo", "rspack", "rsbuild"]) {
      await attemptAsync(`${preset}-build-matrix`, async () => {
        const env = { ...baseEnv };
        const run = (cwd, args) => command(binary, args, { cwd, env });
        const source = path.join(root, `${preset} source 한글`);
        const derived = path.join(root, `${preset} derived 한글`);
        const fixture = createFixture(source, preset);
        const git = (args) => command("git", args, { cwd: source, env });
        git(["init"]);
        if (preset !== "cargo")
          npm(["install", "--ignore-scripts", "--no-audit", "--no-fund"], {
            cwd: source,
            env,
          });
        if (preset !== "cargo") {
          // A neutral PATH entry exposes the installed CLI without embedding one
          // worktree's path in compatibility settings for the other worktree.
          const tools = path.join(root, `${preset}-tools`);
          symlinkSync(
            path.join(source, "node_modules/.bin"),
            tools,
            process.platform === "win32" ? "junction" : "dir",
          );
          env.PATH = tools + path.delimiter + env.PATH;
        }
        const toolVersion =
          preset === "cargo"
            ? /^cargo (\S+)/u.exec(
                command("cargo", ["--version"], { cwd: source, env }).stdout,
              )?.[1]
            : JSON.parse(
                readFileSync(
                  path.join(
                    source,
                    `node_modules/@${preset}/core/package.json`,
                  ),
                ),
              ).version;
        assert.equal(toolVersion, preset === "cargo" ? "1.99.1" : "2.3.0");
        if (preset === "cargo") {
          assert.match(
            command("rustc", ["--version"], { cwd: source, env }).stdout,
            /^rustc 1\.99\.1 /u,
          );
          const metadata = JSON.parse(
            command(
              "cargo",
              ["metadata", "--format-version", "1", "--no-deps", "--offline"],
              { cwd: source, env },
            ).stdout,
          );
          assert.equal(metadata.target_directory, path.join(source, "target"));
          assert.equal(metadata.build_directory, metadata.target_directory);
        }
        git(["check-ignore", path.join(source, fixture.cache, "probe")]);
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
        if (preset !== "cargo")
          npm(["ci", "--ignore-scripts", "--no-audit", "--no-fund"], {
            cwd: derived,
            env,
          });
        let original;
        let sharing = false;
        for (const phase of phases) {
          const cwd = ["cold", "warm-source"].includes(phase)
            ? source
            : derived;
          if (phase === "changed-worktree") {
            write(
              cwd,
              preset === "cargo" ? "src/main.rs" : "src/index.js",
              preset === "cargo"
                ? 'fn main() { println!("ccow-changed"); }\n'
                : 'console.log("ccow-changed");\n',
            );
          }
          const argv = fixture.argv.map((arg) => arg.replace(source, cwd));
          // A missing primary cache is bootstrapped only by run. There is no
          // separate preparation operation in the cold phase.
          const preparation = preparePhase(
            phase,
            cwd,
            source,
            preset,
            argv,
            run,
          );
          if (phase === "cloned-worktree") {
            assert.deepEqual(
              snapshot(path.join(derived, fixture.cache)),
              original,
            );
            attempt(`${preset}-shared-extents`, () => {
              const allocation = JSON.parse(
                command(
                  "python3",
                  [
                    fileURLToPath(
                      new URL("./ccow-extents.py", import.meta.url),
                    ),
                    path.join(source, fixture.cache),
                    path.join(derived, fixture.cache),
                  ],
                  { cwd: root, env },
                ).stdout,
              );
              assert.equal(allocation.shared, true);
              sharing = true;
            });
          }
          const timeFile = path.join(root, `${preset}-${phase}.time`);
          const args = ["run", "--preset", preset, "--", ...argv];
          let build;
          if (process.platform === "linux")
            build = command(
              "/usr/bin/time",
              ["-v", "-o", timeFile, binary, ...args],
              { cwd, env },
            );
          else if (process.platform === "darwin")
            build = command("/usr/bin/time", ["-l", binary, ...args], {
              cwd,
              env,
            });
          else build = run(cwd, args);
          // Record no synthetic zero when allocation/RSS instrumentation is absent.
          attempt(`${preset}-${phase}-measurement`, () => {
            assert.notEqual(
              process.platform,
              "win32",
              "Windows process RSS instrumentation unavailable",
            );
            const allocation = JSON.parse(
              command(
                "python3",
                [
                  fileURLToPath(new URL("./ccow-extents.py", import.meta.url)),
                  path.join(source, fixture.cache),
                  ...(cwd === derived
                    ? [path.join(derived, fixture.cache)]
                    : []),
                ],
                { cwd: root, env },
              ).stdout,
            );
            result.measurements.push({
              preset,
              phase,
              physicalBytes: allocation.physicalBytes,
              preparationMs: preparation.ms,
              buildMs: build.ms,
              peakMemoryBytes: parsePeakMemory(
                process.platform === "linux"
                  ? readFileSync(timeFile, "utf8")
                  : build.stderr,
                process.platform,
              ),
            });
          });
          if (phase === "warm-source") {
            original = snapshot(path.join(source, fixture.cache));
            assert(Object.keys(original).length > 0);
          }
          if (cwd === derived)
            assert.deepEqual(
              snapshot(path.join(source, fixture.cache)),
              original,
            );
          if (preset === "cargo")
            assert.equal(
              command(path.join(cwd, fixture.output), [], {
                cwd,
                env,
              }).stdout.trim(),
              phase === "changed-worktree" ? "ccow-changed" : "ccow-original",
            );
          else
            assert(
              Object.values(snapshot(path.join(cwd, fixture.output))).length >
                0,
            );
        }
        result.tools[preset] = toolVersion;
        result.checks[`${preset}-minimum`] = true;
        result.scenarios.push({
          id: `${preset}-write-isolation`,
          status: "passed",
        });
        if (sharing)
          result.scenarios.push({
            id: `${preset}-physical-sharing`,
            status: "passed",
          });
        attempt(`${preset}-freshness`, () => {
          for (const change of ["tracked", "untracked"]) {
            const value = `ccow-${change}`;
            if (preset === "cargo") {
              if (change === "untracked")
                write(
                  derived,
                  "src/value.rs",
                  `pub const VALUE: &str = "${value}";\n`,
                );
              write(
                derived,
                "src/main.rs",
                change === "tracked"
                  ? `fn main() { println!("${value}"); }\n`
                  : 'mod value; fn main() { println!("{}", value::VALUE); }\n',
              );
            } else {
              if (change === "untracked")
                write(derived, "src/value.js", `export default "${value}";\n`);
              write(
                derived,
                "src/index.js",
                change === "tracked"
                  ? `console.log("${value}");\n`
                  : 'import value from "./value.js"; console.log(value);\n',
              );
            }
            run(derived, ["run", "--preset", preset, "--", ...fixture.argv]);
            const output = () =>
              preset === "cargo"
                ? command(path.join(derived, fixture.output), [], {
                    cwd: derived,
                    env,
                  }).stdout
                : snapshot(path.join(derived, fixture.output));
            const warm = output();
            if (preset === "cargo") assert.equal(warm.trim(), value);
            // Same sources and command, with all generated cache/output removed.
            // This is an independent clean build, not a comparison of cache bytes.
            rmSync(path.join(derived, fixture.cache), {
              recursive: true,
              force: true,
            });
            if (preset !== "cargo")
              rmSync(path.join(derived, fixture.output), {
                recursive: true,
                force: true,
              });
            command(fixture.argv[0], fixture.argv.slice(1), {
              cwd: derived,
              env,
            });
            assert.deepEqual(output(), warm);
            assert.deepEqual(
              snapshot(path.join(source, fixture.cache)),
              original,
            );
          }
        });
        if (preset === "cargo") {
          result.checks["native-install-no-node"] = attempt(
            "native-node-free-cargo",
            () =>
              nodeFreeScenario({ binary, cwd: source, fixture, env, command }),
          );
          run(source, ["run", "--preset", "cargo", "--", ...fixture.argv]);
          // Restore the source snapshot after a deliberately cold Node-free build.
          original = snapshot(path.join(source, fixture.cache));
          write(source, "src/bin/ccow-probe.rs", probeSource);
          write(derived, "src/bin/ccow-probe.rs", probeSource);
          attempt("native-cross-volume-copy", () =>
            crossVolumeScenario({
              binary,
              source,
              fixture,
              env,
              command,
              snapshot,
              workspace: process.env.CCOW_ACCEPTANCE_COPY_WORKSPACE,
            }),
          );
          attempt("native-unknown-settings-version", () =>
            unknownVersionScenario({
              binary,
              cwd: source,
              env,
              command,
              snapshot,
            }),
          );
          attempt("native-replacement-preservation", () =>
            replacementScenario({
              binary,
              source,
              derived,
              fixture,
              env,
              snapshot,
              command,
            }),
          );
          attempt("native-shim-dispatch", () =>
            shimScenarios({ binary, root, source, env, command, snapshot }),
          );
          shellScenarios({ binary, root, env, command, attempt });
          await lifecycleScenarios({
            binary,
            source,
            derived,
            root,
            env,
            command,
            snapshot,
            attempt: attemptAsync,
          });
          result.checks.privacy = attempt("native-private-state-audit", () =>
            privacyScenario({ binary, cwd: source, env, command }),
          );
          // Probe builds legitimately change the source; isolation after clean
          // compares against the last quiescent state rather than the old build.
          original = snapshot(path.join(source, fixture.cache));
        }
        run(derived, ["clean", "--preset", preset]);
        assert(!existsSync(path.join(derived, fixture.cache)));
        assert.deepEqual(snapshot(path.join(source, fixture.cache)), original);
        result.scenarios.push({
          id: `${preset}-clean-isolation`,
          status: "passed",
        });
      });
    }
    for (const check of [
      "write-isolation",
      "clean-isolation",
      "physical-sharing",
    ])
      result.checks[check] = ["cargo", "rspack", "rsbuild"].every((preset) =>
        result.scenarios.some(
          (item) =>
            item.id === `${preset}-${check}` && item.status === "passed",
        ),
      );
    result.checks.freshness = ["cargo", "rspack", "rsbuild"].every((preset) =>
      result.scenarios.some(
        (item) => item.id === `${preset}-freshness` && item.status === "passed",
      ),
    );
    // Exercise emitted code and owned profile round trips, but do not promote
    // these partial observations to the full shell/shim acceptance groups.
    attempt("shim-install-idempotent", () => {
      run(root, ["shim", "install"]);
      run(root, ["shim", "install"]);
    });
    for (const shell of ["bash", "zsh", "powershell"])
      attempt(`shell-${shell}-roundtrip`, () => {
        const profile = path.join(home, `${shell}.profile`);
        const before = "# unrelated user configuration\n";
        writeFileSync(profile, before);
        const init = run(root, ["shell", "init", shell]).stdout;
        const script = path.join(
          root,
          shell === "powershell" ? "activate.ps1" : `activate.${shell}`,
        );
        writeFileSync(
          script,
          init +
            (shell === "powershell"
              ? "\nif (-not (Get-Command cargo -ErrorAction Stop)) { exit 1 }\n"
              : "\ncommand -v cargo\n"),
        );
        command(
          shell === "powershell" ? "pwsh" : shell,
          shell === "powershell"
            ? ["-NoProfile", "-NonInteractive", "-File", script]
            : [script],
          { cwd: root, env },
        );
        run(root, ["shell", "setup", shell, "--file", profile, "--yes"]);
        const once = readFileSync(profile, "utf8");
        assert.notEqual(once, before);
        assert(once.includes(before));
        run(root, ["shell", "setup", shell, "--file", profile, "--yes"]);
        assert.equal(readFileSync(profile, "utf8"), once);
        run(root, ["shell", "remove", shell, "--file", profile, "--yes"]);
        run(root, ["shell", "remove", shell, "--file", profile, "--yes"]);
        assert.equal(readFileSync(profile, "utf8"), before);
      });
    return finish();
  } finally {
    // Only remove the freshly allocated driver-owned directory.
    // A timed-out supervisor may still own writers. Do not delete their files
    // until an operator verifies process termination; this is not acceptance.
    if (root && !retainWorkspace)
      rmSync(root, { recursive: true, force: true });
  }
  function finish() {
    if (retainWorkspace)
      block(
        "process-tree",
        "timed-out-command-requires-runner-process-cleanup",
      );
    result.blockers = result.blockers.filter((item) => !result.checks[item.id]);
    result.unperformed = checks.filter((id) => !result.checks[id]);
    return result;
  }
}
