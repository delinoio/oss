import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createRequire } from "node:module";
import { cp, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const require = createRequire(import.meta.url);
const packagePath = "apps/devhud-chrome-extension";
const archiveNames = ["devhud-chrome-web-store.zip", "devhud-chrome-github-validation.zip"];

test("extension CI blocks final build/cache restoration and ZIP checks until package regressions finish", { timeout: 120_000 }, async (t) => {
  const cwd = await mkdtemp(join(tmpdir(), "extension-ci-order-"));
  const executions = [];
  t.after(async () => {
    // Release a paused packer before joining Turbo and deleting its workspace.
    if (existsSync(join(cwd, "control"))) await writeFile(join(cwd, "control/release"), "");
    await Promise.allSettled(executions);
    await rm(cwd, { recursive: true, force: true });
  });
  const app = join(cwd, packagePath);
  const control = join(cwd, "control");
  const write = async (path, value) => {
    await mkdir(dirname(join(cwd, path)), { recursive: true });
    await writeFile(join(cwd, path), value);
  };
  const git = (...args) => execFileSync("git", args, { cwd, stdio: ["ignore", "pipe", "pipe"] });
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  const workspace = JSON.parse(await readFile(join(root, "package.json"), "utf8"));
  await write("package.json", JSON.stringify({ name: "fixture", private: true, packageManager: workspace.packageManager }));
  await write("pnpm-workspace.yaml", "packages:\n  - apps/*\n");
  await write("pnpm-lock.yaml", "lockfileVersion: '9.0'\nimporters:\n  .: {}\n  apps/devhud-chrome-extension: {}\n");
  await write(".gitignore", "node_modules\n.turbo\ncontrol\n**/build\n**/dist\n**/artifacts\n");
  for (const path of ["turbo.json", ".nvmrc", "rust-toolchain", "go.mod", "go.sum", `${packagePath}/turbo.json`, "apps/devhud/src-tauri/icons/icon.png"]) {
    await write(path, await readFile(join(root, path)));
  }
  for (const path of ["scripts", "src", "public"]) {
    await cp(join(root, packagePath, path), join(app, path), { recursive: true });
  }
  // Use the production graph, packer, time-zone regression and ZIP verifier.
  // Replace compilation and unrelated checks with bounded fixture inputs.
  const manifest = JSON.parse(await readFile(join(root, packagePath, "package.json"), "utf8"));
  manifest.scripts.build = "node fixture-build.mjs";
  manifest.scripts["test:package"] = "node fixture-package.mjs";
  manifest.scripts["ci:zip"] = "node fixture-zip.mjs";
  for (const name of ["typecheck", "test:unit"]) manifest.scripts[name] = "node -e \"process.exit(0)\"";
  await write(`${packagePath}/package.json`, JSON.stringify(manifest));
  await write(`${packagePath}/fixture-control.mjs`, `
import { appendFile, readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
export const control = fileURLToPath(new URL("../../control/", import.meta.url));
export const state = JSON.parse(await readFile(control + "state.json", "utf8"));
export const event = (value) => appendFile(control + "events", value + "\\n");
`);
  await write(`${packagePath}/fixture-build.mjs`, `
import { existsSync } from "node:fs";
import fs from "node:fs/promises";
import { syncBuiltinESMExports } from "node:module";
import { resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { control, event, state } from "./fixture-control.mjs";
const timezone = process.env.TZ;
const phase = timezone === "America/Los_Angeles" ? "package:first" : timezone === "Asia/Seoul" ? "package:second" : "final";
await event(phase + ":start");
const originalRm = fs.rm;
fs.rm = async (path, options) => {
  await originalRm(path, options);
  if (resolve(path) !== resolve("artifacts")) return;
  await event(phase + ":removed");
  if (phase === "package:first" && state.pause) {
    await fs.writeFile(control + "paused", "");
    const deadline = Date.now() + 10_000;
    while (!existsSync(control + "release")) {
      if (Date.now() > deadline) throw new Error("fixture release timed out");
      await delay(10);
    }
  }
  if ((phase === "package:first" && state.fail === "package-builder") || (phase === "final" && state.fail === "final-builder")) {
    throw new Error("injected " + state.fail + " failure");
  }
};
syncBuiltinESMExports();
await fs.rm("build", { recursive: true, force: true });
await fs.mkdir("build", { recursive: true });
for (const name of ["service-worker", "popup"]) await fs.writeFile("build/" + name + ".js", "export {};\\n");
if (phase === "package:second" && state.fail === "reproducibility") await fs.writeFile("build/drift.js", "export {};\\n");
await import("./scripts/build.mjs");
// Only the final build owns this cache-restoration witness. It is excluded
// from the archive and erased by each subsequent package-test clean build.
if (phase === "final") await fs.writeFile("build/final-only", "cached final build\\n");
else if (existsSync("build/final-only")) throw new Error("final output restored during package regression");
await event(phase + ":done");
`);
  await write(`${packagePath}/fixture-package.mjs`, `
import { spawnSync } from "node:child_process";
import { event } from "./fixture-control.mjs";
await event("package:start");
const result = spawnSync(process.execPath, ["--test", "scripts/policy.test.mjs", "scripts/deterministic-build.test.mjs"], { stdio: "inherit", env: process.env });
if (result.error) throw result.error;
if (result.status === 0) await event("package:done");
process.exitCode = result.status ?? 1;
`);
  await write(`${packagePath}/fixture-zip.mjs`, `
import { event } from "./fixture-control.mjs";
await event("zip:start");
await import("./scripts/verify-ci-zips.mjs");
await event("zip:done");
`);
  git("add", "--all");
  git("commit", "-m", "fixture");
  for (const path of ["node_modules", `${packagePath}/node_modules`]) {
    await symlink(join(root, path), join(cwd, path), process.platform === "win32" ? "junction" : "dir");
  }
  const events = async () => (await readFile(join(control, "events"), "utf8")).trim().split("\n");
  const prepare = async (state) => {
    await rm(control, { recursive: true, force: true });
    await mkdir(control);
    await writeFile(join(control, "state.json"), JSON.stringify(state));
    await writeFile(join(control, "events"), "");
  };
  const run = (readCache = true) => {
    const env = Object.fromEntries(["PATH", "HOME", "USERPROFILE", "SystemRoot", "COMSPEC", "PATHEXT", "TMPDIR", "TEMP", "TMP"].filter(key => process.env[key] !== undefined).map(key => [key, process.env[key]]));
    env.PATH = `${dirname(process.execPath)}${delimiter}${env.PATH ?? ""}`;
    const child = spawn(process.execPath, [require.resolve("turbo/bin/turbo"), "run", "ci:check", "--filter=devhud-chrome-extension", `--cache=local:${readCache ? "rw" : "w"}`, "--summarize", "--concurrency=6"], {
      cwd, env: { ...env, CI: "true", TURBO_TELEMETRY_DISABLED: "1" }, stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    for (const stream of [child.stdout, child.stderr]) stream.on("data", data => { output += data; });
    const finished = new Promise((resolve, reject) => {
      child.once("error", reject);
      child.once("close", code => resolve({ code, output }));
    });
    executions.push(finished);
    return { child, finished, output: () => output };
  };
  const archives = () => Promise.all(archiveNames.map(name => readFile(join(app, "artifacts", name))));
  const coldEvents = ["package:start", "package:first:start", "package:first:removed", "package:first:done", "package:second:start", "package:second:removed", "package:second:done", "package:done", "final:start", "final:removed", "final:done", "zip:start", "zip:done"];
  let expectedArchive;
  for (const warm of [false, true]) {
    await prepare({ pause: true });
    for (const path of ["dist", "build", "artifacts"]) await rm(join(app, path), { recursive: true, force: true });
    const execution = run();
    const deadline = Date.now() + 10_000;
    while (!existsSync(join(control, "paused"))) {
      assert.equal(execution.child.exitCode, null, execution.output());
      assert.ok(Date.now() < deadline, execution.output());
      await delay(10);
    }
    // Hold the destructive interval open while Turbo can schedule siblings.
    await delay(200);
    assert.equal(execution.child.exitCode, null, execution.output());
    assert.deepEqual(await events(), coldEvents.slice(0, 3));
    assert.equal(existsSync(join(app, "artifacts")), false);
    assert.equal(existsSync(join(app, "build/final-only")), false);
    await writeFile(join(control, "release"), "");
    const result = await execution.finished;
    assert.equal(result.code, 0, result.output);
    assert.deepEqual(await events(), warm ? coldEvents.slice(0, 8) : coldEvents);
    assert.equal(await readFile(join(app, "build/final-only"), "utf8"), "cached final build\n");
    const [store, validation] = await archives();
    assert.deepEqual(store, validation);
    const summaries = await readdir(join(cwd, ".turbo/runs"));
    const summary = JSON.parse(await readFile(join(cwd, ".turbo/runs", summaries.sort().at(-1)), "utf8"));
    const tasks = Object.fromEntries(summary.tasks.map(task => [task.task, task]));
    // Run-summary wall-clock timestamps can overlap even when the graph and
    // observed operations are ordered, especially for restored cached tasks.
    // The paused destructive interval and exact event sequence above prove the
    // live order; also verify the scheduler's explicit dependency edges.
    assert.ok(tasks["build:test"].dependencies.includes("devhud-chrome-extension#test:package"));
    assert.ok(tasks["ci:zip"].dependencies.includes("devhud-chrome-extension#build:test"));
    if (warm) {
      assert.deepEqual(store, expectedArchive);
      for (const name of ["build:test", "ci:zip"]) assert.equal(tasks[name].cache.status, "HIT");
    } else expectedArchive = store;
    t.diagnostic(`${warm ? "Warm" : "Cold"} run: artifact deletion blocked final build/restoration and ZIP validation; archive bytes matched`);
  }
  for (const fail of ["package-builder", "reproducibility", "final-builder"]) {
    await prepare({ fail });
    const result = await run(fail !== "final-builder").finished;
    assert.notEqual(result.code, 0, result.output);
    const observed = await events();
    const completedEvents = { "package-builder": 3, reproducibility: 7, "final-builder": 10 };
    assert.deepEqual(observed, coldEvents.slice(0, completedEvents[fail]), result.output);
    assert.match(result.output, fail === "reproducibility" ? /ERR_ASSERTION|strictly deep-equal/u : new RegExp(`injected ${fail} failure`, "u"));
    t.diagnostic(`${fail} failure: aggregate failed before ZIP validation`);
  }
});
