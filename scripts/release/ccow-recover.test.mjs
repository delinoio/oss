// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import test from "node:test";
import { recoverySource, resolveRecovery } from "./ccow-recover.mjs";

function fixture() {
  const run = {
    id: 42,
    repository: { full_name: "delinoio/oss" },
    head_repository: { full_name: "delinoio/oss" },
    path: ".github/workflows/release-ccow.yml",
    event: "workflow_dispatch",
    head_branch: "main",
    status: "completed",
    conclusion: "failure",
    head_sha: "a".repeat(40),
  };
  return {
    run,
    sourcePackage: { version: "0.1.0" },
    artifacts: ["candidate", "signatures"].map((kind) => ({
      name: `ccow-${kind}-${run.head_sha}`,
      expired: false,
    })),
  };
}

test("recovery binds the original failed main release and retained artifact identities", () => {
  const f = fixture();
  assert.deepEqual(recoverySource(f.run, f.sourcePackage, f.artifacts, "42"), {
    revision: "a".repeat(40),
    version: "0.1.0",
    runId: "42",
  });
  for (const [field, value] of Object.entries({
    id: 43,
    repository: { full_name: "other/oss" },
    head_repository: { full_name: "other/oss" },
    path: ".github/workflows/other.yml",
    event: "pull_request",
    head_branch: "feature",
    status: "in_progress",
    conclusion: "success",
    head_sha: "0".repeat(40),
  })) {
    assert.throws(
      () =>
        recoverySource(
          { ...f.run, [field]: value },
          f.sourcePackage,
          f.artifacts,
          "42",
        ),
      field,
    );
  }
  assert.throws(() =>
    recoverySource(f.run, { version: "0.2.0" }, f.artifacts, "42"),
  );
  for (const runId of ["0", "42;false", "9007199254740993", "../42"])
    assert.throws(() =>
      recoverySource(f.run, f.sourcePackage, f.artifacts, runId),
    );
  for (const artifacts of [
    f.artifacts.slice(1),
    [...f.artifacts, f.artifacts[0]],
    f.artifacts.map((a) => ({ ...a, expired: true })),
  ])
    assert.throws(() =>
      recoverySource(f.run, f.sourcePackage, artifacts, "42"),
    );
});

test("recovery fetches source metadata at the original SHA and paginates artifact reads without writes", () => {
  const f = fixture();
  const reads = [];
  const resolved = resolveRecovery("42", (endpoint) => {
    reads.push(endpoint);
    if (endpoint.endsWith("/actions/runs/42")) return f.run;
    if (endpoint.includes("/contents/")) {
      assert(endpoint.endsWith(`?ref=${f.run.head_sha}`));
      return {
        encoding: "base64",
        content: Buffer.from(JSON.stringify(f.sourcePackage)).toString(
          "base64",
        ),
      };
    }
    if (endpoint.endsWith("page=1"))
      return {
        artifacts: [
          ...f.artifacts,
          ...Array.from({ length: 98 }, (_, i) => ({
            name: `unrelated-${i}`,
            expired: false,
          })),
        ],
      };
    if (endpoint.endsWith("page=2")) return { artifacts: [] };
    throw new Error("Unexpected endpoint");
  });
  assert.equal(resolved.revision, f.run.head_sha);
  assert.equal(reads.length, 4);
});
