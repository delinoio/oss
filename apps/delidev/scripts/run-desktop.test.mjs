import { spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { desktopArguments, desktopEnvironment, runDesktop } from "./run-desktop.mjs";
import { DevelopmentSigningError, developmentBundleDirectory } from "./development-signing.mjs";

const success = { code: 0, signal: null };
const environment = { npm_execpath: "/tools/pnpm.cjs", PATH: "/tools", HOME: "/fixture" };
const identity = "A".repeat(40);
const macFixtures = { identityFor: async () => identity, lock: () => () => {}, publish: async () => "/immutable/DeliDev.app/Contents/MacOS/delidev-desktop" };

test("macOS prepares a CEF bundle with embedded assets and preserves application argv", async () => {
  const calls = [];
  const logs = [];
  let released = false;
  const args = ["--data-dir", "/private/델리 dev/$literal`argument`"];
  assert.deepEqual(await runDesktop(["--", ...args], {
    platform: "darwin", arch: "arm64", environment,
    ...macFixtures,
    lock: () => () => { released = true; },
    publish: async (_source, output, pinned) => { assert.equal(output, developmentBundleDirectory()); assert.equal(pinned, identity); assert.equal(released, false); return macFixtures.publish(); },
    creditsFor: () => "/cef/CREDITS.html",
    log: entry => logs.push(entry),
    run: async (...call) => { calls.push(call); return success; },
  }), success);
  assert.equal(calls.length, 3);
  assert.deepEqual(calls[0][1], [environment.npm_execpath, "build:native"]);
  const [command, argv, options, lifecycle] = calls[1];
  assert.equal(command, process.execPath);
  assert.ok(argv[0].replaceAll("\\", "/").endsWith("/scripts/tauri-cli.mjs"));
  assert.ok(argv.includes("build"));
  assert.ok(argv.includes("--debug"));
  assert.deepEqual(argv.slice(argv.indexOf("--bundles"), argv.indexOf("--bundles") + 2), ["--bundles", "app"]);
  assert.ok(argv.includes("desktop-host,custom-protocol"));
  assert.ok(!argv.includes(args[1]));
  assert.equal(calls[2][0], await macFixtures.publish());
  assert.deepEqual(calls[2][1], args);
  assert.equal(calls[2][3].terminateProcessTree, false);
  assert.equal(typeof calls[2][3].onStdout, "function");
  assert.equal(typeof calls[2][3].onStderr, "function");
  assert.equal(calls[2][2].detached, true);
  assert.equal(released, true);
  const config = JSON.parse(argv[argv.indexOf("--config") + 1]);
  assert.equal(config.build.devUrl, null);
  assert.equal(config.bundle.macOS.signingIdentity, "-");
  assert.equal(config.bundle.resources["/cef/CREDITS.html"], "notices/Chromium-CREDITS.html");
  assert.equal(options.shell, false);
  assert.equal(calls[0][2].env.MACOSX_DEPLOYMENT_TARGET, "13.0");
  assert.deepEqual(calls[0][2].env, options.env);
  assert.equal(lifecycle.terminateProcessTree, true);
  assert.equal(calls[0][3].terminateProcessTree, true);
  assert.equal(JSON.stringify(logs).includes(args[1]), false);
});

test("Windows and Linux retain the direct Cargo executable path", () => {
  for (const platform of ["win32", "linux"]) {
    assert.deepEqual(desktopArguments(platform, ["--data-dir", "/private/test"]), [
      "run", "--locked", "--manifest-path", "src-tauri/Cargo.toml",
      "--features", "desktop-host,custom-protocol", "--bin", "delidev-desktop",
      "--", "--data-dir", "/private/test",
    ]);
    assert.deepEqual(desktopEnvironment(platform, environment), environment);
  }
});

test("local macOS preparation and bundling share the configured deployment target and CEF cache without signing credentials", () => {
  const env = desktopEnvironment("darwin", {
    ...environment, CARGO_TARGET_DIR: "/build output", CEF_PATH: "/foreign-cef",
    APPLE_SIGNING_IDENTITY: "private", APPLE_CERTIFICATE: "private", APPLE_CERTIFICATE_PASSWORD: "private",
    APPLE_ID: "private", APPLE_PASSWORD: "private", APPLE_TEAM_ID: "private",
    APPLE_API_KEY: "private", APPLE_API_ISSUER: "private", APPLE_API_KEY_PATH: "private",
    TAURI_SIGNING_PRIVATE_KEY: "private", NODE_OPTIONS: "--require private",
    MACOSX_DEPLOYMENT_TARGET: "27.0",
  }, "/fixture");
  assert.deepEqual(env, {
    PATH: "/tools", HOME: "/fixture", CARGO_TARGET_DIR: "/build output",
    CEF_PATH: "/fixture/Library/Caches/tauri-cef",
    MACOSX_DEPLOYMENT_TARGET: JSON.parse(readFileSync(new URL("../src-tauri/tauri.conf.json", import.meta.url), "utf8")).bundle.macOS.minimumSystemVersion,
  });
  assert.equal(desktopEnvironment("darwin", { CARGO_TARGET_DIR: "build output" }).CARGO_TARGET_DIR,
    fileURLToPath(new URL("../build output", import.meta.url)));
});

test("failed or interrupted preparation never launches the application", async () => {
  for (const result of [{ code: 7, signal: null }, { code: null, signal: "SIGINT" }]) {
    let calls = 0;
    assert.deepEqual(await runDesktop([], {
      platform: "darwin", environment, log() {},
      ...macFixtures,
      run: async () => { calls++; return result; },
      creditsFor: () => assert.fail("must not resolve bundle resources after failed preparation"),
    }), result);
    assert.equal(calls, 1);
  }
});

test("missing local identity and failed publication never launch or terminate a server", async () => {
  let calls = 0;
  let releases = 0;
  const logs = [];
  const options = { platform: "darwin", arch: "arm64", environment, ...macFixtures, creditsFor: () => "/cef/CREDITS.html",
    log: row => logs.push(row), lock: () => () => releases++, run: async () => { calls++; return success; } };
  await runDesktop([], { ...options, identityFor: async () => { throw new DevelopmentSigningError("development-certificate-missing"); } });
  assert.equal(calls, 0);
  assert.equal(releases, 0);
  await runDesktop([], { ...options, publish: async () => { throw new DevelopmentSigningError("development-signing-command-failed"); } });
  assert.equal(calls, 2);
  assert.equal(releases, 1);
  assert.equal(logs.at(-1).code, "development-signing-command-failed");
});

test("child failures retain status and exception logs omit private argv", async () => {
  const logs = [];
  let calls = 0;
  const failure = { code: 23, signal: null };
  assert.deepEqual(await runDesktop([], {
    platform: "linux", environment, log: entry => logs.push(entry),
    run: async () => ++calls === 1 ? success : failure,
  }), failure);
  assert.equal(logs.at(-1).stage, "run");
  assert.equal(logs.at(-1).code, 23);
  assert.deepEqual(await runDesktop([], {
    platform: "linux", environment, log: entry => logs.push(entry),
    run: async () => { throw new Error("spawn /private/user-data --token secret"); },
  }), { code: 1, signal: null });
  assert.equal(logs.at(-1).code, "launch-failed");
  assert.doesNotMatch(JSON.stringify(logs), /user-data|secret/);
});

test("SIGTERM reaches the active child during preparation and execution", { skip: process.platform === "win32", timeout: 20_000 }, async t => {
  const root = mkdtempSync(join(tmpdir(), "delidev-launch-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const scenarios = ["prepare", "run"].flatMap(stage => [
    { stage, vanishedProcess: false },
    ...(process.platform === "linux" ? [{ stage, vanishedProcess: true }] : []),
  ]);
  for (const { stage, vanishedProcess } of scenarios) {
    const receipt = join(root, `${stage}-${vanishedProcess}`);
    const childCode = `
      const fs = require('node:fs');
      process.on('SIGTERM', () => {
        fs.writeFileSync(${JSON.stringify(receipt)}, 'terminated');
        setTimeout(() => process.exit(0), ${vanishedProcess ? 50 : 0});
      });
      setInterval(() => {}, 1000);
      process.stdout.write('child-ready\\n');
    `;
    const wrapper = spawn(process.execPath, ["--input-type=module", "-e", `
      import { runDesktop } from ${JSON.stringify(new URL("./run-desktop.mjs", import.meta.url).href)};
      import { spawnDevServer, exitLikeChild } from ${JSON.stringify(new URL("../../../scripts/spawn-dev-server.mjs", import.meta.url).href)};
      import fs from 'node:fs';
      import { syncBuiltinESMExports } from 'node:module';
      if (${vanishedProcess}) {
        // Reproduce a vanished /proc entry deterministically while retaining
        // real child/process-group termination and the other real proc reads.
        const enumerate = fs.readdirSync;
        fs.readdirSync = (path, options) => {
          if (path !== '/proc') return enumerate(path, options);
          if (options?.withFileTypes) throw Object.assign(new Error('vanished process'), { code: 'ENOENT' });
          process.stderr.write('vanished-process-observed\\n');
          return [...enumerate(path, options), '2147483647'];
        };
        syncBuiltinESMExports();
      }
      let calls = 0;
      exitLikeChild(await runDesktop([], {
        platform: 'linux', environment: { npm_execpath: '/fixture/pnpm.cjs' }, log() {},
        run: async (_command, _args, _options, lifecycle) => {
          calls++;
          if (${JSON.stringify(stage)} === 'run' && calls === 1) return { code: 0, signal: null };
          const running = spawnDevServer(process.execPath, ['-e', ${JSON.stringify(childCode)}], { stdio: 'inherit', shell: false }, lifecycle);
          process.stdout.write('wrapper-ready\\n');
          return running;
        }
      }));
    `], { stdio: ["ignore", "pipe", "pipe"] });
    t.after(() => { if (wrapper.exitCode === null && wrapper.signalCode === null) wrapper.kill("SIGTERM"); });
    let diagnostics = "";
    wrapper.stderr.on("data", chunk => { diagnostics += chunk; });
    const exited = once(wrapper, "exit");
    // Inherited child stdout can arrive while the wrapper is still returning
    // from spawn, before its signal handlers exist. Wait for both readiness
    // markers so scheduling under load cannot kill the wrapper prematurely.
    let output = "";
    for await (const chunk of wrapper.stdout.iterator({ destroyOnReturn: false })) {
      output += chunk;
      if (output.includes("child-ready\n") && output.includes("wrapper-ready\n")) break;
    }
    assert.ok(output.includes("child-ready\n") && output.includes("wrapper-ready\n"));
    wrapper.kill("SIGTERM");
    const [code, signal] = await exited;
    assert.equal(code, null, diagnostics);
    assert.equal(signal, "SIGTERM");
    assert.equal(readFileSync(receipt, "utf8"), "terminated");
    if (vanishedProcess) assert.match(diagnostics, /vanished-process-observed/u);
  }
});

// Synthetic children exercise output handling without compiling or launching a
// desktop, Cargo, signing tool, or real account environment.
test("captured command diagnostics hide argv echoes and preserve final safe output and failure", async () => {
  const privatePath = "/absolute/private/fixture-scope";
  const output = [];
  const errors = [];
  const reports = [];
  let calls = 0;
  const result = await runDesktop(["--data-dir", privatePath], {
    platform: "linux", environment,
    stdout: text => output.push(text), stderr: text => errors.push(text), log: entry => reports.push(entry),
    run: (_command, _args, options, lifecycle) => spawnDevServer(process.execPath, ["-e", ++calls === 1
      ? "process.stdout.write('Preparing assets\\n')"
      : `process.stderr.write('    Running desktop --data-dir ${privatePath}\\n'); process.stdout.write('runtime ready\\n'); process.stderr.write('error: failed reading ${privatePath}\\ncleanup status: uncertain'); process.exitCode = 23;`],
    { ...options, env: process.env }, lifecycle),
  });
  assert.deepEqual(result, { code: 23, signal: null });
  assert.equal(output.join(""), "Preparing assets\nruntime ready\n");
  assert.equal(errors.join(""), "error: failed reading [redacted]\ncleanup status: uncertain");
  assert.equal(JSON.stringify([...output, ...errors, ...reports]).includes(privatePath), false);
  assert.equal(reports.at(-1).state, "failed");
  assert.equal(reports.at(-1).code, 23);
});

test("diagnostic filtering handles fragmented UTF-8, escaped values, command echoes and bounded lines", async () => {
  const { createDiagnosticFilter } = await import("./desktop-diagnostics.mjs");
  const privateValue = "/private/델리 dev/$literal`argument`";
  const output = [];
  const filter = createDiagnosticFilter([privateValue, "--token=secret-value", "private\nsecond-line"], text => output.push(text));
  const fixture = Buffer.from(`\x1b[32m    Running desktop ${privateValue}\x1b[0m\nwarning: ${privateValue}\nerror: ${JSON.stringify(privateValue)}\nretry token secret-value\nprivate\nsecond-line\n`);
  for (let offset = 0; offset < fixture.length; offset += 3) filter.write(fixture.subarray(offset, offset + 3));
  filter.write(Buffer.from("x".repeat(70_000)));
  filter.write(Buffer.from("\nsafe final line"));
  filter.end();
  assert.equal(output.join(""), 'warning: [redacted]\nerror: "[redacted]"\nretry token [redacted]\n[redacted]\n[redacted]\n[desktop diagnostic omitted: oversized line]\nsafe final line');
});

test("captured diagnostics preserve child signal outcomes", { skip: process.platform === "win32" }, async () => {
  let calls = 0;
  const output = [];
  const result = await runDesktop([], {
    platform: "linux", environment, log: () => {}, stdout: text => output.push(text), stderr: () => {},
    run: (_command, _args, options, lifecycle) => spawnDevServer(process.execPath, ["-e", ++calls === 1
      ? "process.stdout.write('prepared\\n')"
      : "process.stdout.write('runtime failure\\n'); process.kill(process.pid, 'SIGTERM')"], { ...options, env: process.env }, lifecycle),
  });
  assert.deepEqual(result, { code: null, signal: "SIGTERM" });
  assert.equal(output.join(""), "prepared\nruntime failure\n");
});

test("surviving descendants cannot hold the captured launcher open", { skip: process.platform === "win32", timeout: 3000 }, async () => {
  let incomplete = 0;
  const result = await spawnDevServer(process.execPath, ["-e", `
    const { spawn } = require('node:child_process');
    const child = spawn(process.execPath, ['-e', 'setTimeout(() => process.exit(0), 600)'], { stdio: ['ignore', 1, 2], detached: true });
    child.unref();
    process.stdout.write('desktop exited\\n');
  `], { stdio: ["ignore", "pipe", "pipe"] }, { onStdout: () => {}, onStderr: () => {}, onOutputIncomplete: () => { incomplete++; } });
  assert.deepEqual(result, success);
  assert.equal(incomplete, 1);
});

test("lock cleanup uncertainty stays visible without exception paths or replacing child failure", async () => {
  const logs = [];
  const result = await runDesktop([], {
    ...macFixtures, platform: "darwin", arch: "arm64", environment,
    lock: () => () => { throw new Error("private lock /absolute/private/fixture-scope"); },
    log: entry => logs.push(entry),
    run: async () => ({ code: 17, signal: null }),
  });
  assert.deepEqual(result, { code: 17, signal: null });
  assert.deepEqual(logs.at(-1), { operation: "desktop_development", stage: "prepare", state: "cleanup-uncertain", code: "build-lock-release-failed" });
  assert.equal(JSON.stringify(logs).includes("/absolute/private/fixture-scope"), false);
});

test("short argument values do not erase unrelated safe diagnostic words", async () => {
  const { createDiagnosticFilter } = await import("./desktop-diagnostics.mjs");
  const output = [];
  const filter = createDiagnosticFilter(["e", "redacted"], text => output.push(text));
  filter.write(Buffer.from("error: recovery pending; token e; argument redacted\n"));
  filter.end();
  assert.equal(output.join(""), "error: recovery pending; token [redacted]; argument [redacted]\n");
});
