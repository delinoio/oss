import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import test from "node:test";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
function build() {
  const result = spawnSync("pnpm", ["build:test"], { cwd: root, env: process.env, encoding: "utf8" });
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`);
}
test("identical inputs produce byte-identical extension ZIPs", async () => {
  build(); const first = await readFile(join(root, "artifacts/devhud-chrome-extension.zip"));
  const manifest = JSON.parse(await readFile(join(root, "dist/manifest.json"), "utf8"));
  assert.equal(manifest.manifest_version, 3); assert.equal(manifest.incognito, "not_allowed"); assert.ok(manifest.permissions.includes("activeTab"));
  assert.ok(!JSON.stringify(manifest).includes("<all_urls>"));
  build(); const second = await readFile(join(root, "artifacts/devhud-chrome-extension.zip"));
  assert.deepEqual(first, second);
});
