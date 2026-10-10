// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { expect, it } from "vitest";
import { document, encode } from "./documents";
import { QuestionPresentation as P, questionPresentation, QuestionPresentationStore, questionTrayEmpty } from "./question-presentation";
function row(revision: bigint, value: Record<string, unknown> = {}) { return create(ResourceSchema, { id: "question", sessionId: "session", kind: EntityKind.INTERACTION, schemaVersion: 1, revision, documentJson: encode({ type: "user-question", closure: "open", questions: { questions: [{ id: "q", text: "Original" }] }, ...value }) }); }
it.each(["queued", "claimed", "transmitted", "uncertain", "failed", "not-sent", "unknown", ""])('retains compact unresolved %s delivery without an answer form', state => { expect(questionPresentation(row(10n, { response: { state } }))).toBe(P.Recovery); });
it("separates confirmed closure, accepted delivery, cancellation and exact outstanding receipts", () => {
 expect(questionPresentation(row(9n))).toBe(P.Form);
 expect(questionPresentation(row(10n, { closure: "turn-ended" }))).toBe(P.Hidden);
 expect(questionPresentation(row(10n, { closure: "native-closed", response: { state: "accepted" } }))).toBe(P.Hidden);
 expect(questionPresentation(row(10n, { closure: "native-closed", response: { state: "accepted" } }), true)).toBe(P.Recovery);
 expect(questionPresentation(row(10n, { response: { state: "canceled" } }))).toBe(P.Recovery);
 expect(questionPresentation(row(10n, { closure: "turn-ended", response: { state: "canceled" } }))).toBe(P.Hidden);
 for (const value of [{ closure: "unknown" }, { closure: "open", approval_response: {} }, { closure: "turn-ended", response: { state: "claimed" } }]) expect(questionPresentation(row(11n, value))).toBe(P.Recovery);
});
it("uses the same exact authoritative revision after RPC, stale live/list/restoration and later acceptance", () => {
 const store = new QuestionPresentationStore(), original = row(9n), queued = row(10n, { response: { state: "queued" } });
 store.observe(original); store.observe(queued);
 expect(store.latest(original).revision).toBe(10n); expect(questionPresentation(store.latest(original))).toBe(P.Recovery);
 store.observe(row(11n, { closure: "native-closed", response: { state: "accepted" } })); store.observe(original); store.observe(queued);
 expect(questionPresentation(store.latest(original))).toBe(P.Hidden);
 expect(document(original).closure).toBe("open"); expect(document(store.latest(original)).questions).toEqual(document(original).questions);
 expect(store.latest(create(ResourceSchema, { ...original, sessionId: "foreign" })).revision).toBe(9n);
 const recovered = store.retained("session", new Set([original.id])); expect(recovered).toHaveLength(1);
 expect(document(recovered[0]).questions).toBeUndefined(); expect(questionPresentation(recovered[0], true)).toBe(P.Recovery);
 expect(store.retained("foreign", new Set([original.id]))).toEqual([]);
});
it("never revives forms on contradictory equal revisions or lifecycle regression", () => {
 const store = new QuestionPresentationStore(); store.observe(row(10n, { response: { state: "queued" } }));
 store.observe(row(10n)); expect(questionPresentation(store.latest(row(10n)))).toBe(P.Recovery);
 const revision = store.snapshot(); store.observe(row(10n)); expect(store.snapshot()).toBe(revision);
 store.observe(row(11n, { closure: "native-closed", response: { state: "accepted" } }));
 expect(questionPresentation(store.latest(row(12n)))).toBe(P.Recovery);
 store.observe(row(12n)); expect(questionPresentation(store.latest(row(12n)))).toBe(P.Recovery);
});
it("hides only verified complete resident emptiness and leaves all recovery reachable", () => {
 const query = { loaded: true, nextPageToken: "", pages: [{ token: "" }], payloadPages: [{ token: "" }] };
 expect(questionTrayEmpty([], false, query)).toBe(true);
 for (const extra of [{ loaded: false }, { loading: "refresh" }, { error: {} }, { nextPageToken: "more" }, { payloadPages: [] }]) expect(questionTrayEmpty([], false, { ...query, ...extra })).toBe(false);
 expect(questionTrayEmpty([], true, query)).toBe(false); expect(questionTrayEmpty([row(9n)], false, query)).toBe(false);
});

it("bounds lifecycle metadata and fails closed rather than returning an evicted answer capability", () => {
 const store = new QuestionPresentationStore();
 for (let index = 0; index < 4097; index++) store.observe(create(ResourceSchema, { ...row(9n), id: String(index) }));
 expect(questionPresentation(store.latest(row(9n)))).toBe(P.Recovery);
});
it("retains only closed lifecycle classifications rather than unknown source field contents", () => {
 const store = new QuestionPresentationStore();
 store.observe(row(9n, { closure: "future-closure", response: { state: "future-" + "x".repeat(2048), input: { answers: "Original retained bytes" } } }));
 const recovered = store.retained("session", new Set(["question"]))[0];
 expect(document(recovered).closure).toBe("unknown");
 expect(document(recovered).response).toEqual({ state: "unknown" });
 expect(questionPresentation(recovered, true)).toBe(P.Recovery);
});
