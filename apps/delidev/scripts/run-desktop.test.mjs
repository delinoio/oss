import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { desktopArguments, desktopEnvironment, runDesktop } from "./run-desktop.mjs";

const success = { code: 0, signal: null };
const environment = { npm_execpath: "/tools/pnpm.cjs", PATH: "/tools", HOME: "/fixture" };

test("macOS prepares a CEF bundle with embedded assets and preserves application argv", async () => {
  const calls = [];
  const logs = [];
  const args = ["--data-dir", "/private/델리 dev/$literal`argument`"];
  assert.deepEqual(await runDesktop(["--", ...args], {
    platform: "darwin", arch: "arm64", environment,
    creditsFor: () => "/cef/CREDITS.html",
    log: entry => logs.push(entry),
    run: async (...call) => { calls.push(call); return success; },
  }), success);
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0][1], [environment.npm_execpath, "build:native"]);
  const [command, argv, options, lifecycle] = calls[1];
  assert.equal(command, process.execPath);
  assert.ok(argv[0].replaceAll("\\", "/").endsWith("/scripts/tauri-cli.mjs"));
  assert.ok(argv.includes("dev"));
  assert.ok(argv.includes("--no-watch"));
  assert.ok(argv.includes("--no-dev-server"));
  assert.ok(argv.includes("desktop-host,custom-protocol"));
  assert.deepEqual(argv.slice(-args.length - 1), ["--", ...args]);
  const config = JSON.parse(argv[argv.indexOf("--config") + 1]);
  assert.equal(config.build.devUrl, null);
  assert.equal(config.bundle.macOS.signingIdentity, "-");
  assert.equal(config.bundle.resources["/cef/CREDITS.html"], "notices/Chromium-CREDITS.html");
  assert.equal(options.shell, false);
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

test("local macOS bundling excludes signing credentials and shares one CEF cache across build and run", () => {
  const env = desktopEnvironment("darwin", {
    ...environment, CARGO_TARGET_DIR: "/build output", CEF_PATH: "/foreign-cef",
    APPLE_SIGNING_IDENTITY: "private", APPLE_CERTIFICATE: "private", APPLE_CERTIFICATE_PASSWORD: "private",
    APPLE_ID: "private", APPLE_PASSWORD: "private", APPLE_TEAM_ID: "private",
    APPLE_API_KEY: "private", APPLE_API_ISSUER: "private", APPLE_API_KEY_PATH: "private",
    TAURI_SIGNING_PRIVATE_KEY: "private", NODE_OPTIONS: "--require private",
  }, "/fixture");
  assert.deepEqual(env, {
    PATH: "/tools", HOME: "/fixture", CARGO_TARGET_DIR: "/build output",
    CEF_PATH: "/fixture/Library/Caches/tauri-cef",
  });
  assert.equal(desktopEnvironment("darwin", { CARGO_TARGET_DIR: "build output" }).CARGO_TARGET_DIR,
    fileURLToPath(new URL("../build output", import.meta.url)));
});

test("failed or interrupted preparation never launches the application", async () => {
  for (const result of [{ code: 7, signal: null }, { code: null, signal: "SIGINT" }]) {
    let calls = 0;
    assert.deepEqual(await runDesktop([], {
      platform: "darwin", environment, log() {},
      run: async () => { calls++; return result; },
      creditsFor: () => assert.fail("must not resolve bundle resources after failed preparation"),
    }), result);
    assert.equal(calls, 1);
  }
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
    environment, log: entry => logs.push(entry),
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
