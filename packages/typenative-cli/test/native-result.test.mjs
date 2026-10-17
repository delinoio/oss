import assert from "node:assert/strict";
import test from "node:test";
import { finalizeNativeResult } from "./native-result.mjs";

test("native success waits for successful artifact cleanup", async () => {
  let release;
  const cleanup = new Promise((resolve) => {
    release = resolve;
  });
  let finished = false;
  const pending = finalizeNativeResult(
    { status: "passed", buildId: "invocation" },
    () => cleanup,
  ).then((result) => {
    finished = true;
    return result;
  });
  await Promise.resolve();
  assert.equal(finished, false);
  release();
  assert.equal((await pending).status, "passed");
});

for (const status of ["passed", "failed"]) {
  test(`cleanup errors sanitize a ${status} result without rejecting`, async () => {
    const result = await finalizeNativeResult(
      { status, buildId: "invocation" },
      async () => {
        throw new Error("EPERM private-artifact-path");
      },
    );
    assert.equal(result.status, "failed");
    assert.equal(result.phase, "cleanup");
    assert.equal(result.code, "TN_NATIVE_CLEANUP_FAILED");
    assert.equal(result.buildId, "invocation");
    assert.equal(
      JSON.stringify(result).includes("private-artifact-path"),
      false,
    );
  });
}
