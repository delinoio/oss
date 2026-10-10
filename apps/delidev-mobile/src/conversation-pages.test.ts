// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { EntityKind, ResourceSchema } from "@delinoio/delidev-api-client";
import { appendPage, ConversationLane, emptyPages } from "./conversation-pages";
import { documentBytes } from "./state";
const lane = ConversationLane.Queue;
const row = (id: string) => create(ResourceSchema, { kind: EntityKind.QUEUE, id, revision: 7n, sessionId: "session", schemaVersion: 1, documentJson: documentBytes({ prompt: id }) });
it("retains original order, identities and revisions across the 51st input", () => {
  const first = Array.from({ length: 50 }, (_, index) => row(`${index}`));
  const second = row("original-51");
  const pages = appendPage(appendPage(emptyPages(), { resources: first, nextPageToken: "opaque" }, "", lane, "session"), { resources: [second], nextPageToken: "" }, "opaque", lane, "session");
  expect(pages.resources).toEqual([...first, second]);
  expect(pages.resources[50]).toBe(second);
  expect(pages.nextPageToken).toBe("");
});
it("accepts a genuinely exhausted empty list", () => {
  expect(appendPage(emptyPages(), { resources: [], nextPageToken: "" }, "", lane, "session").incomplete).toBe(false);
});
it.each(["empty-continuation", "repeated-token", "wrong-token", "duplicate-page-id", "duplicate-chain-id", "wrong-session", "wrong-kind", "wrong-revision", "oversized-page", "cursor-cycle"])("rejects %s without replacing accepted observations", scenario => {
  const first = appendPage(emptyPages(), { resources: [row("original")], nextPageToken: "opaque" }, "", lane, "session");
  const page = { resources: [row("new")], nextPageToken: "" };
  let token = "opaque";
  if (scenario === "empty-continuation") { page.resources = []; page.nextPageToken = "next"; }
  if (scenario === "repeated-token") page.nextPageToken = "opaque";
  if (scenario === "wrong-token") token = "replacement";
  if (scenario === "duplicate-page-id") page.resources.push(page.resources[0]!);
  if (scenario === "duplicate-chain-id") page.resources = [row("original")];
  if (scenario === "wrong-session") page.resources[0]!.sessionId = "other";
  if (scenario === "wrong-kind") page.resources[0]!.kind = EntityKind.INTERACTION;
  if (scenario === "wrong-revision") page.resources[0]!.revision = 0n;
  if (scenario === "oversized-page") page.resources = Array.from({ length: 51 }, (_, index) => row(`extra-${index}`));
  if (scenario === "cursor-cycle") { first.tokens.push("earlier"); page.nextPageToken = "earlier"; }
  expect(() => appendPage(first, page, token, lane, "session")).toThrow();
  expect(first.resources.map(resource => resource.id)).toEqual(["original"]);
});
it.each([false, true])("stops at 1,000 inputs and reports excess continuation (%s)", excess => {
  let pages = emptyPages();
  for (let index = 0; index < 20; index++) {
    const token = pages.nextPageToken;
    pages = appendPage(pages, { resources: Array.from({ length: 50 }, (_, offset) => row(`${index}-${offset}`)), nextPageToken: index === 19 && !excess ? "" : `page-${index + 1}` }, token, lane, "session");
  }
  expect(pages.resources).toHaveLength(1000);
  expect(pages.nextPageToken).toBe("");
  expect(pages.incomplete).toBe(excess);
  expect(() => appendPage(pages, { resources: [row("excess")], nextPageToken: "" }, "", lane, "session")).toThrow();
});
it("enforces the combined document bound", () => {
  const resource = row("large"); resource.documentJson = new Uint8Array((16 << 20) + 1);
  expect(() => appendPage(emptyPages(), { resources: [resource], nextPageToken: "" }, "", lane, "session")).toThrow();
});
