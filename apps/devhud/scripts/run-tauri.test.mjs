import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import test from "node:test";

const scriptPath = fileURLToPath(new URL("./run-tauri.mjs", import.meta.url));
const configOverrides = [
  ["separated short option", ["build", "--", "-c", "alternate.json"]],
  ["equals-delimited short option", ["build", "--", "-c=alternate.json"]],
  ["attached short option", ["build", "--", "-calternate.json"]],
  ["separated long option", ["build", "--", "--config", "alternate.json"]],
  ["equals-delimited long option", ["build", "--", "--config=alternate.json"]],
];

for (const [name, args] of configOverrides) {
  test(`rejects the ${name}`, () => {
    const result = spawnSync(process.execPath, [scriptPath, ...args], {
      encoding: "utf8",
    });

    assert.equal(result.signal, null);
    assert.equal(result.status, 1);
    assert.match(
      result.stderr,
      /devhud: -c\/--config cannot override the pinned application, CSP, or development origin/,
    );
  });
}
