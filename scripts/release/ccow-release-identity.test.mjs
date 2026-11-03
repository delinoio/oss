// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { releaseIdentity } from "./ccow-release-identity.mjs";

const revision = "a".repeat(40);

test("release identity rejects arbitrary refs, versions and revisions", () => {
  assert.equal(
    releaseIdentity("0.1.0", revision, "refs/heads/main").tag,
    "ccow@v0.1.0",
  );
  for (const args of [
    ["0.1.1", revision, "refs/heads/main"],
    ["0.1.0", revision, "refs/heads/topic"],
    ["0.1.0", "0".repeat(40), "refs/heads/main"],
  ])
    assert.throws(() => releaseIdentity(...args));
});
