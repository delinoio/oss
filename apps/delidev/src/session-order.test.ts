import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { interactionRows, messageRows, queueRows } from "./session";

it("appends newly streamed messages even when a replacement Worker generated an older UUID", () => {
  const sessionId = "session-a";
  const base = create(ResourceSchema, { id: "f000", sessionId, kind: EntityKind.MESSAGE, revision: 1n });
  const delayed = create(ResourceSchema, { id: "1000", sessionId, kind: EntityKind.MESSAGE, revision: 1n });
  const later = create(ResourceSchema, { id: "2000", sessionId, kind: EntityKind.MESSAGE, revision: 1n });
  const oldPage = create(ResourceSchema, { id: "0500", sessionId, kind: EntityKind.MESSAGE, revision: 1n });
  const foreign = create(ResourceSchema, { id: "3000", sessionId: "session-b", kind: EntityKind.MESSAGE, revision: 1n });
  const live = new Map([base, delayed, later, oldPage, foreign].map((row) => [row.id, row]));
  const arrivals = [delayed.id, later.id, foreign.id, base.id];

  expect(messageRows([base], live, new Set(), arrivals, sessionId, true).map((row) => row.id)).toEqual([base.id, delayed.id, later.id]);
  expect(messageRows([base], live, new Set(), arrivals, sessionId, false).map((row) => row.id)).toEqual([base.id]);
  expect(messageRows([base], live, new Set([delayed.id]), arrivals, sessionId, true).map((row) => row.id)).toEqual([base.id, later.id]);
});

it("appends streamed interactions by arrival even when their UUIDs sort before the loaded page", () => {
  const sessionId = "session-a";
  const base = create(ResourceSchema, { id: "f000", sessionId, kind: EntityKind.INTERACTION, revision: 1n });
  const approval = create(ResourceSchema, { id: "1000", sessionId, kind: EntityKind.INTERACTION, revision: 1n });
  const question = create(ResourceSchema, { id: "2000", sessionId, kind: EntityKind.INTERACTION, revision: 1n });
  const foreign = create(ResourceSchema, { id: "3000", sessionId: "session-b", kind: EntityKind.INTERACTION, revision: 1n });
  const live = new Map([base, approval, question, foreign].map((row) => [row.id, row]));
  const arrivals = [approval.id, question.id, foreign.id, base.id];

  expect(interactionRows([base], live, new Set(), arrivals, sessionId, true).map((row) => row.id)).toEqual([base.id, approval.id, question.id]);
  expect(interactionRows([base], live, new Set(), arrivals, sessionId, false).map((row) => row.id)).toEqual([base.id]);
  expect(interactionRows([base], live, new Set([approval.id]), arrivals, sessionId, true).map((row) => row.id)).toEqual([base.id, question.id]);
});

it("appends a streamed queue item beyond the safe integer range by identity", () => {
  const sessionId = "session-a";
  const bytes = (sequence: string) => new TextEncoder().encode(`{"sequence":${sequence},"delivery":"queued"}`);
  const base = create(ResourceSchema, { id: "base", sessionId, kind: EntityKind.QUEUE, revision: 1n, documentJson: bytes("9007199254740992") });
  const arrived = create(ResourceSchema, { id: "arrived", sessionId, kind: EntityKind.QUEUE, revision: 1n, documentJson: bytes("9007199254740993") });
  const older = create(ResourceSchema, { id: "older", sessionId, kind: EntityKind.QUEUE, revision: 1n, documentJson: bytes("1") });
  const live = new Map([base, arrived, older].map((row) => [row.id, row]));

  expect(queueRows([base], live, new Set(), [arrived.id], sessionId, true).map((row) => row.id)).toEqual([base.id, arrived.id]);
  expect(queueRows([base], live, new Set(), [arrived.id], sessionId, false).map((row) => row.id)).toEqual([base.id]);
  expect(queueRows([base], live, new Set([arrived.id]), [arrived.id], sessionId, true).map((row) => row.id)).toEqual([base.id]);
});
