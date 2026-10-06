import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const installers = [
  "nodeup.sh", "nodeup.ps1", "binpm.sh", "binpm.ps1",
  "async-commit-hook.sh", "async-commit-hook.ps1", "pnport.sh", "pnport.ps1",
];
const tasks = ["build", "build:frontend", "ci:routes"];

test("public-docs installer inputs retain inherited build settings and outputs", () => {
  const config = JSON.parse(readFileSync(join(root, "apps/public-docs/turbo.json"), "utf8"));
  for (const task of tasks) {
    for (const installer of installers) {
      assert.ok(config.tasks[task].inputs.includes(`$TURBO_ROOT$/scripts/install/${installer}`), `${task}: ${installer}`);
    }
  }
  for (const task of ["build", "build:frontend"]) {
    assert.deepEqual(Object.keys(config.tasks[task]), ["inputs"]);
    assert.equal(config.tasks[task].inputs[0], "$TURBO_EXTENDS$");
  }
  assert.equal(config.tasks["ci:routes"].cache, true);
  assert.deepEqual(config.tasks["ci:routes"].outputs, []);
});

test("each public installer invalidates builds and checks while unchanged output restores exactly", (t) => {
  const cwd = mkdtempSync(join(tmpdir(), "public-docs-cache-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const write = (path, data) => {
    mkdirSync(dirname(join(cwd, path)), { recursive: true });
    writeFileSync(join(cwd, path), data);
  };
  const copy = (path) => write(path, readFileSync(join(root, path)));
  const git = (...args) => execFileSync("git", args, { cwd, stdio: "pipe" });
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(cwd, "empty-hooks"));
  for (const path of ["package.json", "pnpm-workspace.yaml", "pnpm-lock.yaml", "turbo.json", ".nvmrc", "rust-toolchain", "go.mod", "go.sum", "apps/public-docs/turbo.json", "apps/public-docs/scripts/copy-public-assets.mjs"]) copy(path);
  for (const installer of installers) copy(`scripts/install/${installer}`);
  write(".gitignore", "node_modules\n.turbo\n**/dist\n**/doc_build\napps/public-docs/docs/public\n**/.runs\n");

  // Use the production Turbo configuration and asset copier with a bounded
  // output fixture. The full public-docs test owns Rspress and rendered routes.
  const manifest = JSON.parse(readFileSync(join(root, "apps/public-docs/package.json"), "utf8"));
  manifest.scripts.build = "node scripts/copy-public-assets.mjs && node fixture-build.mjs";
  manifest.scripts["build:frontend"] = manifest.scripts.build;
  manifest.scripts["ci:routes"] = "node fixture-check.mjs";
  write("apps/public-docs/package.json", JSON.stringify(manifest));
  write("packages/docs-site-switcher/package.json", JSON.stringify({
    name: "@delinoio/docs-site-switcher", private: true,
    scripts: { build: "node -e \"console.log('selector build fixture')\"", test: "node -e \"console.log('selector check fixture')\"" },
  }));
  write("apps/public-docs/fixture-build.mjs", `
import { appendFileSync, cpSync, mkdirSync, writeFileSync } from 'node:fs';
appendFileSync('.runs', 'build\\n');
mkdirSync('doc_build', { recursive: true });
cpSync('docs/public', 'doc_build', { recursive: true });
writeFileSync('doc_build/index.html', '<main>Stable route fixture</main>\\n');
`);
  write("apps/public-docs/fixture-check.mjs", `
import assert from 'node:assert/strict';
import { appendFileSync, readFileSync } from 'node:fs';
appendFileSync('.runs', 'check\\n');
for (const installer of ${JSON.stringify(installers)}) {
  const [slug, extension] = installer.split('.');
  assert.deepEqual(readFileSync('doc_build/' + slug + '/install.' + extension), readFileSync('../../scripts/install/' + installer), installer);
}
`);
  git("add", "--all");
  git("commit", "-m", "fixture");
  symlinkSync(join(root, "node_modules"), join(cwd, "node_modules"), process.platform === "win32" ? "junction" : "dir");
  const turbo = join(root, "node_modules/turbo/bin/turbo");
  const run = (args) => spawnSync(process.execPath, [turbo, "run", ...args, "--filter=public-docs", "--cache=local:rw", "--no-daemon"], {
    cwd, encoding: "utf8", env: { ...process.env, CI: "true", TURBO_TELEMETRY_DISABLED: "1" },
  });
  const pass = (...args) => {
    const result = run(args);
    assert.equal(result.status, 0, result.stdout + result.stderr);
    return result;
  };
  const hashes = () => new Map(JSON.parse(pass(...tasks, "--dry=json").stdout).tasks.map((task) => [task.taskId, task.hash]));
  const output = (installer) => {
    const [slug, extension] = installer.split(".");
    return readFileSync(join(cwd, `apps/public-docs/doc_build/${slug}/install.${extension}`));
  };
  const currentOutput = () => {
    for (const installer of installers) assert.deepEqual(output(installer), readFileSync(join(cwd, `scripts/install/${installer}`)), installer);
  };
  const runs = () => readFileSync(join(cwd, "apps/public-docs/.runs"), "utf8");
  const cleanOutput = () => rmSync(join(cwd, "apps/public-docs/doc_build"), { recursive: true, force: true });
  const baseline = hashes();
  pass("build");
  pass("ci:routes");
  currentOutput();
  const baselineRuns = runs();
  const baselineRoute = readFileSync(join(cwd, "apps/public-docs/doc_build/index.html"));
  for (const task of ["build", "ci:routes"]) {
    cleanOutput();
    assert.match(pass(task).stdout, /cache hit/u);
    assert.equal(runs(), baselineRuns);
    currentOutput();
    assert.deepEqual(readFileSync(join(cwd, "apps/public-docs/doc_build/index.html")), baselineRoute);
  }

  for (const installer of installers) {
    const path = `scripts/install/${installer}`;
    const original = readFileSync(join(cwd, path));
    write(path, Buffer.concat([original, Buffer.from("\n# cache invalidation fixture\n")]));
    const changed = hashes();
    for (const task of tasks) assert.notEqual(changed.get(`public-docs#${task}`), baseline.get(`public-docs#${task}`), `${task}: ${installer}`);
    assert.equal(changed.get("@delinoio/docs-site-switcher#build"), baseline.get("@delinoio/docs-site-switcher#build"), installer);
    const before = runs();
    pass("build");
    pass("ci:routes");
    assert.equal(runs(), before + "build\nbuild\ncheck\n", installer);
    currentOutput();
    write(path, original);
    cleanOutput();
    assert.match(pass("ci:routes").stdout, /cache hit/u);
    currentOutput();
    assert.deepEqual(hashes(), baseline);
  }

  // A fresh check must reject an old generated installer when its source
  // changes. --only bypasses the build to expose the equality failure.
  write("scripts/install/nodeup.sh", Buffer.concat([readFileSync(join(cwd, "scripts/install/nodeup.sh")), Buffer.from("\n# mismatch fixture\n")]));
  const mismatch = run(["ci:routes", "--only"]);
  assert.notEqual(mismatch.status, 0, mismatch.stdout + mismatch.stderr);
  assert.match(mismatch.stdout + mismatch.stderr, /AssertionError/u);
});
