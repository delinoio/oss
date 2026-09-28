import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

const root = fileURLToPath(new URL("../../", import.meta.url));
const script = join(root, "scripts/check-proto-breaking.sh");

test("schema baseline comparison ignores unavailable LFS assets but still rejects breaking fields", (t) => {
  const temporary = mkdtempSync(join(tmpdir(), "proto-lfs-"));
  t.after(() => rmSync(temporary, { recursive: true, force: true }));
  const cwd = join(temporary, "repository");
  mkdirSync(cwd);
  const config = join(temporary, "gitconfig");
  writeFileSync(config, "");
  const env = { ...process.env, GIT_CONFIG_GLOBAL: config, GIT_CONFIG_NOSYSTEM: "1", GIT_TERMINAL_PROMPT: "0", GIT_LFS_SKIP_SMUDGE: "0" };
  const git = (...args) => execFileSync("git", args, { cwd, env, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] }).trim();
  const write = (path, body) => { mkdirSync(dirname(join(cwd, path)), { recursive: true }); writeFileSync(join(cwd, path), body); };
  git("init", "-b", "main");
  git("config", "user.name", "CI Fixture");
  git("config", "user.email", "ci@example.invalid");
  git("config", "commit.gpgsign", "false");
  git("config", "core.hooksPath", join(temporary, "empty-hooks"));
  git("lfs", "install", "--skip-repo");
  write("buf.yaml", "version: v2\nmodules:\n  - path: protos\nbreaking:\n  use:\n    - FILE\n");
  write("package.json", JSON.stringify({ private: true, packageManager: "pnpm@10.26.2" }));
  write(".gitignore", "node_modules\n");
  write(".gitattributes", "unavailable.bin filter=lfs diff=lfs merge=lfs -text\n");
  // The source and baseline deliberately contain only the pointer, just like
  // the protocol CI checkout. No remote server or cached LFS payload exists.
  const oid = "a".repeat(64);
  write("unavailable.bin", `version https://git-lfs.github.com/spec/v1\noid sha256:${oid}\nsize 1024\n`);
  const schema = 'syntax = "proto3";\npackage devhud.v1;\nmessage Fixture { string value = 1; }\n';
  write("protos/devhud/v1/common.proto", schema);
  git("add", "--all");
  git("commit", "-m", "schema baseline with unavailable LFS asset");
  const baseline = git("rev-parse", "HEAD");
  symlinkSync(join(root, "node_modules"), join(cwd, "node_modules"), process.platform === "win32" ? "junction" : "dir");
  const run = (ref) => spawnSync("bash", [script], {
    cwd, env: { ...env, DEVHUD_PROTO_BASELINE: ref }, encoding: "utf8", timeout: 30_000,
  });
  let result = run(baseline);
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.equal(existsSync(join(cwd, ".git/lfs/objects/aa/aa", oid)), false);
  write("protos/devhud/v1/common.proto", schema.replace(" string value = 1; ", ""));
  result = run(baseline);
  assert.notEqual(result.status, 0, "Removing a baseline field must fail comparison");
  assert.match(result.stdout + result.stderr, /Previously present field.*value/u);
  result = run("no-schema-baseline");
  assert.equal(result.status, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /treating this change as the v1 baseline/u);
});
