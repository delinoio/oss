import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import {
  readJavaScriptBuildMetadata,
  writeJavaScriptBuildProvenance,
} from "./js-build-provenance.mjs";

const revision = "a".repeat(40);
const files = ["index.js", "glb.js", "fbx.js"];

test("JavaScript build provenance binds required dist files to the revision", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-js-build-"));
  try {
    for (const file of files) await writeFile(join(directory, file), file);
    await writeJavaScriptBuildProvenance(directory, revision);
    const metadata = await readJavaScriptBuildMetadata(directory, revision);
    assert.deepEqual(Object.keys(metadata.files), files);
    await writeFile(join(directory, "glb.js"), "stale");
    await assert.rejects(
      readJavaScriptBuildMetadata(directory, revision),
      /digest mismatch/,
    );
    assert.match(
      await readFile(join(directory, "release", "js-build.json"), "utf8"),
      /sourceRevision/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
