// SPDX-License-Identifier: Apache-2.0
import { mkdir, mkdtemp, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRsbuild } from "@rsbuild/core";
import { pluginReact } from "@rsbuild/plugin-react";
import { Processes, QaError } from "./processes.mjs";
import { Environment } from "./environment.mjs";
import { cleanup } from "./cleanup.mjs";

export const app = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
export const repository = resolve(app, "../..");
export function workersArgument(args) {
  const values = args.filter((value, index) => index !== 0 || value !== "--");
  if (!values.length) return 4;
  if (values.length !== 2 || values[0] !== "--workers" || !/^[1-8]$/.test(values[1])) throw new QaError("workers-must-be-1-through-8");
  return Number(values[1]);
}

export class QaRun {
  processes = new Processes(); environments = []; stopping = false; records = [];
  constructor({ workers = 4, log = value => console.log(JSON.stringify(value)) } = {}) {
    if (!Number.isInteger(workers) || workers < 1 || workers > 8) throw new QaError("workers-must-be-1-through-8");
    Object.assign(this, { workers, log });
  }
  async start() {
    this.root = await realpath(await mkdtemp(join(tmpdir(), "delidev-qa-state-")));
    this.artifacts = await realpath(await mkdtemp(join(tmpdir(), "delidev-qa-records-")));
    this.assets = join(this.root, "frontend"); this.binary = join(this.root, process.platform === "win32" ? "delidev.exe" : "delidev");
    await mkdir(join(this.artifacts, "screenshots"), { mode: 0o700 });
    const git = args => this.processes.run("git", args, { cwd: repository });
    this.source = { revision: (await git(["rev-parse", "HEAD"])).trim(), dirty: Boolean((await git(["status", "--porcelain"])).trim()) };
    this.log({ operation: "qa-build", source: this.source });
    await this.processes.run("pnpm", ["--filter", "@delinoio/delidev-api-client", "build"], { cwd: repository, timeout: 120_000 });
    this.checkPreparing();
    await this.processes.run("go", ["build", "-o", this.binary, "./cmds/delidev-cli"], { cwd: repository, timeout: 180_000 });
    this.checkPreparing();
    const build = await createRsbuild({ cwd: app, rsbuildConfig: {
      logLevel: "error", performance: { printFileSize: false },
      plugins: [pluginReact()], source: { entry: { index: join(app, "src/qa/main.tsx") } },
      html: { template: join(app, "index.html"), title: "DeliDev browser QA" },
      output: { distPath: { root: this.assets }, assetPrefix: "/", sourceMap: false, cleanDistPath: true },
    } });
    await build.build(); this.checkPreparing();
    this.api = await import(pathToFileURL(join(repository, "packages/delidev-api-client/dist/index.js")).href);
    for (let index = 1; index <= this.workers; index++) {
      this.checkPreparing();
      const environment = new Environment({ root: join(this.root, `worker-${index}`), assets: this.assets, binary: this.binary, api: this.api, index, changed: () => {
        if (this.initialized) {
          const value = this.manifest(); this.log(value);
          this.recording = (this.recording ?? Promise.resolve()).then(() => this.record(value));
          this.recording.catch(() => {});
        }
      } });
      this.environments.push(environment);
      await environment.prepare();
    }
    this.checkPreparing(); this.initialized = true;
    await this.record(this.manifest()); this.log(this.manifest());
    return this;
  }
  checkPreparing() { if (this.stopping) throw new QaError("qa-interrupted"); }
  interrupt() { this.stopping = true; for (const environment of this.environments) environment.closing = true; return this.processes.close(); }
  manifest() { return { operation: "qa-environments", version: 1, command: `pnpm --filter delidev-desktop dev:qa -- --workers ${this.workers}`, source: this.source, artifacts: this.artifacts, environments: this.environments.map(environment => environment.manifest()) }; }
  async record(value) {
    if (this.artifacts) await writeFile(join(this.artifacts, "run.json"), JSON.stringify(value, null, 2), { mode: 0o600 });
  }
  close() {
    // Keep one original cleanup, including its uncertainty, across repeated signals.
    this.closing ??= this.finish(); return this.closing;
  }
  async finish() {
    this.stopping = true;
    const results = [];
    for (const environment of this.environments) results.push(await cleanup(environment));
    try { await this.processes.close(); } catch { results.push({ state: "preserved", path: this.root, reasons: ["preparation-process-exit-unconfirmed"] }); }
    if (this.root && results.every(result => result.state === "deleted")) await rm(this.root, { recursive: true, force: true });
    const record = { operation: "qa-cleanup", source: this.source, artifacts: this.artifacts, environments: results };
    await this.recording?.catch(() => {});
    await this.record(record); this.log(record); return record;
  }
}

async function main() {
  const run = new QaRun({ workers: workersArgument(process.argv.slice(2)) });
  let wake; const stopped = new Promise(resolve => { wake = resolve; });
  const signal = () => { void run.interrupt().catch(() => {}); wake(); };
  process.on("SIGINT", signal); process.on("SIGTERM", signal);
  let failed = false;
  try { await run.start(); await stopped; }
  catch (error) { failed = !run.stopping; run.log({ operation: "qa-start", result: "failed", code: error instanceof QaError ? error.code : "qa-start-failed" }); }
  finally {
    const result = await run.close();
    process.off("SIGINT", signal); process.off("SIGTERM", signal);
    process.exitCode = failed || result.environments.some(environment => environment.state === "preserved") ? 1 : 0;
  }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch(error => { console.error(JSON.stringify({ operation: "qa-run", code: error instanceof QaError ? error.code : "qa-run-failed" })); process.exitCode = 1; });
}
