// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { activeRequestRows, projectActiveQuestion, questionPresentation, questionReceiptMatches } from "./active-question";
const sessionId = newRequestId(), id = newRequestId();
const row = (state?: string, closure = "open", revision = 9n) => create(ResourceSchema, { id, sessionId, kind: EntityKind.INTERACTION, revision, schemaVersion: 1, documentJson: encode({ type: "user-question", closure, ...(state ? { response: { state, id: "original" } } : {}) }) });
for (const state of ["queued", "claimed", "transmitted", "uncertain", "canceled", "failed", "unknown"]) it(`keeps ${state} delivery visible without the answer form`, () => {
  expect(projectActiveQuestion(row(state), false)).toEqual({ displayed: true, compact: true });
});
it("retires only authoritative acceptance or known closure without unresolved ownership", () => {
  expect(projectActiveQuestion(row(), false)).toEqual({ displayed: true, compact: false });
  for (const resolved of [row("accepted"), row(undefined, "native-closed"), row(undefined, "turn-ended")]) {
    expect(projectActiveQuestion(resolved, false).displayed).toBe(false);
    expect(projectActiveQuestion(resolved, true).displayed).toBe(true);
  }
  expect(projectActiveQuestion(row("accepted", "unknown"), false).displayed).toBe(true);
  expect(projectActiveQuestion(row("queued", "native-closed"), false).displayed).toBe(true);
});
it("preserves accepted revision watermarks across stale payload restoration", () => {
  const accepted = questionPresentation(row("accepted", "native-closed", 11n));
  expect(projectActiveQuestion(row(), false, accepted)).toEqual({ displayed: false, compact: true });
  expect(projectActiveQuestion(row(), true, accepted)).toEqual({ displayed: true, compact: true });
});
it("merges original resources by revision while retaining page and final-arrival order", () => {
  const other = create(ResourceSchema, { ...row(), id: newRequestId() });
  const newer = row("accepted", "native-closed", 11n);
  expect(activeRequestRows([row(), newer], new Map(), new Set(), [], sessionId, false)).toEqual([newer]);
  const foreign = create(ResourceSchema, { ...newer, sessionId: newRequestId() });
  expect(activeRequestRows([row()], new Map([[id, foreign]]), new Set(), [], sessionId, false)).toEqual([row()]);
  expect(activeRequestRows([row()], new Map([[id, newer], [other.id, other]]), new Set(), [other.id], sessionId, true)).toEqual([newer, other]);
  expect(activeRequestRows([row()], new Map([[id, newer]]), new Set([id]), [], sessionId, false)).toEqual([]);
});
it("accepts only the exact original response UUID, revision, interaction and authenticated session", () => {
  const request = { mutation: { id, expectedRevision: 9n, requestId: "original" } };
  expect(questionReceiptMatches(row("queued", "open", 10n), request, sessionId)).toBe(true);
  for (const mutation of [{ ...request.mutation, requestId: "replacement" }, { ...request.mutation, id: newRequestId() }, { ...request.mutation, expectedRevision: 10n }]) expect(questionReceiptMatches(row("queued", "open", 10n), { mutation }, sessionId)).toBe(false);
  expect(questionReceiptMatches(row("queued", "open", 10n), request, newRequestId())).toBe(false);
});
