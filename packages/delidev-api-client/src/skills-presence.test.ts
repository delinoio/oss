// SPDX-License-Identifier: Apache-2.0
import { create, fromBinary, toBinary, toJson } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { CreateSessionRequestSchema, EnqueueInputRequestSchema, EditQueuedInputRequestSchema } from "./gen/delidev/v1/session_pb.js";

it("retains omitted text-only requests and explicit skill clears across the wire", () => {
 for (const schema of [CreateSessionRequestSchema, EnqueueInputRequestSchema, EditQueuedInputRequestSchema] as const) {
  const omitted = create(schema), clear = create(schema, { skills: { selections: [] } });
  expect(toJson(schema, omitted)).not.toHaveProperty("skills");
  expect(fromBinary(schema, toBinary(schema, omitted)).skills).toBeUndefined();
  expect(fromBinary(schema, toBinary(schema, clear)).skills?.selections).toEqual([]);
  expect(toJson(schema, clear)).toHaveProperty("skills", {});
 }
});
