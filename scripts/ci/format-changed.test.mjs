// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  copyFileSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import {
  allFormatFiles,
  changedFormatFiles,
  runFormatter,
} from "./format-changed.mjs";
import { Event, planJobs } from "./plan.mjs";

const root = fileURLToPath(new URL("../..", import.meta.url));
function fixture(t) {
  const cwd = mkdtempSync(join(tmpdir(), "rstack-format-"));
  t.after(() => rmSync(cwd, { recursive: true, force: true }));
  const git = (...args) =>
    execFileSync("git", args, { cwd, encoding: "utf8" }).trim();
  git("init", "--quiet");
  git("config", "user.email", "fixture@example.invalid");
  git("config", "user.name", "Formatting fixture");
  git("config", "core.hooksPath", ".no-hooks");
  const write = (name, text) => {
    mkdirSync(dirname(join(cwd, name)), { recursive: true });
    writeFileSync(join(cwd, name), text);
  };
  write("package.json", '{"type":"module"}\n');
  copyFileSync(join(root, "rstack.config.ts"), join(cwd, "rstack.config.ts"));
  symlinkSync(
    join(root, "node_modules"),
    join(cwd, "node_modules"),
    process.platform === "win32" ? "junction" : "dir",
  );
  write(".gitignore", "node_modules\n");
  const commit = () => {
    git("add", ".");
    git("commit", "--quiet", "-m", "fixture");
    return git("rev-parse", "HEAD");
  };
  return { cwd, write, git, commit, base: commit() };
}

test("real Rstack checks whole changed files without rewriting, then fixes them", (t) => {
  const f = fixture(t);
  const names = ["space name.ts", "한글[1].ts", "-flag.ts", "!negation.ts"];
  if (process.platform !== "win32") names.push("line\nname.ts", "star*.ts");
  for (const name of names) f.write(name, "const value={answer:42}\n");
  f.write("unrelated.go", "not Go\n");
  const head = f.commit();
  const paths = changedFormatFiles(f.cwd, f.base, head);
  assert.deepEqual(paths, names.sort());
  const before = paths.map((name) => readFileSync(join(f.cwd, name), "utf8"));
  assert.equal(runFormatter(f.cwd, paths), 1);
  assert.deepEqual(
    paths.map((name) => readFileSync(join(f.cwd, name), "utf8")),
    before,
  );
  assert.equal(runFormatter(f.cwd, paths, "write"), 0);
  assert.equal(runFormatter(f.cwd, paths), 0);
  // Validation checks merge/worktree bytes, not the selected head blob.
  f.write(names[0], "const broken = ;\n");
  assert.equal(runFormatter(f.cwd, paths), 2);
});

test("deletions, renames, empty ranges, local edits and invalid comparisons", (t) => {
  const f = fixture(t);
  f.write("old.ts", "const old = 1;\n");
  f.write("deleted.ts", "const deleted = 1;\n");
  const base = f.commit();
  renameSync(join(f.cwd, "old.ts"), join(f.cwd, "new.ts"));
  rmSync(join(f.cwd, "deleted.ts"));
  const head = f.commit();
  assert.deepEqual(changedFormatFiles(f.cwd, base, head), ["new.ts"]);
  assert.deepEqual(changedFormatFiles(f.cwd, head, head), []);
  assert.equal(runFormatter(f.cwd, []), 0);
  f.write("local.md", "# Local\n");
  f.write("new.ts", "const changed=2\n");
  assert.deepEqual(changedFormatFiles(f.cwd, head, head, true), [
    "local.md",
    "new.ts",
  ]);
  assert.throws(() => changedFormatFiles(f.cwd, "", head), /comparison SHA/);
  assert.throws(() => changedFormatFiles(f.cwd, "a".repeat(40), head));
});

test("all-file selection skips unstaged deletions and retains existing files", (t) => {
  const f = fixture(t);
  f.write("deleted.ts", "const deleted = 1;\n");
  f.write("retained.ts", "const retained = 1;\n");
  f.commit();
  rmSync(join(f.cwd, "deleted.ts"));
  const paths = allFormatFiles(f.cwd);
  assert.ok(!paths.includes("deleted.ts"));
  assert.ok(paths.includes("retained.ts"));
  assert.equal(runFormatter(f.cwd, paths, "write"), 0);
  assert.equal(runFormatter(f.cwd, paths), 0);
  assert.ok(f.git("status", "--porcelain").includes("D deleted.ts"));
});

test("Rstack owns exclusions and retains Markdown prose and embedded code", (t) => {
  const f = fixture(t);
  const ignored = [
    "packages/example/src/gen/client.ts",
    "vendor/code.ts",
    "data/fixtures/input.json",
    "data/testdata/input.json",
    "pnpm-lock.yaml",
    "guidance.generated.json",
    "apps/delidev/src/locales/resources.ts",
    "output/dist/code.ts",
  ];
  for (const name of ignored) f.write(name, "deliberately invalid {{{\n");
  f.commit();
  assert.equal(runFormatter(f.cwd, ignored), 0);
  assert.ok(
    ignored.every(
      (name) =>
        readFileSync(join(f.cwd, name), "utf8") ===
        "deliberately invalid {{{\n",
    ),
  );
  const md =
    "# Guide\n\nA long prose line that stays on this line without being wrapped by formatting.\n\n```js\nconst x={a:1}\n```\n";
  f.write("guide.md", md);
  assert.equal(runFormatter(f.cwd, ["guide.md"]), 0);
  assert.equal(readFileSync(join(f.cwd, "guide.md"), "utf8"), md);
});

test("authored localization JSON remains eligible for formatting", (t) => {
  const f = fixture(t);
  const name = "apps/devhud-chrome-extension/public/_locales/en/messages.json";
  const source = '{"title":{"message":"Browser context"}}\n';
  f.write(name, source);
  const head = f.commit();
  const paths = changedFormatFiles(f.cwd, f.base, head);
  assert.deepEqual(paths, [name]);
  assert.equal(runFormatter(f.cwd, paths), 1);
  assert.equal(readFileSync(join(f.cwd, name), "utf8"), source);
  assert.equal(runFormatter(f.cwd, paths, "write"), 0);
  assert.deepEqual(
    JSON.parse(readFileSync(join(f.cwd, name), "utf8")),
    JSON.parse(source),
  );
  assert.equal(runFormatter(f.cwd, paths), 0);
});

test("formatting is selected on all CI events for source, docs and configuration", () => {
  for (const event of Object.values(Event)) {
    for (const path of [
      "src/file.tsx",
      "style.css",
      "data.json",
      ".github/workflows/example.yml",
      "docs/guide.md",
      "rstack.config.ts",
    ]) {
      assert.equal(
        planJobs(event, [path]).jobs["source-format"],
        true,
        `${event}: ${path}`,
      );
    }
    assert.equal(planJobs(event, []).jobs["source-format"], false);
    assert.equal(
      planJobs(event, ["crates/example/src/lib.rs"]).jobs["source-format"],
      false,
    );
  }
});

test("CLI rejects incomplete comparisons and CI-wide formatting", (t) => {
  const f = fixture(t);
  const script = join(root, "scripts/ci/format-changed.mjs");
  const env = { ...process.env, CI: "true" };
  delete env.TURBO_SCM_BASE;
  delete env.TURBO_SCM_HEAD;
  for (const args of [
    [],
    ["--base", f.base],
    ["--all", "--base", f.base, "--head", f.base],
  ]) {
    const result = spawnSync(process.execPath, [script, ...args], {
      cwd: f.cwd,
      env,
      encoding: "utf8",
    });
    assert.equal(result.status, 2);
    assert.match(result.stderr, /ci_format_error/);
  }
});
