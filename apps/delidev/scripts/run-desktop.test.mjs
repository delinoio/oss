import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { PassThrough, Writable } from "node:stream";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import {
  captureDesktopDiagnostics,
  desktopDiagnosticFilter,
} from "./desktop-diagnostics.mjs";
import {
  desktopArguments,
  desktopColorEnvironment,
  desktopEnvironment,
  runDesktop,
} from "./run-desktop.mjs";
import {
  DevelopmentSigningError,
  developmentBundleDirectory,
} from "./development-signing.mjs";

const success = { code: 0, signal: null };
const environment = {
  npm_execpath: "/tools/pnpm.cjs",
  PATH: "/tools",
  HOME: "/fixture",
};
const identity = "A".repeat(40);
const macFixtures = {
  identityFor: async () => identity,
  lock: () => () => {},
  publish: async () => "/immutable/DeliDev.app/Contents/MacOS/delidev-desktop",
};

test("macOS prepares a CEF bundle with embedded assets and preserves application argv", async () => {
  const calls = [];
  const logs = [];
  let released = false;
  const args = ["--data-dir", "/private/델리 dev/$literal`argument`"];
  assert.deepEqual(
    await runDesktop(["--", ...args], {
      platform: "darwin",
      arch: "arm64",
      environment,
      ...macFixtures,
      lock: () => () => {
        released = true;
      },
      publish: async (_source, output, pinned) => {
        assert.equal(output, developmentBundleDirectory());
        assert.equal(pinned, identity);
        assert.equal(released, false);
        return macFixtures.publish();
      },
      creditsFor: () => "/cef/CREDITS.html",
      log: (entry) => logs.push(entry),
      run: async (...call) => {
        calls.push(call);
        return success;
      },
    }),
    success,
  );
  assert.equal(calls.length, 3);
  assert.deepEqual(calls[0][1], [environment.npm_execpath, "build:native"]);
  const [command, argv, options, lifecycle] = calls[1];
  assert.equal(command, process.execPath);
  assert.ok(argv[0].replaceAll("\\", "/").endsWith("/scripts/tauri-cli.mjs"));
  assert.ok(argv.includes("build"));
  assert.ok(argv.includes("--debug"));
  assert.deepEqual(
    argv.slice(argv.indexOf("--bundles"), argv.indexOf("--bundles") + 2),
    ["--bundles", "app"],
  );
  assert.ok(argv.includes("desktop-host,custom-protocol"));
  assert.ok(!argv.includes(args[1]));
  assert.equal(calls[2][0], await macFixtures.publish());
  assert.deepEqual(calls[2][1], args);
  assert.equal(calls[2][3].terminateProcessTree, false);
  assert.equal(typeof calls[2][3].captureOutput, "function");
  assert.deepEqual(calls[2][2].stdio, ["inherit", "pipe", "pipe"]);
  assert.equal(calls[2][2].detached, true);
  assert.equal(released, true);
  const config = JSON.parse(argv[argv.indexOf("--config") + 1]);
  assert.equal(config.build.devUrl, null);
  assert.equal(config.bundle.macOS.signingIdentity, "-");
  assert.equal(
    config.bundle.resources["/cef/CREDITS.html"],
    "notices/Chromium-CREDITS.html",
  );
  assert.equal(options.shell, false);
  assert.equal(calls[0][2].env.MACOSX_DEPLOYMENT_TARGET, "13.0");
  assert.deepEqual(calls[0][2].env, options.env);
  assert.equal(lifecycle.terminateProcessTree, true);
  assert.equal(calls[0][3].terminateProcessTree, true);
  assert.equal(JSON.stringify(logs).includes(args[1]), false);
});

test("Windows and Linux retain the direct Cargo executable path", () => {
  for (const platform of ["win32", "linux"]) {
    assert.deepEqual(
      desktopArguments(platform, ["--data-dir", "/private/test"]),
      [
        "run",
        "--locked",
        "--manifest-path",
        "src-tauri/Cargo.toml",
        "--features",
        "desktop-host,custom-protocol",
        "--bin",
        "delidev-desktop",
        "--",
        "--data-dir",
        "/private/test",
      ],
    );
    assert.deepEqual(desktopEnvironment(platform, environment), environment);
  }
});

test("desktop child colors follow parent terminals and respect opt-outs", async () => {
  const captureEnvironment = async (overrides, terminal) => {
    const environments = [];
    await runDesktop([], {
      platform: "linux",
      environment: { ...environment, ...overrides },
      stdout: () => {},
      stderr: () => {},
      stdoutIsTTY: terminal,
      stderrIsTTY: false,
      log() {},
      run: async (_command, _args, options) => {
        environments.push(options.env);
        return success;
      },
    });
    return environments;
  };

  const interactive = await captureEnvironment({}, true);
  assert.equal(interactive.length, 2);
  for (const env of interactive) {
    assert.equal(env.CARGO_TERM_COLOR, "always");
    assert.equal(env.FORCE_COLOR, "1");
  }

  const noColor = await captureEnvironment(
    { NO_COLOR: "", CARGO_TERM_COLOR: "always", FORCE_COLOR: "1" },
    true,
  );
  for (const env of noColor) {
    assert.equal(env.CARGO_TERM_COLOR, "never");
    assert.equal(env.FORCE_COLOR, "0");
  }

  const cargoOptOut = desktopColorEnvironment(
    { ...environment, CARGO_TERM_COLOR: "never" },
    { stdoutIsTTY: true },
  );
  assert.equal(cargoOptOut.CARGO_TERM_COLOR, "never");
  assert.equal(cargoOptOut.FORCE_COLOR, "0");
});

test("local macOS preparation and bundling share the configured deployment target and CEF cache without signing credentials", () => {
  const env = desktopEnvironment(
    "darwin",
    {
      ...environment,
      CARGO_TARGET_DIR: "/build output",
      CEF_PATH: "/foreign-cef",
      APPLE_SIGNING_IDENTITY: "private",
      APPLE_CERTIFICATE: "private",
      APPLE_CERTIFICATE_PASSWORD: "private",
      APPLE_ID: "private",
      APPLE_PASSWORD: "private",
      APPLE_TEAM_ID: "private",
      APPLE_API_KEY: "private",
      APPLE_API_ISSUER: "private",
      APPLE_API_KEY_PATH: "private",
      TAURI_SIGNING_PRIVATE_KEY: "private",
      NODE_OPTIONS: "--require private",
      MACOSX_DEPLOYMENT_TARGET: "27.0",
    },
    "/fixture",
  );
  assert.deepEqual(env, {
    PATH: "/tools",
    HOME: "/fixture",
    CARGO_TARGET_DIR: "/build output",
    CEF_PATH: "/fixture/Library/Caches/tauri-cef",
    MACOSX_DEPLOYMENT_TARGET: JSON.parse(
      readFileSync(
        new URL("../src-tauri/tauri.conf.json", import.meta.url),
        "utf8",
      ),
    ).bundle.macOS.minimumSystemVersion,
  });
  assert.equal(
    desktopEnvironment("darwin", { CARGO_TARGET_DIR: "build output" })
      .CARGO_TARGET_DIR,
    fileURLToPath(new URL("../build output", import.meta.url)),
  );
});

test("failed or interrupted preparation never launches the application", async () => {
  for (const result of [
    { code: 7, signal: null },
    { code: null, signal: "SIGINT" },
  ]) {
    let calls = 0;
    assert.deepEqual(
      await runDesktop([], {
        platform: "darwin",
        environment,
        log() {},
        ...macFixtures,
        run: async () => {
          calls++;
          return result;
        },
        creditsFor: () =>
          assert.fail(
            "must not resolve bundle resources after failed preparation",
          ),
      }),
      result,
    );
    assert.equal(calls, 1);
  }
});

test("missing local identity and failed publication never launch or terminate a server", async () => {
  let calls = 0;
  let releases = 0;
  const logs = [];
  const options = {
    platform: "darwin",
    arch: "arm64",
    environment,
    ...macFixtures,
    creditsFor: () => "/cef/CREDITS.html",
    log: (row) => logs.push(row),
    lock: () => () => releases++,
    run: async () => {
      calls++;
      return success;
    },
  };
  await runDesktop([], {
    ...options,
    identityFor: async () => {
      throw new DevelopmentSigningError("development-certificate-missing");
    },
  });
  assert.equal(calls, 0);
  assert.equal(releases, 0);
  await runDesktop([], {
    ...options,
    publish: async () => {
      throw new DevelopmentSigningError("development-signing-command-failed");
    },
  });
  assert.equal(calls, 2);
  assert.equal(releases, 1);
  assert.equal(logs.at(-1).code, "development-signing-command-failed");
});

test("child failures retain status and exception logs omit private argv", async () => {
  const logs = [];
  let calls = 0;
  const failure = { code: 23, signal: null };
  assert.deepEqual(
    await runDesktop([], {
      platform: "linux",
      environment,
      log: (entry) => logs.push(entry),
      run: async () => (++calls === 1 ? success : failure),
    }),
    failure,
  );
  assert.equal(logs.at(-1).stage, "run");
  assert.equal(logs.at(-1).code, 23);
  assert.deepEqual(
    await runDesktop([], {
      platform: "linux",
      environment,
      log: (entry) => logs.push(entry),
      run: async () => {
        throw new Error("spawn /private/user-data --token secret");
      },
    }),
    { code: 1, signal: null },
  );
  assert.equal(logs.at(-1).code, "launch-failed");
  assert.doesNotMatch(JSON.stringify(logs), /user-data|secret/);
});

test(
  "SIGTERM reaches the active child during preparation and execution",
  { skip: process.platform === "win32", timeout: 20_000 },
  async (t) => {
    const root = mkdtempSync(join(tmpdir(), "delidev-launch-test-"));
    t.after(() => rmSync(root, { recursive: true, force: true }));
    const scenarios = ["prepare", "run"].flatMap((stage) => [
      { stage, vanishedProcess: false },
      ...(process.platform === "linux"
        ? [{ stage, vanishedProcess: true }]
        : []),
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
      const wrapper = spawn(
        process.execPath,
        [
          "--input-type=module",
          "-e",
          `
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
    `,
        ],
        { stdio: ["ignore", "pipe", "pipe"] },
      );
      t.after(() => {
        if (wrapper.exitCode === null && wrapper.signalCode === null)
          wrapper.kill("SIGTERM");
      });
      let diagnostics = "";
      wrapper.stderr.on("data", (chunk) => {
        diagnostics += chunk;
      });
      const exited = once(wrapper, "exit");
      // Inherited child stdout can arrive while the wrapper is still returning
      // from spawn, before its signal handlers exist. Wait for both readiness
      // markers so scheduling under load cannot kill the wrapper prematurely.
      let output = "";
      for await (const chunk of wrapper.stdout.iterator({
        destroyOnReturn: false,
      })) {
        output += chunk;
        if (
          output.includes("child-ready\n") &&
          output.includes("wrapper-ready\n")
        )
          break;
      }
      assert.ok(
        output.includes("child-ready\n") && output.includes("wrapper-ready\n"),
      );
      wrapper.kill("SIGTERM");
      const [code, signal] = await exited;
      assert.equal(code, null, diagnostics);
      assert.equal(signal, "SIGTERM");
      assert.equal(readFileSync(receipt, "utf8"), "terminated");
      if (vanishedProcess)
        assert.match(diagnostics, /vanished-process-observed/u);
    }
  },
);

test("private argv spellings are removed while safe diagnostics remain", () => {
  const value = '/private/한글 folder/quote"and\\slash';
  const filter = desktopDiagnosticFilter([
    "--data-dir",
    value,
    "--token=fixture-secret",
  ]);
  for (const spelling of [
    value,
    JSON.stringify(value).slice(1, -1),
    value.replace(/[^a-zA-Z0-9_./:-]/g, "\\$&"),
  ]) {
    assert.equal(
      filter(`failure at ${spelling}`),
      "failure at [private argument]",
    );
  }
  assert.equal(filter("safe compiler warning"), "safe compiler warning");
  assert.equal(filter("token fixture-secret"), "token [private argument]");
  assert.equal(
    filter("\x1b[32mRunning\x1b[0m `cargo private arguments`"),
    "[desktop diagnostic line omitted]",
  );
  assert.equal(
    filter("error: process didn't exit successfully: private command"),
    "[desktop diagnostic line omitted]",
  );
  const multiline = desktopDiagnosticFilter(["private-first\nprivate-second"]);
  assert.equal(multiline("private-first"), "[private argument]");
  assert.equal(multiline("private-second"), "[private argument]");
});

test("captured diagnostics join split UTF-8 and keep bounded lines private", async () => {
  const stdout = new PassThrough(),
    stderr = new PassThrough();
  const out = [],
    err = [],
    incomplete = [];
  const secret = "/private/한글 fixture";
  const drain = captureDesktopDiagnostics([secret], {
    stdout: (value) => out.push(value),
    stderr: (value) => err.push(value),
    incomplete: (code) => incomplete.push(code),
  })({ stdout, stderr });
  const bytes = Buffer.from(`safe\r\n${secret}\n`);
  for (const byte of bytes) stdout.write(Buffer.from([byte]));
  stderr.write("x".repeat(70_000));
  stderr.write(`${secret}\nsafe error\n`);
  stdout.end();
  stderr.end();
  await drain();
  assert.equal(out.join(""), "safe\n[private argument]\n");
  assert.equal(err.join(""), "[desktop diagnostic line omitted]\nsafe error\n");
  assert.deepEqual(incomplete, []);
});

test("captured diagnostics pause child output until the destination drains", async () => {
  const stdout = new PassThrough();
  const incomplete = [];
  const chunks = [];
  let maxBuffered = 0;
  const destination = new Writable({
    highWaterMark: 1,
    write(chunk, _encoding, callback) {
      chunks.push(chunk.toString());
      queueMicrotask(callback);
    },
  });
  const write = destination.write.bind(destination);
  destination.write = (...args) => {
    const accepted = write(...args);
    maxBuffered = Math.max(maxBuffered, destination.writableLength);
    return accepted;
  };
  const expected = Array.from(
    { length: 100 },
    (_, index) => `line ${index}\n`,
  ).join("");
  const drain = captureDesktopDiagnostics([], {
    stdout: destination,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout, stderr: null });
  stdout.end(expected);
  await drain();
  destination.end();
  await once(destination, "finish");
  assert.equal(chunks.join(""), expected);
  assert.ok(maxBuffered < expected.length / 2);
  assert.deepEqual(incomplete, []);
});

test("diagnostic drain bounds descendant pipes and discards incomplete private text", async () => {
  const stdout = new PassThrough(),
    stderr = new PassThrough();
  const out = [],
    incomplete = [];
  const drain = captureDesktopDiagnostics(["/private/long-argument"], {
    stdout: (value) => out.push(value),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout, stderr });
  stdout.write("/private/long");
  stderr.end();
  await drain();
  await drain();
  assert.deepEqual(out, []);
  assert.deepEqual(incomplete, ["output-drain-timeout"]);
  assert.equal(stdout.destroyed, true);
});

test("launcher captures real child diagnostics without changing the original exit code", async () => {
  const secret = "/private/fixture scope";
  const out = [],
    err = [],
    logs = [];
  let calls = 0;
  const result = await runDesktop(["--data-dir", secret], {
    platform: "linux",
    environment,
    stdout: (value) => out.push(value),
    stderr: (value) => err.push(value),
    log: (entry) => logs.push(entry),
    run: (_command, _args, options, lifecycle) => {
      const code = ++calls === 1 ? 0 : 23;
      const diagnostic =
        "Running `binary --data-dir " +
        secret +
        "`\nerror reading " +
        secret +
        "\n";
      const source = `process.stdout.write('safe output\\n'); process.stderr.write(${JSON.stringify(diagnostic)}); process.exitCode = ${code};`;
      return spawnDevServer(
        process.execPath,
        ["-e", source],
        options,
        lifecycle,
      );
    },
  });
  assert.deepEqual(result, { code: 23, signal: null });
  assert.equal(calls, 2);
  assert.match(out.join(""), /safe output/);
  assert.match(err.join(""), /error reading \[private argument\]/);
  assert.doesNotMatch(
    JSON.stringify({ out, err, logs }),
    /fixture scope|--data-dir/,
  );
  assert.equal(logs.at(-1).code, 23);
});

test("failed final lock release preserves the child result and reports safe uncertainty", async () => {
  const logs = [];
  const failure = { code: 19, signal: null };
  let calls = 0;
  const result = await runDesktop([], {
    platform: "darwin",
    environment,
    ...macFixtures,
    creditsFor: () => "/fixture/credits",
    log: (entry) => logs.push(entry),
    lock: () => () => {
      throw new Error("/private/lock-path");
    },
    run: async () => (++calls === 1 ? success : failure),
  });
  assert.deepEqual(result, failure);
  assert.deepEqual(logs.at(-1), {
    operation: "desktop_development",
    stage: "bundle",
    state: "cleanup-uncertain",
    code: "build-lock-release-failed",
  });
  assert.doesNotMatch(JSON.stringify(logs), /private|lock-path/);
});

test("failed diagnostic reads report incomplete output without private error text", async () => {
  const stdout = new PassThrough(),
    stderr = new PassThrough();
  const out = [],
    incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: (value) => out.push(value),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout, stderr });
  stdout.destroy(new Error("/private/stream-error"));
  stderr.end();
  await drain();
  assert.deepEqual(out, ["[desktop diagnostic line omitted]\n"]);
  assert.deepEqual(incomplete, ["output-read-failed"]);
});

test(
  "captured output preserves the original child's termination signal",
  { skip: process.platform === "win32" },
  async () => {
    const captureOutput = captureDesktopDiagnostics([], {
      stdout() {},
      stderr() {},
      incomplete() {},
    });
    const result = await spawnDevServer(
      process.execPath,
      [
        "-e",
        "process.stdout.write('safe\\n'); process.kill(process.pid, 'SIGTERM');",
      ],
      { stdio: ["ignore", "pipe", "pipe"], shell: false },
      { captureOutput },
    );
    assert.deepEqual(result, { code: null, signal: "SIGTERM" });
  },
);

const spawnDevServerUrl = new URL(
  "../../../scripts/spawn-dev-server.mjs",
  import.meta.url,
).href;
const diagnosticsUrl = new URL("./desktop-diagnostics.mjs", import.meta.url)
  .href;

// Runs `body` in an isolated Node process so process.exit behavior and
// uncaught errors are observed without affecting the test runner.
async function runIsolated(body) {
  const child = spawn(
    process.execPath,
    [
      "--input-type=module",
      "-e",
      `
      import { PassThrough, Writable } from 'node:stream';
      import fs from 'node:fs';
      import { captureDesktopDiagnostics } from ${JSON.stringify(diagnosticsUrl)};
      import { spawnDevServer, exitLikeChild } from ${JSON.stringify(spawnDevServerUrl)};
      ${body}
    `,
    ],
    { stdio: ["ignore", "pipe", "pipe"] },
  );
  let stdout = "",
    stderr = "";
  child.stdout.on("data", (chunk) => (stdout += chunk));
  child.stderr.on("data", (chunk) => (stderr += chunk));
  const startedAt = Date.now();
  // `exit` can fire before the final stdio bytes are consumed; `close` follows
  // only after both pipes end, so the captured strings are complete.
  const [code, signal] = await once(child, "close");
  return { code, signal, stdout, stderr, elapsed: Date.now() - startedAt };
}

function delayedFileFixture(file, delay) {
  return `
    const file = ${JSON.stringify(file)};
    const incomplete = [];
    const out = new Writable({
      highWaterMark: 65536,
      write(chunk, _encoding, callback) {
        setTimeout(() => { fs.appendFileSync(file, chunk); callback(); }, ${delay});
      },
    });
    const stdout = new PassThrough();
    const finish = captureDesktopDiagnostics([], {
      stdout: out, stderr() {}, incomplete: (code) => incomplete.push(code),
    })({ stdout, stderr: null });
  `;
}

for (const [name, input, expected] of [
  ["a terminated line", "safe tail\n", "safe tail\n"],
  ["an unterminated tail", "safe tail", "safe tail"],
]) {
  test(`captured output waits for a delayed below-watermark write of ${name} before exit`, async (t) => {
    const root = mkdtempSync(join(tmpdir(), "delidev-diagnostics-test-"));
    t.after(() => rmSync(root, { recursive: true, force: true }));
    const file = join(root, "output");
    const result = await runIsolated(`
      ${delayedFileFixture(file, 40)}
      stdout.end(${JSON.stringify(input)});
      await finish();
      fs.writeFileSync(${JSON.stringify(file + ".incomplete")}, incomplete.join(','));
      exitLikeChild({ code: 7, signal: null });
    `);
    assert.equal(result.code, 7, result.stderr);
    assert.equal(readFileSync(file, "utf8"), expected);
    assert.equal(readFileSync(`${file}.incomplete`, "utf8"), "");
  });
}

test("captured output stops waiting for a held write at the bound and keeps the exit code", async (t) => {
  const root = mkdtempSync(join(tmpdir(), "delidev-diagnostics-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const file = join(root, "output");
  const result = await runIsolated(`
    ${delayedFileFixture(file, 10_000)}
    stdout.end('held output\\n');
    await finish();
    fs.writeFileSync(${JSON.stringify(file + ".incomplete")}, incomplete.join(','));
    exitLikeChild({ code: 7, signal: null });
  `);
  assert.equal(result.code, 7, result.stderr);
  assert.ok(result.elapsed < 5_000, `exit took ${result.elapsed}ms`);
  assert.equal(
    readFileSync(`${file}.incomplete`, "utf8"),
    "output-drain-timeout",
  );
});

test("captured output does not complete before a below-watermark write callback", async () => {
  const stdout = new PassThrough();
  const callbacks = [];
  const destination = new Writable({
    highWaterMark: 1024,
    write(_chunk, _encoding, callback) {
      callbacks.push(callback);
    },
  });
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: destination,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout, stderr: null });
  let settled = false;
  const completion = drain().then(() => {
    settled = true;
  });
  stdout.write("first\n");
  stdout.end("tail");
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(destination.writableLength > 0, true);
  assert.equal(settled, false);
  // The newline-terminated line settles first; the tail is still outstanding.
  callbacks.shift()();
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(settled, false);
  while (callbacks.length === 0) await new Promise(setImmediate);
  callbacks.shift()();
  await completion;
  assert.deepEqual(incomplete, []);
  assert.equal(destination.writableEnded, false);
  assert.equal(destination.listenerCount("error"), 0);
});

test("function and synchronous destinations complete without a drain timeout or ending shared streams", async () => {
  const received = [];
  const synchronous = new Writable({
    write(chunk, _encoding, callback) {
      received.push(chunk.toString());
      callback();
    },
  });
  const stdout = new PassThrough(),
    stderr = new PassThrough();
  const functionOutput = [];
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: synchronous,
    stderr: (value) => functionOutput.push(value),
    incomplete: (code) => incomplete.push(code),
  })({ stdout, stderr });
  stdout.end("line\ntail");
  stderr.end("error line\n");
  await drain();
  // Waiting for the drain bound would report output-drain-timeout below.
  assert.deepEqual(received, ["line\n", "tail"]);
  assert.deepEqual(functionOutput, ["error line\n"]);
  assert.deepEqual(incomplete, []);
  assert.equal(synchronous.writableEnded, false);
  assert.equal(synchronous.listenerCount("error"), 0);
});

function failingSink(writes, { delay = 0 } = {}) {
  return new Writable({
    highWaterMark: 65536,
    write(chunk, _encoding, callback) {
      writes.push(chunk.toString());
      const failure = Object.assign(new Error("test downstream closed"), {
        code: "EPIPE",
      });
      if (delay) setTimeout(() => callback(failure), delay);
      else setImmediate(() => callback(failure));
    },
  });
}

for (const target of ["stdout", "stderr"]) {
  test(`a failed ${target} sink is owned, stops forwarding and reports safe incomplete output`, async () => {
    const input = new PassThrough();
    const writes = [];
    const incomplete = [];
    const healthy = [];
    const sink = failingSink(writes);
    const other = target === "stdout" ? "stderr" : "stdout";
    const stream = { stdout: null, stderr: null };
    stream[target] = input;
    const otherInput = new PassThrough();
    stream[other] = otherInput;
    const drain = captureDesktopDiagnostics([], {
      [target]: sink,
      [other]: (value) => healthy.push(value),
      incomplete: (code) => incomplete.push(code),
    })(stream);
    assert.equal(sink.listenerCount("error"), 1);
    input.write("first\n");
    otherInput.end("healthy\n");
    const drained = drain();
    await new Promise((resolve) => sink.once("close", resolve));
    // Later input is discarded: the failed sink receives no further writes and
    // the child pipe is still consumed.
    input.write("second\n");
    input.end("third");
    await drained;
    await new Promise((resolve) => setTimeout(resolve, 30));
    assert.deepEqual(writes, ["first\n"]);
    assert.deepEqual(incomplete, ["output-write-failed"]);
    assert.deepEqual(healthy, ["healthy\n"]);
    assert.equal(input.readableFlowing, true);
    assert.equal(sink.destroyed, true);
    assert.equal(sink.listenerCount("error"), 0);
  });
}

test("a failed diagnostic read waits for the accepted omission marker write", async () => {
  const input = new PassThrough();
  const callbacks = [];
  const destination = new Writable({
    highWaterMark: 1024,
    write(chunk, _encoding, callback) {
      callbacks.push({ chunk: chunk.toString(), callback });
    },
  });
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: destination,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  let settled = false;
  const completion = drain().then(() => {
    settled = true;
  });
  input.destroy(new Error("/private/stream-error"));
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.deepEqual(
    callbacks.map(({ chunk }) => chunk),
    ["[desktop diagnostic line omitted]\n"],
  );
  assert.equal(settled, false);
  callbacks.shift().callback();
  await completion;
  assert.deepEqual(incomplete, ["output-read-failed"]);
  assert.equal(destination.listenerCount("error"), 0);
});

test("a destination error after a failed diagnostic read is still reported", async () => {
  const input = new PassThrough();
  const writes = [];
  const incomplete = [];
  const sink = failingSink(writes, { delay: 20 });
  const drain = captureDesktopDiagnostics([], {
    stdout: sink,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.destroy(new Error("/private/stream-error"));
  await drain();
  assert.deepEqual(writes, ["[desktop diagnostic line omitted]\n"]);
  assert.deepEqual(incomplete, ["output-write-failed"]);
  if (!sink.closed) await new Promise((resolve) => sink.once("close", resolve));
  assert.equal(sink.listenerCount("error"), 0);
});

test("a failed diagnostic read stops waiting for a held marker write at the bound", async () => {
  const input = new PassThrough();
  const callbacks = [];
  const destination = new Writable({
    write(_chunk, _encoding, callback) {
      callbacks.push(callback);
    },
  });
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: destination,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.destroy(new Error("/private/stream-error"));
  await drain();
  assert.deepEqual(incomplete, ["output-drain-timeout"]);
  // The held write still owns the destination error listener.
  assert.equal(destination.listenerCount("error"), 1);
  callbacks.shift()();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(destination.listenerCount("error"), 0);
});

test("a source read error after a sink failure is owned and keeps the classification", async () => {
  const input = new PassThrough();
  const writes = [];
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: failingSink(writes),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.write("early\n");
  const drained = drain();
  await new Promise((resolve) => setTimeout(resolve, 20));
  // The pipe is still being discarded; its late failure must not be uncaught.
  input.destroy(new Error("/private/late-stream-error"));
  await drained;
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.deepEqual(writes, ["early\n"]);
  assert.deepEqual(incomplete, ["output-write-failed"]);
});

test("a failed sink closes a discarded pipe that a descendant keeps open", async () => {
  const input = new PassThrough();
  const writes = [];
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: failingSink(writes),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.write("early\n");
  // The input never ends, like a pipe inherited by a surviving descendant.
  await drain();
  assert.equal(input.destroyed, true);
  assert.deepEqual(writes, ["early\n"]);
  assert.deepEqual(incomplete, ["output-write-failed"]);
});

test("a failed sink does not delay completion when the pipe closes first", async () => {
  const input = new PassThrough();
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: failingSink([]),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.write("early\n");
  setTimeout(() => input.end(), 10);
  await drain();
  assert.equal(input.readableEnded, true);
  assert.deepEqual(incomplete, ["output-write-failed"]);
});

test("a failed sink pipe is closed when another stream times out", async () => {
  const failed = new PassThrough();
  const held = new PassThrough();
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: failingSink([]),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: failed, stderr: held });
  failed.write("early\n");
  held.write("partial");
  await drain();
  assert.equal(failed.destroyed, true);
  assert.equal(held.destroyed, true);
  assert.deepEqual(incomplete, ["output-drain-timeout", "output-write-failed"]);
});

test("a failed sink pipe shares the original drain deadline", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] });
  const flush = () => new Promise((resolve) => setImmediate(resolve));
  const failed = new PassThrough();
  const slow = new PassThrough();
  const incomplete = [];
  let settled = false;
  const completion = captureDesktopDiagnostics([], {
    stdout: failingSink([]),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: failed, stderr: slow })().then(() => (settled = true));
  failed.write("early\n");
  await flush();
  // The other stream settles late in the drain window, leaving 10 ms.
  t.mock.timers.tick(90);
  slow.end();
  await flush();
  assert.equal(settled, false);
  // The failed sink's descendant-held pipe has only the remaining time, not a
  // second full interval.
  t.mock.timers.tick(10);
  await flush();
  assert.equal(settled, true);
  await completion;
  assert.equal(failed.destroyed, true);
  assert.deepEqual(incomplete, ["output-write-failed"]);
});

test("a sink error before input end is classified without an unhandled error", async () => {
  const input = new PassThrough();
  const writes = [];
  const incomplete = [];
  const drain = captureDesktopDiagnostics([], {
    stdout: failingSink(writes),
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.write("early\n");
  await drain();
  assert.deepEqual(incomplete, ["output-write-failed"]);
});

test("a sink error after input end with an outstanding write is owned", async () => {
  const input = new PassThrough();
  const writes = [];
  const incomplete = [];
  const sink = failingSink(writes, { delay: 20 });
  const drain = captureDesktopDiagnostics([], {
    stdout: sink,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.end("tail without newline");
  await drain();
  assert.deepEqual(writes, ["tail without newline"]);
  assert.deepEqual(incomplete, ["output-write-failed"]);
});

test("a sink error after the drain bound is still owned until the write settles", async () => {
  const input = new PassThrough();
  const writes = [];
  const incomplete = [];
  const sink = failingSink(writes, { delay: 160 });
  const drain = captureDesktopDiagnostics([], {
    stdout: sink,
    stderr() {},
    incomplete: (code) => incomplete.push(code),
  })({ stdout: input, stderr: null });
  input.end("late\n");
  await drain();
  assert.deepEqual(incomplete, ["output-drain-timeout"]);
  // The listener must survive until the late write error has been emitted.
  assert.equal(sink.listenerCount("error"), 1);
  await new Promise((resolve) => sink.once("close", resolve));
  assert.deepEqual(incomplete, ["output-drain-timeout"]);
  assert.equal(sink.listenerCount("error"), 0);
});

test("a failed sink does not bypass the managed child lifecycle or exit code", async () => {
  const result = await runIsolated(`
    const sink = new Writable({
      write(_chunk, _encoding, callback) {
        setImmediate(() => callback(Object.assign(new Error('closed'), { code: 'EPIPE' })));
      },
    });
    const incomplete = [];
    const captureOutput = captureDesktopDiagnostics([], {
      stdout: sink, stderr() {}, incomplete: (code) => incomplete.push(code),
    });
    const before = process.listenerCount('SIGTERM');
    let cleaned = false;
    let outcome;
    try {
      outcome = await spawnDevServer(
        process.execPath,
        ['-e', "process.stdout.write('first\\\\n'); setTimeout(() => { process.stdout.write('second\\\\n'); process.exitCode = 5; }, 50);"],
        { stdio: ['ignore', 'pipe', 'pipe'], shell: false },
        { captureOutput },
      );
    } finally {
      cleaned = process.listenerCount('SIGTERM') === before;
      console.log('lifecycle-finally ' + cleaned);
    }
    console.log(JSON.stringify({ outcome, incomplete }));
    exitLikeChild(outcome);
  `);
  assert.equal(result.code, 5, result.stderr);
  assert.match(result.stdout, /lifecycle-finally true/);
  assert.match(
    result.stdout,
    /\{"outcome":\{"code":5,"signal":null\},"incomplete":\["output-write-failed"\]\}/,
  );
  assert.doesNotMatch(result.stderr, /Unhandled 'error'/);
});
