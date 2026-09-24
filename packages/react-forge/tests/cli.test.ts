import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
const exec = promisify(execFile);

test("workspace CLI runs TSX tasks and reports JSON, help, version and safe errors", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-cli-"));
  const cli = new URL("../bin/react-forge.mjs", import.meta.url).pathname;
  const task = new URL("../examples/presentation.tsx", import.meta.url).pathname;
  try {
    const { stdout } = await exec(process.execPath, [cli, "run", task, "--output", join(directory, "result.pptx"), "--data", '{"title":"CLI test"}', "--json"]);
    assert.equal(JSON.parse(stdout).ok, true);
    assert.equal((await readFile(join(directory, "result.pptx"))).subarray(0, 2).toString(), "PK");
    assert.match((await exec(process.execPath, [cli, "--help"])).stdout, /default task/);
    assert.equal((await exec(process.execPath, [cli, "--version"])).stdout.trim(), "0.0.0");
    await assert.rejects(exec(process.execPath, [cli, "run", task, "--unknown", "--json"]), (error: unknown) => {
      const result = JSON.parse((error as { stdout: string }).stdout);
      assert.equal(result.ok, false);
      assert.equal(result.error.code, "malformed_input");
      return true;
    });
  } finally { await rm(directory, { recursive: true, force: true }); }
});
