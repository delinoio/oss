// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { spawn, spawnSync } from "node:child_process";
import {
  chmodSync,
  existsSync,
  mkdtempSync,
  realpathSync,
  symlinkSync,
  lstatSync,
  readdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import { performance } from "node:perf_hooks";

// This is real build code compiled by the selected native Cargo toolchain. It
// exercises the installed supervisor, not a replacement cargo or clone API.
export const probeSource = `
use std::{env, fs, process::{Command, exit}, thread, time::Duration};
fn main() {
 let a: Vec<String> = env::args().collect();
 match a[1].as_str() {
  "hold" => { fs::write(&a[2], "ready").unwrap(); while !std::path::Path::new(&a[3]).exists() { thread::sleep(Duration::from_millis(20)); } },
  "descendant" | "orphan" => { let child = Command::new(env::current_exe().unwrap()).args(["writer", &a[2]]).spawn().unwrap(); fs::write(&a[3], child.id().to_string()).unwrap(); while !std::path::Path::new(&a[2]).exists() { thread::sleep(Duration::from_millis(20)); } if a[1] == "descendant" { loop { thread::sleep(Duration::from_millis(20)); } } },
  "writer" => { #[cfg(unix)] unsafe { unsafe extern "C" { fn signal(sig: i32, handler: usize) -> usize; } signal(15, 1); } loop { fs::write(&a[2], "writing").unwrap(); thread::sleep(Duration::from_millis(20)); } },
  "nested" => { let status = Command::new(&a[2]).current_dir(&a[3]).args(["run", "--preset", "cargo", "--", "cargo", "build", "--offline"]).status().unwrap(); exit(status.code().unwrap_or(99)); },
  "fail" => { fs::write(&a[2], "retained").unwrap(); exit(37); },
  _ => exit(91)
 }
}
`;

export function stateDirectory(env, platform = process.platform) {
  return path.join(
    platform === "win32"
      ? env.LOCALAPPDATA
      : platform === "darwin"
        ? path.join(env.HOME, "Library", "Application Support")
        : env.XDG_CONFIG_HOME,
    "cachecow",
  );
}

export function expectFailure(binary, args, options, code = 125) {
  const r = spawnSync(binary, args, {
    encoding: "utf8",
    timeout: 30_000,
    stdio: ["ignore", "pipe", "pipe"],
    ...options,
  });
  if (r.error || r.signal) {
    const error = new Error("Native negative scenario did not complete");
    error.processOwnershipUnverified = true;
    throw error;
  }
  assert.equal(r.status, code, "Unexpected native failure status");
  return r;
}

function start(binary, args, options) {
  const child = spawn(binary, args, { ...options, stdio: "ignore" });
  const record = { child, done: false, code: null };
  record.exit = new Promise((resolve) => {
    child.once("error", () => {
      record.done = true;
      resolve();
    });
    child.once("exit", (code) => {
      record.code = code;
      record.done = true;
      resolve();
    });
  });
  return record;
}
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(predicate, timeout = 30_000) {
  const end = performance.now() + timeout;
  while (!predicate()) {
    assert(performance.now() < end, "Native scenario deadline expired");
    await pause(25);
  }
}
async function completed(p) {
  await until(() => p.done);
  assert.equal(p.code, 0, "Native child failed");
}

export async function lifecycleScenarios({
  binary,
  source,
  derived,
  root,
  env,
  command,
  snapshot,
  attempt,
}) {
  const options = (cwd) => ({ cwd, env });
  const argv = [
    "run",
    "--preset",
    "cargo",
    "--",
    "cargo",
    "run",
    "--offline",
    "--bin",
    "ccow-probe",
    "--",
  ];
  const owned = [];
  const launch = (cwd, args) => {
    const p = start(binary, args, options(cwd));
    owned.push(p);
    return p;
  };
  const run = (cwd, args) => command(binary, args, options(cwd));
  const marker = (name) => path.join(root, name);
  const hold = (cwd, name) =>
    launch(cwd, [
      ...argv,
      "hold",
      marker(`${name}.ready`),
      marker(`${name}.release`),
    ]);
  const release = (name) => writeFileSync(marker(`${name}.release`), "release");
  const groups = {};
  // Build the native helper in both worktrees before capturing waiters' source
  // inventories. Concurrent compiler-created filenames are a separate case.
  for (const cwd of [source, derived])
    run(cwd, ["run", "--preset", "cargo", "--", "cargo", "build", "--offline"]);
  try {
    groups.locking = await attempt("native-cache-locking", async () => {
      // Warm both command identities before contention; no source build should
      // be needed merely to run a probe in an existing local cache.
      const one = hold(source, "one");
      await until(() => existsSync(marker("one.ready")));
      expectFailure(binary, ["clean", "--preset", "cargo"], options(source));
      const two = hold(source, "two");
      const other = hold(derived, "other");
      await until(() => existsSync(marker("other.ready")));
      await pause(250);
      assert(!existsSync(marker("two.ready")) && !two.done);
      release("other");
      await completed(other);
      release("one");
      await completed(one);
      await until(() => {
        assert(!two.done, "Waiting writer exited before acquiring its cache");
        return existsSync(marker("two.ready"));
      });
      release("two");
      await completed(two);
      const alias = path.join(root, "source-alias");
      symlinkSync(
        source,
        alias,
        process.platform === "win32" ? "junction" : "dir",
      );
      const aliasOwner = hold(source, "alias-owner");
      await until(() => existsSync(marker("alias-owner.ready")));
      const aliasWaiter = hold(alias, "alias-waiter");
      await pause(250);
      assert(!aliasWaiter.done && !existsSync(marker("alias-waiter.ready")));
      release("alias-owner");
      await completed(aliasOwner);
      await until(() => existsSync(marker("alias-waiter.ready")));
      release("alias-waiter");
      await completed(aliasWaiter);
      for (const cwd of [source, derived]) {
        run(source, [...argv, "nested", binary, cwd]);
      }
      // cargo clean must wait and preserve its original --release semantics.
      run(root, ["shim", "install"]);
      const shim = path.join(
        stateDirectory(env),
        "shims",
        process.platform === "win32" ? "cargo.exe" : "cargo",
      );
      const active = hold(source, "clean");
      await until(() => existsSync(marker("clean.ready")));
      const clean = launch(source, ["--version"]);
      await completed(clean);
      const cleaner = start(shim, ["clean", "--release"], options(source));
      owned.push(cleaner);
      await pause(250);
      assert(!cleaner.done);
      release("clean");
      await completed(active);
      await completed(cleaner);
      assert(existsSync(path.join(source, "target", "debug")));
    });
    for (const name of [
      "one",
      "two",
      "other",
      "clean",
      "alias-owner",
      "alias-waiter",
    ])
      release(name);
    await until(() => owned.every((p) => p.done), 15_000);
    groups["process-tree"] = await attempt(
      "native-descendant-ownership",
      async () => {
        for (const signal of ["SIGTERM", "SIGKILL"]) {
          const beat = marker(`${signal}.beat`),
            pidFile = marker(`${signal}.pid`);
          const parent = launch(source, [...argv, "descendant", beat, pidFile]);
          await until(() => {
            assert.equal(
              parent.code,
              null,
              "Descendant supervisor exited before writer readiness",
            );
            return existsSync(beat) && existsSync(pidFile);
          });
          await pause(250);
          assert(!parent.done, "Supervisor released a surviving descendant");
          const pid = Number(readFileSync(pidFile, "utf8"));
          assert(Number.isInteger(pid) && pid > 1);
          expectFailure(
            binary,
            ["clean", "--preset", "cargo"],
            options(source),
          );
          const cancelledAt = performance.now();
          parent.child.kill(signal);
          await until(() => parent.done);
          // A new managed writer can proceed only after recovery proves that the
          // previous tree is gone. Dead parent PID alone is not sufficient.
          run(source, [
            "run",
            "--preset",
            "cargo",
            "--",
            "cargo",
            "build",
            "--offline",
          ]);
          assert.throws(() => process.kill(pid, 0));
          if (process.platform !== "win32")
            assert(
              performance.now() - cancelledAt >= 4_800,
              "Grace interval was shortened",
            );
          const stamp = lstatSync(beat).mtimeMs;
          await pause(150);
          assert.equal(lstatSync(beat).mtimeMs, stamp);
        }
        const retained = path.join(source, "target", "partial-build");
        expectFailure(binary, [...argv, "fail", retained], options(source), 37);
        assert.equal(readFileSync(retained, "utf8"), "retained");
      },
    );
  } finally {
    // A failed assertion must not leave a deliberately long-running writer.
    for (const name of [
      "one",
      "two",
      "other",
      "clean",
      "alias-owner",
      "alias-waiter",
    ])
      release(name);
    for (const p of owned) if (!p.done) p.child.kill("SIGTERM");
    try {
      await until(() => owned.every((p) => p.done), 15_000);
    } catch {
      const error = new Error("Native process cleanup unverified");
      error.processOwnershipUnverified = true;
      throw error;
    }
    for (const signal of ["SIGTERM", "SIGKILL"]) {
      const file = marker(`${signal}.pid`);
      if (!existsSync(file)) continue;
      const pid = Number(readFileSync(file, "utf8"));
      let alive = false;
      try {
        process.kill(pid, 0);
        alive = true;
      } catch (error) {
        if (error.code !== "ESRCH") alive = true;
      }
      if (alive) {
        const error = new Error("Native descendant cleanup unverified");
        error.processOwnershipUnverified = true;
        throw error;
      }
    }
  }
  // These scenarios cover finite/descendant lifetimes and core contention.
  // Alias, cross-selected sources and five-second stubborn shutdown remain
  // separate required assertions; callers must not promote the full groups.
  return groups;
}

export function privacyScenario({ env, binary, cwd, command }) {
  const sentinel = "ccow-private-value-4b8b17";
  const privateEnv = { ...env, CCOW_ACCEPTANCE_SECRET: sentinel };
  const r = command(
    binary,
    ["doctor", "--preset", "cargo", "--verbose", "--json"],
    { cwd, env: privateEnv },
  );
  assert(!r.stdout.includes(sentinel) && !r.stderr.includes(sentinel));
  const root = stateDirectory(env);
  function visit(dir) {
    const st = lstatSync(dir);
    assert(!st.isSymbolicLink());
    if (process.platform !== "win32") assert.equal(st.mode & 0o077, 0);
    if (st.isDirectory())
      for (const name of readdirSync(dir)) visit(path.join(dir, name));
    else if (st.isFile())
      assert(!readFileSync(dir).includes(Buffer.from(sentinel)));
    // Unix sockets have no persisted content and are allowed in private dirs.
  }
  visit(root);
  if (process.platform === "win32") {
    // Examine actual DACLs, not Unix emulation mode bits. System and the current
    // user are the only permitted allow principals on private state entries.
    command(
      "pwsh",
      [
        "-NoProfile",
        "-NonInteractive",
        "-Command",
        "$ErrorActionPreference='Stop'; $u=[System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value; $items=@(Get-Item -LiteralPath $env:CCOW_AUDIT_ROOT)+@(Get-ChildItem -LiteralPath $env:CCOW_AUDIT_ROOT -Recurse -Force); foreach($i in $items){foreach($a in (Get-Acl -LiteralPath $i.FullName).Access){if($a.AccessControlType -eq 'Allow' -and $a.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value -notin @($u,'S-1-5-18')){exit 1}}}",
      ],
      { cwd, env: { ...env, CCOW_AUDIT_ROOT: root } },
    );
  }
}

export function nodeFreeScenario({ binary, cwd, fixture, env, command }) {
  // Remove whole PATH entries that expose Node, including version-manager
  // shims. Check the resulting process search, then run an actual Cargo build.
  const names =
    process.platform === "win32"
      ? ["node.exe", "node.cmd", "node.bat", "node.com"]
      : ["node"];
  const cleanEnv = {
    ...env,
    PATH: (env.PATH || "")
      .split(path.delimiter)
      .filter(
        (dir) => dir && !names.some((name) => existsSync(path.join(dir, name))),
      )
      .join(path.delimiter),
  };
  const absent = spawnSync("node", ["--version"], { cwd, env: cleanEnv });
  assert.equal(absent.error?.code, "ENOENT", "Node remains discoverable");
  rmSync(path.join(cwd, fixture.cache), { recursive: true, force: true });
  command(binary, ["run", "--preset", "cargo", "--", ...fixture.argv], {
    cwd,
    env: cleanEnv,
  });
  assert.equal(
    command(path.join(cwd, fixture.output), [], {
      cwd,
      env: cleanEnv,
    }).stdout.trim(),
    "ccow-original",
  );
}

export function unknownVersionScenario({
  binary,
  cwd,
  env,
  command,
  snapshot,
}) {
  command(binary, ["config", "set", "source", cwd], { cwd, env });
  const root = stateDirectory(env);
  const settings = readdirSync(root).filter(
    (name) => name.startsWith("settings-") && name.endsWith(".json"),
  );
  assert(settings.length > 0);
  // Mutate only disposable records created by this scenario. This verifies the
  // documented forward-version boundary; no real user state is inspected.
  const originals = settings.map((name) => [
    path.join(root, name),
    readFileSync(path.join(root, name)),
  ]);
  try {
    for (const [file, bytes] of originals)
      writeFileSync(
        file,
        JSON.stringify({ ...JSON.parse(bytes), schemaVersion: 2147483647 }),
      );
    const before = snapshot(root);
    for (const args of [
      ["config", "get"],
      ["config", "set", "source", cwd],
      ["config", "unset", "source"],
    ]) {
      const failed = expectFailure(
        binary,
        [...args, "--json"],
        { cwd, env },
        2,
      );
      const error = JSON.parse(failed.stdout);
      assert.equal(error.success, false);
      assert(error.error.remediation.length > 0);
      assert.deepEqual(snapshot(root), before);
    }
  } finally {
    for (const [file, bytes] of originals) writeFileSync(file, bytes);
  }
}

export function replacementScenario({
  binary,
  source,
  derived,
  fixture,
  env,
  snapshot,
  command,
}) {
  const cache = path.join(derived, fixture.cache);
  const before = snapshot(cache);
  const sourceCache = path.join(source, fixture.cache);
  const original = snapshot(sourceCache);
  const args = [
    "prepare",
    "--preset",
    "cargo",
    "--source",
    source,
    "--replace",
    "--",
    ...fixture.argv,
  ];
  // A missing source must preserve a usable destination, including all bytes.
  const missing = path.join(source, "missing-source");
  expectFailure(
    binary,
    [
      "prepare",
      "--preset",
      "cargo",
      "--source",
      missing,
      "--replace",
      "--",
      ...fixture.argv,
    ],
    { cwd: derived, env },
  );
  assert.deepEqual(snapshot(cache), before);
  if (process.platform !== "win32") {
    const mode = lstatSync(sourceCache).mode & 0o777;
    chmodSync(sourceCache, 0);
    try {
      // Root can bypass POSIX permissions; it must not report this injection as
      // a permission denial test. The acceptance runner must use a normal user.
      assert(process.getuid?.() !== 0);
      expectFailure(binary, args, { cwd: derived, env });
      assert.deepEqual(snapshot(cache), before);
    } finally {
      chmodSync(sourceCache, mode);
    }
  } else throw new Error("Native Windows denial injection not provisioned");
  assert.deepEqual(snapshot(sourceCache), original);
  assert(
    !readdirSync(derived).some(
      (name) =>
        name.includes(".ccow-") &&
        (name.endsWith(".stage") || name.endsWith(".backup")),
    ),
  );
  command(binary, args, { cwd: derived, env });
  assert.deepEqual(snapshot(cache), original);
}

export function shellScenarios({ binary, root, env, command, attempt }) {
  const run = (args) => command(binary, args, { cwd: root, env });
  for (const shell of process.platform === "win32"
    ? ["bash", "powershell"]
    : ["bash", "zsh", "powershell"]) {
    attempt(`native-shell-${shell}`, () => {
      const profile = path.join(root, `${shell}.profile`);
      const before = "# unrelated user configuration\n";
      writeFileSync(profile, before);
      for (const verb of ["setup", "remove"]) {
        expectFailure(binary, ["shell", verb, shell, "--file", profile], {
          cwd: root,
          env,
        });
        assert.equal(readFileSync(profile, "utf8"), before);
      }
      run(["shell", "setup", shell, "--file", profile, "--yes"]);
      const registered = readFileSync(profile, "utf8");
      assert(registered.includes(before));
      run(["shell", "setup", shell, "--file", profile, "--yes"]);
      assert.equal(readFileSync(profile, "utf8"), registered);
      const script = path.join(
        root,
        shell === "powershell" ? "shell-check.ps1" : `shell-check.${shell}`,
      );
      // Paths are passed as arguments/environment, never interpolated as code.
      writeFileSync(
        script,
        shell === "powershell"
          ? ". $env:CCOW_TEST_PROFILE\n& cargo --version\nif($LASTEXITCODE -ne 0){exit $LASTEXITCODE}\n"
          : '. "$CCOW_TEST_PROFILE"\ncargo --version\n',
      );
      const shellEnv = { ...env, CCOW_TEST_PROFILE: profile };
      const output = command(
        shell === "powershell" ? "pwsh" : shell,
        shell === "powershell"
          ? ["-NoProfile", "-NonInteractive", "-File", script]
          : [script],
        { cwd: root, env: shellEnv },
      );
      assert.match(output.stdout, /cargo 1\.99\.1/u);
      if (process.platform !== "win32") {
        const parent = path.dirname(profile);
        const old = lstatSync(parent).mode & 0o777;
        chmodSync(parent, 0o500);
        try {
          assert(process.getuid?.() !== 0);
          expectFailure(
            binary,
            ["shell", "remove", shell, "--file", profile, "--yes"],
            { cwd: root, env },
          );
          assert.equal(readFileSync(profile, "utf8"), registered);
        } finally {
          chmodSync(parent, old);
        }
      }
      run(["shell", "remove", shell, "--file", profile, "--yes"]);
      run(["shell", "remove", shell, "--file", profile, "--yes"]);
      assert.equal(readFileSync(profile, "utf8"), before);
    });
  }
}

export function shimScenarios({
  binary,
  root,
  source,
  env,
  command,
  snapshot,
}) {
  const run = (args) => command(binary, args, { cwd: root, env });
  run(["shim", "install"]);
  run(["shim", "install"]);
  const dir = path.join(stateDirectory(env), "shims");
  const cargo = path.join(
    dir,
    process.platform === "win32" ? "cargo.exe" : "cargo",
  );
  const activated = { ...env, PATH: dir + path.delimiter + env.PATH };
  const baseline = command("cargo", ["--version"], { cwd: root, env }).stdout;
  for (const cwd of [root, source]) {
    assert.equal(
      command(cargo, ["--version"], { cwd, env: activated }).stdout,
      baseline,
    );
    assert.equal(
      command(cargo, ["--version"], {
        cwd,
        env: { ...activated, CCOW_BYPASS: "1" },
      }).stdout,
      baseline,
    );
  }
  expectFailure(
    cargo,
    ["--version"],
    { cwd: root, env: { ...env, PATH: dir } },
    127,
  );
  command(cargo, ["build", "--offline"], { cwd: source, env: activated });
  command(
    binary,
    ["run", "--preset", "cargo", "--", cargo, "build", "--offline"],
    { cwd: source, env: activated },
  );
  for (const verb of ["list", "doctor"]) {
    const report = JSON.parse(run(["shim", verb, "--json"]).stdout);
    assert.equal(report.schemaVersion, 1);
    assert.equal(report.success, true);
  }
  const cache = snapshot(path.join(source, "target"));
  const unrelated = path.join(dir, "unrelated-file");
  writeFileSync(unrelated, "preserve", { mode: 0o600 });
  run(["shim", "uninstall"]);
  assert.equal(readFileSync(unrelated, "utf8"), "preserve");
  assert.deepEqual(snapshot(path.join(source, "target")), cache);
  writeFileSync(cargo, "foreign owner", { mode: 0o700 });
  expectFailure(binary, ["shim", "install"], { cwd: root, env });
  assert.equal(readFileSync(cargo, "utf8"), "foreign owner");
  // Uninstall must not remove a conflicting tool even when the command fails.
  const uninstalled = spawnSync(binary, ["shim", "uninstall"], {
    cwd: root,
    env,
    stdio: "ignore",
    timeout: 30_000,
  });
  assert(!uninstalled.error && !uninstalled.signal);
  assert.equal(readFileSync(cargo, "utf8"), "foreign owner");
  rmSync(cargo);
  run(["shim", "install"]);
}

// The runner provisions an existing second volume; this driver never formats or
// fills a caller-owned volume. All writes stay inside a newly allocated child.
export function crossVolumeScenario({
  binary,
  source,
  fixture,
  env,
  command,
  snapshot,
  workspace,
}) {
  assert(workspace, "Second-volume acceptance workspace unavailable");
  const other = realpathSync(
    mkdtempSync(path.join(workspace, ".ccow-cross-volume-")),
  );
  chmodSync(other, 0o700);
  let registered = false;
  try {
    assert.notEqual(
      lstatSync(other).dev,
      lstatSync(source).dev,
      "Need distinct native volumes",
    );
    command("git", ["worktree", "add", "--detach", other], {
      cwd: source,
      env,
    });
    registered = true;
    const before = snapshot(path.join(source, fixture.cache));
    const args = [
      "prepare",
      "--preset",
      "cargo",
      "--source",
      source,
      "--",
      ...fixture.argv,
    ];
    expectFailure(binary, args, { cwd: other, env });
    assert(!existsSync(path.join(other, fixture.cache)));
    const copied = command(
      binary,
      [
        "prepare",
        "--preset",
        "cargo",
        "--source",
        source,
        "--allow-copy",
        "--",
        ...fixture.argv,
      ],
      { cwd: other, env },
    );
    assert.match(copied.stderr, /Ordinary copying was used/u);
    assert.deepEqual(snapshot(path.join(other, fixture.cache)), before);
    writeFileSync(
      path.join(other, fixture.cache, "isolation-probe"),
      "derived",
    );
    assert.deepEqual(snapshot(path.join(source, fixture.cache)), before);
    command(binary, ["clean", "--preset", "cargo"], { cwd: other, env });
    assert(!existsSync(path.join(other, fixture.cache)));
    assert.deepEqual(snapshot(path.join(source, fixture.cache)), before);
  } finally {
    // git's worktree entry must also be removed; never prune unrelated entries.
    if (registered)
      command("git", ["worktree", "remove", "--force", other], {
        cwd: source,
        env,
      });
    rmSync(other, { recursive: true, force: true });
  }
}
