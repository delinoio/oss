// SPDX-License-Identifier: Apache-2.0
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema } from "./gen/delidev/v1/common_pb.js";
import { decodeResourceDocument, supportsResourceSchema } from "./configuration-identity.js";
import { newRequestId } from "./validation.js";

function job(type: string, action?: string, size = (1 << 20) + 1) {
  const body = { type, state: "claimed", input: action ? { action } : { action_id: newRequestId(), assignment: { session_id: newRequestId() } } };
  const json = JSON.stringify(body);
  return create(ResourceSchema, { id: newRequestId(), sessionId: newRequestId(), kind: EntityKind.JOB, revision: 1n, schemaVersion: 1, documentJson: new TextEncoder().encode(json + " ".repeat(size - json.length)) });
}

it.each(["compact-session", "workspace-storage"])("decodes only the bounded %s family through binary Resource transport", type => {
  for (const size of [(1 << 20) + 1, 4 << 20]) {
    const original = job(type, type === "workspace-storage" ? "recover" : undefined, size);
    const received = fromBinary(ResourceSchema, toBinary(ResourceSchema, original));
    expect(received.documentJson).toHaveLength(size);
    expect(supportsResourceSchema(received)).toBe(true);
    expect(decodeResourceDocument(received)).toMatchObject({ type, state: "claimed" });
    if (size === 4 << 20) {
      const oversized = new Uint8Array(size + 1); oversized.set(received.documentJson); oversized[size] = 32;
      expect(decodeResourceDocument({ ...received, documentJson: oversized })).toBeUndefined();
    }
  }
});

it.each([
  ["execute-session", undefined], ["workspace-storage", "preview"], ["workspace-storage", "cleanup"],
  ["workspace-storage", "restore"], ["workspace-storage", "unknown"], ["unknown", "recover"],
])("keeps oversized ordinary %s/%s jobs unavailable", (type, action) => {
  const resource = job(type!, action);
  expect(decodeResourceDocument(resource)).toBeUndefined();
  expect(supportsResourceSchema(resource)).toBe(false);
  expect(decodeResourceDocument(job(type!, action, 1 << 20))).toBeDefined();
});

it("rejects unsupported schemas/kinds, invalid UTF-8 and malformed typed documents", () => {
  const resource = job("compact-session");
  for (const schemaVersion of [0, 2, 3, 4]) expect(decodeResourceDocument({ ...resource, schemaVersion })).toBeUndefined();
  expect(decodeResourceDocument({ ...resource, kind: EntityKind.SESSION })).toBeUndefined();
  for (const json of ["null", "[]", "{", '{"type":"compact-session","input":[]}', '{"type":"workspace-storage","input":{"action":"preview"}}']) {
    const bytes = new TextEncoder().encode(json + " ".repeat((1 << 20) + 1));
    expect(decodeResourceDocument({ ...resource, documentJson: bytes })).toBeUndefined();
  }
  expect(decodeResourceDocument({ ...resource, documentJson: Uint8Array.of(255) })).toBeUndefined();
});
