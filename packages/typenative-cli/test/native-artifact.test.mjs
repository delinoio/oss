import assert from "node:assert/strict";
import { mkdtemp, writeFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { Target } from "../dist/core.js";
import { readNativeArtifact } from "./native-artifact.mjs";

test("missing and invalid compiler artifacts share a stable diagnostic", async () => {
  const root = await mkdtemp(join(tmpdir(), "typenative-artifact-"));
  try {
    const path = join(root, "missing");
    await assert.rejects(readNativeArtifact(path, Target.Windows), {
      message: "TN_NATIVE_ARTIFACT_INVALID",
    });
    await writeFile(path, "invalid");
    await assert.rejects(readNativeArtifact(path, Target.Windows), {
      message: "TN_NATIVE_ARTIFACT_INVALID",
    });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
