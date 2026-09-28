import { create } from "@bufbuild/protobuf";
import { expect, it } from "vitest";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { messageRows } from "./session";

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
