import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { existsSync } from "node:fs";
import {
  mkdir,
  mkdtemp,
  readFile,
  rm,
  symlink,
  writeFile,
} from "node:fs/promises";
import { createRequire } from "node:module";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const require = createRequire(import.meta.url);
const appPath = "apps/async-commit-hook";
const controlPath = "control";

// The fixture-owned receipt is the proof that the selected stand-in stage threw.
// Turbo does not always forward the exception text, so console output is kept
// only as an assertion diagnostic and never as the failure evidence.
function verifyInjectedFailure(stage, result, receipts) {
  assert.notEqual(result.code, 0, result.output);
  assert.deepEqual(receipts, [stage], result.output);
}

test("injected failure verification relies on receipts, not console text", () => {
  assert.doesNotThrow(() =>
    verifyInjectedFailure("native", { code: 1, output: "task failed" }, [
      "native",
    ]),
  );
  assert.throws(() =>
    verifyInjectedFailure(
      "native",
      { code: 1, output: "injected native failure" },
      [],
    ),
  );
  assert.throws(() =>
    verifyInjectedFailure("frontend", { code: 1, output: "" }, ["validator"]),
  );
  assert.throws(() =>
    verifyInjectedFailure("validator", { code: 0, output: "" }, ["validator"]),
  );
});

test(
  "async-commit-hook aggregate serializes frontend readers and both rebuilds",
  { timeout: 300_000 },
  async (t) => {
    const cwd = await mkdtemp(join(tmpdir(), "ach-ci-order-"));
    const running = [];
    t.after(async () => {
      for (const marker of ["release-contracts", "release-embedded"])
        await writeFile(join(cwd, controlPath, marker), "").catch(() => {});
      await Promise.allSettled(running);
      await rm(cwd, { recursive: true, force: true });
    });
    const write = async (path, content) => {
      const destination = join(cwd, path);
      await mkdir(dirname(destination), { recursive: true });
      await writeFile(destination, content);
    };
    const copy = async (path) => write(path, await readFile(join(root, path)));
    for (const path of [
      "turbo.json",
      ".nvmrc",
      "rust-toolchain",
      "go.mod",
      "go.sum",
      `${appPath}/turbo.json`,
      `${appPath}/scripts/embed.mjs`,
      `${appPath}/scripts/build.test.mjs`,
      "scripts/ci/turbo.json",
    ])
      await copy(path);
    const packageManager = JSON.parse(
      await readFile(join(root, "package.json"), "utf8"),
    ).packageManager;
    await write(
      "package.json",
      JSON.stringify({
        name: "ach-order-fixture",
        private: true,
        packageManager,
      }),
    );
    await write(
      "pnpm-workspace.yaml",
      "packages:\n  - apps/*\n  - packages/*\n  - scripts/ci\n",
    );
    await write(
      "pnpm-lock.yaml",
      "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  apps/async-commit-hook: {}\n  apps/public-docs: {}\n  packages/async-commit-hook-api-client: {}\n  scripts/ci: {}\n",
    );
    await write(".gitignore", "node_modules\n.turbo\ncontrol\n**/dist\n");
    const appManifest = JSON.parse(
      await readFile(join(root, appPath, "package.json"), "utf8"),
    );
    appManifest.scripts = {
      "build:frontend": "node fixture-frontend.mjs",
      "build:embedded": "node fixture-embedded.mjs",
      "ci:embedded": appManifest.scripts["ci:embedded"],
      "ci:contracts": "node fixture-contracts.mjs",
      typecheck: "node fixture-noop.mjs",
      "test:unit": "node fixture-noop.mjs",
    };
    appManifest.dependencies = {
      "@delinoio/async-commit-hook-api-client": "workspace:*",
    };
    appManifest.devDependencies = {};
    await write(`${appPath}/package.json`, JSON.stringify(appManifest));
    await write(
      "apps/public-docs/package.json",
      JSON.stringify({
        name: "public-docs",
        private: true,
        scripts: { "ci:check": 'node -e "process.exit(0)"' },
      }),
    );
    await write(
      "packages/async-commit-hook-api-client/package.json",
      JSON.stringify({
        name: "@delinoio/async-commit-hook-api-client",
        private: true,
        scripts: {
          build: 'node -e "process.exit(0)"',
          test: 'node -e "process.exit(0)"',
        },
      }),
    );
    const ciManifest = JSON.parse(
      await readFile(join(root, "scripts/ci/package.json"), "utf8"),
    );
    ciManifest.scripts = Object.fromEntries(
      Object.keys(ciManifest.scripts)
        .filter(
          (name) => name.startsWith("ci:ach:") || name.startsWith("ci:proto:"),
        )
        .map((name) => [name, `node fixture-ci.mjs ${name}`]),
    );
    await write("scripts/ci/package.json", JSON.stringify(ciManifest));
    await write(
      `${appPath}/fixture-control.mjs`,
      `import { appendFile, readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
export const control = fileURLToPath(new URL("../../control/", import.meta.url));
export const state = JSON.parse(await readFile(control + "state.json", "utf8"));
export const event = (name) => appendFile(control + "events", name + "\\n");
export async function fault(stage) {
  await appendFile(control + "failures", stage + "\\n");
  throw new Error("injected " + stage + " failure");
}
export async function hold(marker) {
  const deadline = Date.now() + 60_000;
  while (!existsSync(control + marker)) {
    if (Date.now() > deadline) throw new Error("timed out waiting for " + marker);
    await delay(10);
  }
}
`,
    );
    await write(
      `${appPath}/fixture-frontend.mjs`,
      `import { mkdir, writeFile } from "node:fs/promises";
import { event, fault, state } from "./fixture-control.mjs";
await event("frontend:start");
if (state.fail === "frontend") await fault("frontend");
await mkdir("dist/assets", { recursive: true });
await writeFile("dist/index.html", '<html lang="en"><script src="/assets/app.12345678.js"></script><link rel="stylesheet" href="/assets/app.12345678.css"></html>');
await writeFile("dist/assets/app.12345678.js", "export {};\\n");
await writeFile("dist/assets/app.12345678.css", "body {}\\n");
await event("frontend:done");
`,
    );
    await write(
      `${appPath}/fixture-embedded.mjs`,
      `import { rm } from "node:fs/promises";
import { control, event, hold } from "./fixture-control.mjs";
await event("embedded:start");
await rm("dist", { recursive: true, force: true });
await event("embedded:cleaned");
if ((await import("node:fs")).existsSync(control + "pause-embedded")) await hold("release-embedded");
await event("embedded:done");
`,
    );
    await write(
      `${appPath}/fixture-contracts.mjs`,
      `import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { control, event, fault, hold, state } from "./fixture-control.mjs";
await event("contracts:start");
if (existsSync(control + "pause-contracts")) await hold("release-contracts");
if (state.fail === "validator") await fault("validator");
const result = spawnSync(process.execPath, ["--test", "scripts/build.test.mjs"], { stdio: "inherit" });
if (result.error) throw result.error;
if (result.status !== 0) process.exit(result.status ?? 1);
await event("contracts:done");
`,
    );
    await write(`${appPath}/fixture-noop.mjs`, "process.exit(0);\n");
    await write(
      "scripts/ci/fixture-ci.mjs",
      `import { appendFile, readFile, rm } from "node:fs/promises";
const task = process.argv[2];
const control = new URL("../../control/", import.meta.url);
const state = JSON.parse(await readFile(new URL("state.json", control), "utf8"));
const event = (name) => appendFile(new URL("events", control), name + "\\n");
if (task === "ci:ach:native") {
  await event("native:start");
  if (state.fail === "native") {
    await appendFile(new URL("failures", control), "native\\n");
    throw new Error("injected native failure");
  }
  await event("native:done");
} else if (task === "ci:ach:package") {
  await event("package:start");
  await rm(new URL("../../apps/async-commit-hook/dist/", import.meta.url), { recursive: true, force: true });
  await event("package:done");
}
`,
    );
    // The fixture keeps the production Turbo graphs and actual bundle reader.
    // Only unrelated commands and the two output producers are bounded stand-ins.
    await symlink(join(root, "node_modules"), join(cwd, "node_modules"));
    const app = join(cwd, appPath);
    const control = join(cwd, controlPath);
    const prepare = async (state, pause = "") => {
      await rm(control, { recursive: true, force: true });
      await mkdir(control);
      await writeFile(join(control, "state.json"), JSON.stringify(state));
      await writeFile(join(control, "events"), "");
      if (pause) await writeFile(join(control, `pause-${pause}`), "");
      await rm(join(app, "dist"), { recursive: true, force: true });
    };
    const events = async () =>
      (await readFile(join(control, "events"), "utf8"))
        .trim()
        .split("\n")
        .filter(Boolean);
    const receipts = async () =>
      (await readFile(join(control, "failures"), "utf8").catch(() => ""))
        .trim()
        .split("\n")
        .filter(Boolean);
    const run = (readCache = true) => {
      const env = Object.fromEntries(
        [
          "PATH",
          "HOME",
          "USERPROFILE",
          "SystemRoot",
          "COMSPEC",
          "PATHEXT",
          "TMPDIR",
          "TEMP",
          "TMP",
        ]
          .filter((key) => process.env[key] !== undefined)
          .map((key) => [key, process.env[key]]),
      );
      env.PATH = `${dirname(process.execPath)}${delimiter}${env.PATH ?? ""}`;
      const child = spawn(
        process.execPath,
        [
          require.resolve("turbo/bin/turbo"),
          "run",
          "ci:ach:check",
          "--filter=@delinoio/ci",
          `--cache=local:${readCache ? "rw" : "w"}`,
          "--concurrency=10",
        ],
        {
          cwd,
          env: { ...env, TURBO_TELEMETRY_DISABLED: "1" },
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      let output = "";
      for (const stream of [child.stdout, child.stderr])
        stream.on("data", (data) => {
          output += data;
        });
      const finished = new Promise((resolve, reject) => {
        child.once("error", reject);
        child.once("close", (code) => resolve({ code, output }));
      });
      running.push(finished);
      return { child, finished, output: () => output };
    };
    const waitFor = async (name, execution) => {
      const deadline = Date.now() + 60_000;
      while (!(await events()).includes(name)) {
        assert.equal(execution.child.exitCode, null, execution.output());
        assert.ok(Date.now() < deadline, execution.output());
        await delay(10);
      }
    };
    for (const warm of [false, true]) {
      await prepare({}, "embedded");
      const execution = run();
      await waitFor("embedded:cleaned", execution);
      await delay(100);
      const observed = await events();
      assert.ok(observed.includes("contracts:done"), execution.output());
      assert.ok(
        observed.indexOf("contracts:done") < observed.indexOf("embedded:start"),
      );
      assert.ok(!observed.includes("native:start"));
      assert.ok(!observed.includes("package:start"));
      assert.equal(existsSync(join(app, "dist")), false);
      await writeFile(join(control, "release-embedded"), "");
      const result = await execution.finished;
      assert.equal(result.code, 0, result.output);
      const completed = await events();
      assert.ok(
        completed.indexOf("embedded:done") < completed.indexOf("native:start"),
      );
      assert.ok(
        completed.indexOf("native:done") < completed.indexOf("package:start"),
      );
      assert.ok(warm || completed.includes("frontend:done"));
      if (warm) assert.ok(!completed.includes("frontend:start"), result.output);
    }
    await prepare({}, "contracts");
    const held = run();
    await waitFor("contracts:start", held);
    await delay(100);
    assert.ok(!(await events()).includes("embedded:start"), held.output());
    assert.ok(!(await events()).includes("package:start"), held.output());
    await writeFile(join(control, "release-contracts"), "");
    assert.equal((await held.finished).code, 0, held.output());

    for (const fail of ["frontend", "validator", "native"]) {
      await prepare({ fail });
      const result = await run(false).finished;
      assert.notEqual(result.code, 0, result.output);
      verifyInjectedFailure(fail, result, await receipts());
      const observed = await events();
      assert.ok(!observed.includes("package:start"), result.output);
      if (fail !== "native") assert.ok(!observed.includes("embedded:start"));
    }
  },
);
