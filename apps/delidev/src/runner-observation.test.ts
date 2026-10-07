// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { claudeRunnerObservation, validRunnerObservation } from "./runner-observation";
const installation = { harness: "claude-code", state: "detected", version: "2.1.236", protocol_verified: true, protocol: { state: "verified" } };
const observed = (overrides: Record<string, unknown> = {}) => create(ResourceSchema, { id: newRequestId(), kind: EntityKind.MACHINE, schemaVersion: 1, revision: 9007199254740993n, documentJson: encode({ name: "Runner", disabled: false, worker_capabilities: ["native-claude-subscriptions-v1"], installations: [installation], ...overrides }) });
it("projects independent closed Claude exclusion causes without interpreting native messages", () => {
  expect(claudeRunnerObservation(observed())).toEqual({ detectedVersion: "2.1.236" });
  expect(claudeRunnerObservation(observed({ disabled: true })).cause).toBe("disabled");
  expect(claudeRunnerObservation(observed({ worker_capabilities: [] })).cause).toBe("capability");
  expect(claudeRunnerObservation(observed({ installations: [] })).cause).toBe("missing");
  expect(claudeRunnerObservation(observed({ installations: [installation, installation] })).cause).toBe("duplicate");
  for (const [state, cause] of [["permission-denied", "permission"], ["failed", "installationFailed"], ["unchecked", "unchecked"], ["missing", "missing"], ["incompatible", "version"]]) expect(claudeRunnerObservation(observed({ installations: [{ ...installation, state, problem: { message: "/private/credential.txt" } }] })).cause).toBe(cause);
  expect(claudeRunnerObservation(observed({ installations: [{ ...installation, version: "2.1.235" }] }))).toEqual({ cause: "version", detectedVersion: "2.1.235" });
  expect(claudeRunnerObservation(observed({ installations: [{ ...installation, protocol: { state: "failed", problem: { message: "secret native output" } }, protocol_verified: false }] })).cause).toBe("protocolFailed");
});
it("treats malformed and unknown evidence as unavailable without exposing content", () => {
  for (const patch of [{ disabled: "true" }, { worker_capabilities: [20] }, { installations: "native secret" }, { installations: [{ ...installation, state: "future-state", version: "/private/native" }] }, { installations: [{ ...installation, version: "/private/native" }] }]) {
    const projection = claudeRunnerObservation(observed(patch));
    expect(projection.cause).toBe("unavailable"); expect(JSON.stringify(projection)).not.toContain("private"); expect(JSON.stringify(projection)).not.toContain("secret");
  }
  expect(claudeRunnerObservation(create(ResourceSchema, { ...observed(), documentJson: new TextEncoder().encode("{bad native secret") })).cause).toBe("unavailable");
});

it("does not accept malformed flags or unbounded observations as current Runner ownership", () => {
  expect(validRunnerObservation(observed({ disabled: "false" }))).toBe(false);
  expect(validRunnerObservation(observed({ installations: [null] }))).toBe(false);
  expect(validRunnerObservation(observed({ worker_capabilities: Array(257).fill("unknown") }))).toBe(false);
  expect(validRunnerObservation(observed({ disabled: undefined }))).toBe(true);
});
