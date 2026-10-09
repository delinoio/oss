// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { verifyMetadata } from "./artifacts.mjs";
import { Identity } from "./beta.mjs";
test("signed metadata validation rejects wrong identity, signer, minimum OS, version and architecture", () => {
  const input = {
      version: "1.2.3",
      iosBuild: "7",
      androidCode: "8",
      appleSigner: "a".repeat(64),
      androidSigner: "b".repeat(64),
    },
    value = {
      identity: Identity,
      version: input.version,
      build: "7",
      signer: input.appleSigner,
      minimumVersion: "18.0",
      architectures: ["arm64"],
    };
  assert.equal(verifyMetadata(value, input, "ios"), value);
  for (const delta of [
    { identity: "foreign" },
    { signer: input.androidSigner },
    { minimumVersion: "16.0" },
    { build: "8" },
    { architectures: ["x86_64"] },
    { version: "2.0.0" },
  ])
    assert.throws(() => verifyMetadata({ ...value, ...delta }, input, "ios"));
});
