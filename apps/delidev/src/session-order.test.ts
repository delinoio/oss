import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { interactionRows, messageRows, queueRows, isQueuedInput } from "./session";

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

it("filters authoritative queue revisions without changing retained history or final-page identity", () => {
  const sessionId = "session-a";
  const input = (id: string, delivery: string | undefined, revision = 1n) => create(ResourceSchema, { id, sessionId, kind: EntityKind.QUEUE, schemaVersion: 1, revision, documentJson: new TextEncoder().encode(JSON.stringify({ delivery })) });
  const queued = input("queued", "queued");
  const hidden = ["claimed", "accepted", "uncertain", "rejected-before-start", "removed", "unknown", undefined].map((state, i) => input(`hidden-${i}`, state));
  expect(queueRows([queued, ...hidden], new Map(), new Set(), [], sessionId, true).filter(isQueuedInput).map(row => row.id)).toEqual(["queued"]);
  const accepted = input("queued", "accepted", 3n);
  const stale = input("queued", "queued", 2n);
  const live = new Map([[accepted.id, accepted]]);
  expect(queueRows([stale], live, new Set(), [], sessionId, false)).toEqual([accepted]);
  expect(queueRows([stale], live, new Set(), [], sessionId, false).filter(isQueuedInput)).toEqual([]);
  const restored = input("queued", "queued", 4n);
  live.set(restored.id, restored);
  const arrival = input("arrival", "accepted", 1n);
  live.set(arrival.id, arrival);
  expect(queueRows([stale], live, new Set(), [arrival.id], sessionId, true).filter(isQueuedInput)).toEqual([restored]);
  expect(queueRows([stale], live, new Set([restored.id]), [arrival.id], sessionId, true).filter(isQueuedInput)).toEqual([]);
});
