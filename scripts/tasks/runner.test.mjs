import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { developmentTaskArguments, rootChildEnvironment } from "../dev-environment/orchestrator.mjs";
import { terminateTree } from "../dev-environment/process.mjs";

const repositoryRoot = fileURLToPath(new URL("../..", import.meta.url));
const runner = join(repositoryRoot, "node_modules/vite-plus/bin/vp");

async function fixture(t, tasks, beforeCleanup = async () => {}) {
  const root = await mkdtemp(join(tmpdir(), "oss-vite-task-"));
  t.after(async () => {
    await beforeCleanup();
    await rm(root, { recursive: true, force: true });
  });
  await writeFile(join(root, "package.json"), JSON.stringify({ name: "fixture", private: true, packageManager: "pnpm@10.26.2" }));
  await writeFile(join(root, "pnpm-workspace.yaml"), "packages:\n  - packages/*\n");
  await config(root, tasks);
  return root;
}

async function config(root, tasks) {
  await writeFile(join(root, "vite.config.ts"), `export default ${JSON.stringify({ run: { tasks } })};\n`);
}

function run(root, args, env = process.env) {
  const child = spawn(process.execPath, [runner, "run", ...args], {
    cwd: root,
    env: { ...env, PATH: `${join(repositoryRoot, "node_modules/.bin")}${delimiter}${env.PATH ?? env.Path ?? ""}` },
    detached: process.platform !== "win32",
    stdio: ["ignore", "pipe", "pipe"],
  });
  let output = "";
  child.stdout.on("data", (chunk) => { output += chunk; });
  child.stderr.on("data", (chunk) => { output += chunk; });
  const completion = new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("close", (code, signal) => resolve({ code, signal, output }));
  });
  return { child, completion };
}

async function successful(root, args) {
  const result = await run(root, args).completion;
  assert.equal(result.code, 0, result.output);
  return result;
}

test("real runner orders dependencies, restores outputs, and invalidates file and environment inputs", { timeout: 30_000 }, async (t) => {
  const root = await fixture(t, {
    build: { command: "node build.mjs", cache: true, input: ["input.txt", "build.mjs"], output: ["dist/**"], env: ["FIXTURE_VARIANT"] },
    check: { command: "node check.mjs", cache: false, dependsOn: ["build"] },
    fail: { command: "node -e 'process.exit(7)'", cache: false },
    blocked: { command: "node -e 'process.exit(99)'", cache: false, dependsOn: ["fail"] },
  });
  await writeFile(join(root, "input.txt"), "first");
  // Multi-stage builds read their generated files and list their parent directory.
  // Explicit inputs must still permit restoration after the whole output is removed.
  await writeFile(join(root, "build.mjs"), `import {readFileSync,writeFileSync,mkdirSync,appendFileSync,readdirSync} from 'node:fs'; readdirSync('.'); mkdirSync('dist',{recursive:true}); writeFileSync('dist/result',readFileSync('input.txt','utf8')+(process.env.FIXTURE_VARIANT??'')); readFileSync('dist/result'); readdirSync('dist'); appendFileSync('executions','build\\n');`);
  await writeFile(join(root, "check.mjs"), `import {readFileSync} from 'node:fs'; if(!readFileSync('dist/result','utf8'))process.exit(1);`);
  await successful(root, ["check"]);
  await rm(join(root, "dist"), { recursive: true });
  await successful(root, ["check"]);
  assert.equal(await readFile(join(root, "dist/result"), "utf8"), "first");
  assert.equal(await readFile(join(root, "executions"), "utf8"), "build\n");
  await writeFile(join(root, "input.txt"), "second");
  await successful(root, ["check"]);
  const variant = await run(root, ["build"], { ...process.env, FIXTURE_VARIANT: "-variant" }).completion;
  assert.equal(variant.code, 0, variant.output);
  assert.equal(await readFile(join(root, "dist/result"), "utf8"), "second-variant");
  assert.equal((await readFile(join(root, "executions"), "utf8")).trim().split("\n").length, 3);
  await successful(root, ["--no-cache", "build"]);
  assert.equal((await readFile(join(root, "executions"), "utf8")).trim().split("\n").length, 4);
  const failure = await run(root, ["blocked"]).completion;
  assert.notEqual(failure.code, 0);
  assert.doesNotMatch(failure.output, /process.exit\(99\)/u);
});

for (const outcome of ["interruption", "service-failure"]) {
 test(`development runner preserves isolation and reaps concurrent services on ${outcome}`, { timeout: 30_000 }, async (t) => {
  let execution;
  const root = await fixture(t, {}, () => execution && terminateTree(execution.child));
  const packages = ["devhud", "devhud-admin", "@delinoio/devhud-api"];
  for (const [index, name] of packages.entries()) {
    const directory = join(root, "packages", String(index));
    await mkdir(directory, { recursive: true });
    await writeFile(join(directory, "package.json"), JSON.stringify({ name, private: true }));
    await config(directory, { dev: { command: "node serve.mjs", cache: false } });
    await writeFile(join(directory, "serve.mjs"), `import {writeFileSync} from 'node:fs'; writeFileSync('started.json',JSON.stringify({pid:process.pid,mode:process.env.DEVHUD_LOCAL_MODE,cargo:process.env.CARGO_HOME,display:process.env.DISPLAY,names:Object.keys(process.env)})); setInterval(()=>{},1000);${outcome === 'service-failure' && index === 2 ? 'setTimeout(()=>process.exit(7),500);' : ''}`);
  }
  const source = { ...process.env, CARGO_HOME: join(root, "cargo"), DISPLAY: ":42", DEVHUD_DATABASE_URL: "task-canary", INFISICAL_TOKEN: "task-canary", DOCKER_HOST: "task-canary", NEXT_SECRET: "task-canary" };
  execution = run(root, developmentTaskArguments.slice(3), rootChildEnvironment("oss", source));
  const records = [];
  for (let attempt = 0; attempt < 100; attempt++) {
    records.length = 0;
    for (let index = 0; index < packages.length; index++) {
      try { records.push(JSON.parse(await readFile(join(root, "packages", String(index), "started.json"), "utf8"))); }
      catch (error) { if (error.code !== "ENOENT") throw error; }
    }
    if (records.length === 3) break;
    if (execution.child.exitCode !== null) break;
    await delay(50);
  }
  assert.equal(records.length, 3, execution.child.exitCode !== null ? (await execution.completion).output : "all persistent services must start concurrently");
  for (const record of records) {
    assert.equal(record.mode, "oss");
    assert.equal(record.cargo, source.CARGO_HOME);
    assert.equal(record.display, process.platform === "linux" ? ":42" : undefined);
    for (const name of ["DEVHUD_DATABASE_URL", "INFISICAL_TOKEN", "DOCKER_HOST", "NEXT_SECRET"]) assert.ok(!record.names.includes(name), name);
  }
  if (outcome === "interruption") await terminateTree(execution.child);
  const result = await execution.completion;
  if (outcome === "service-failure") assert.notEqual(result.code, 0);
  assert.doesNotMatch(result.output, /task-canary/u);
  for (const { pid } of records) {
    for (let attempt = 0; attempt < 50; attempt++) {
      try { process.kill(pid, 0); } catch (error) { if (error.code === "ESRCH") break; throw error; }
      await delay(20);
    }
    assert.throws(() => process.kill(pid, 0), { code: "ESRCH" });
  }
});
}
