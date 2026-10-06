import assert from "node:assert/strict";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fixtureBinary } from "./fixture-binary.mjs";

test("fixture executable reuse leaves local builds and separate private state available", async (t) => {
  const directory = await mkdtemp(join(tmpdir(), "ci-fixture-binary-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const binary = join(directory, "immutable-binary");
  await writeFile(binary, "fixture");
  assert.equal(await fixtureBinary("local", {}), "local");
  assert.equal(await fixtureBinary("first-private-scope", { DELIDEV_TEST_BINARY: binary }), binary);
  assert.equal(await fixtureBinary("second-private-scope", { DELIDEV_TEST_BINARY: binary }), binary);
  await mkdir(join(directory, "not-a-file"));
  for (const value of ["", "relative", join(directory, "missing"), join(directory, "not-a-file")]) {
    await assert.rejects(fixtureBinary("local", { DELIDEV_TEST_BINARY: value }));
  }
});
