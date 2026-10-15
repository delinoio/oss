import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import {
  readJavaScriptBuildMetadata,
  writeJavaScriptBuildProvenanceFiles,
} from "./js-build-provenance.mjs";

const revision = "a".repeat(40);
const files = ["index.js", "glb.js", "fbx.js"];

test("JavaScript build provenance fixtures work in a dirty source tree", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-js-build-"));
  try {
    for (const file of files) await writeFile(join(directory, file), file);
    await writeJavaScriptBuildProvenanceFiles(directory, revision);
    const metadata = await readJavaScriptBuildMetadata(directory, revision);
    assert.deepEqual(Object.keys(metadata.files), [...files].sort());
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

test("JavaScript build provenance binds transitive local modules", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-js-build-"));
  try {
    await mkdir(join(directory, "scene"));
    for (const file of files)
      await writeFile(
        join(directory, file),
        file === "glb.js" ? 'export * from "./scene/session.js";' : file,
      );
    await writeFile(
      join(directory, "scene/session.js"),
      "export const session = true;",
    );
    await writeJavaScriptBuildProvenanceFiles(directory, revision);
    const metadata = await readJavaScriptBuildMetadata(directory, revision);
    assert.deepEqual(Object.keys(metadata.files), [
      "fbx.js",
      "glb.js",
      "index.js",
      "scene/session.js",
    ]);

    await writeFile(
      join(directory, "scene/session.js"),
      "export const session = false;",
    );
    await assert.rejects(
      readJavaScriptBuildMetadata(directory, revision),
      /digest mismatch/,
    );
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
