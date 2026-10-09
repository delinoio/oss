// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  Identity,
  Stage,
  candidate,
  retainCandidate,
  distribute,
  credentials,
} from "./beta.mjs";
const input = {
  identity: Identity,
  version: "0.1.0",
  iosBuild: "7",
  androidCode: "8",
  sourceSha: "a".repeat(40),
  appleGroupType: "INTERNAL",
  playTrack: "internal",
  appleSigner: "b".repeat(64),
  androidSigner: "c".repeat(64),
};
const ios = {
  identity: Identity,
  version: input.version,
  build: "7",
  sourceSha: input.sourceSha,
  signer: input.appleSigner,
  architectures: ["arm64"],
  bytes: Buffer.from("owned-ios-fixture"),
};
const android = {
  ...ios,
  build: "8",
  signer: input.androidSigner,
  architectures: ["arm64-v8a", "armeabi-v7a"],
  bytes: Buffer.from("owned-android-fixture"),
};
test("identity, signers, version and internal-only destinations fail closed", () => {
  for (const change of [
    { identity: "io.delino.devhud" },
    { appleGroupType: "EXTERNAL" },
    { playTrack: "production" },
    { androidCode: "0" },
    { sourceSha: "main" },
  ])
    assert.throws(() => candidate({ ...input, ...change }, ios, android));
  for (const change of [
    { identity: "foreign" },
    { version: "1.0.0" },
    { signer: "d".repeat(64) },
    { architectures: ["x86_64"] },
  ])
    assert.throws(() => candidate(input, { ...ios, ...change }, android));
  assert.throws(() => credentials({}));
});
test("candidate hash, checksums and version reuse are immutable", () => {
  const d = mkdtempSync(join(tmpdir(), "delidev-beta-"));
  try {
    const m = candidate(input, ios, android),
      path = join(d, "manifest.json");
    retainCandidate(path, m);
    retainCandidate(path, m);
    assert.throws(() =>
      retainCandidate(
        path,
        candidate(input, { ...ios, bytes: Buffer.from("other") }, android),
      ),
    );
  } finally {
    rmSync(d, { recursive: true, force: true });
  }
});
test("dry run has no provider or credential access", async () => {
  const result = await distribute(
    candidate(input, ios, android),
    {
      candidateId: candidate(input, ios, android).candidateId,
      platform: "ios",
      stage: Stage.Ready,
    },
    new Proxy(
      {},
      {
        get() {
          throw new Error("provider invoked");
        },
      },
    ),
    () => {
      throw new Error("write");
    },
  );
  assert.equal(result.outcome, "dry-run");
});
test("unknown upload never retries and preserves original candidate", async () => {
  const m = candidate(input, ios, android),
    changes = [];
  let sends = 0;
  const provider = {
    preflight: async () => {},
    upload: async () => {
      sends++;
      throw new Error("lost acknowledgment");
    },
    inspect: async () => ({ state: "absent" }),
  };
  await assert.rejects(
    distribute(
      m,
      { candidateId: m.candidateId, platform: "ios", stage: Stage.Ready },
      provider,
      async (r) => changes.push(r),
      { dryRun: false },
    ),
  );
  assert.equal(changes.at(-1).stage, Stage.Unknown);
  await assert.rejects(
    distribute(m, changes.at(-1), provider, async (r) => changes.push(r), {
      dryRun: false,
    }),
  );
  assert.equal(sends, 1);
});
test("partial distribution reconciles original uploaded identity without another upload", async () => {
  const m = candidate(input, ios, android);
  let sends = 0,
    assigned = false;
  const provider = {
    preflight: async () => {},
    upload: async () => {
      sends++;
    },
    inspect: async () => ({
      state: "present",
      id: "original",
      sha256: m.artifacts.android.sha256,
      identity: Identity,
      internal: true,
      distributed: assigned,
    }),
    assignInternal: async () => {
      assigned = true;
    },
  };
  const result = await distribute(
    m,
    { candidateId: m.candidateId, platform: "android", stage: Stage.Unknown },
    provider,
    async () => {},
    { dryRun: false },
  );
  assert.equal(result.stage, Stage.Distributed);
  assert.equal(sends, 0);
});
