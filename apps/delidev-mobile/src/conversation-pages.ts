// SPDX-License-Identifier: Apache-2.0
import { documentOf } from "./state";
import { EntityKind, supportsResourceSchema, type Resource } from "@delinoio/delidev-api-client";

export enum ConversationLane { Queue = "queue", Interactions = "interactions" }
export interface ConversationPage { resources: Resource[]; nextPageToken: string }
export interface ConversationPages { resources: Resource[]; tokens: string[]; nextPageToken: string; incomplete: boolean; bytes: number }
export const emptyPages = (): ConversationPages => ({ resources: [], tokens: [], nextPageToken: "", incomplete: false, bytes: 0 });
export function appendPage(previous: ConversationPages, page: ConversationPage, token: string, lane: ConversationLane, sessionId: string): ConversationPages {
  const limit = lane === ConversationLane.Queue ? 1_000 : 10_000;
  const kind = lane === ConversationLane.Queue ? EntityKind.QUEUE : EntityKind.INTERACTION;
  const seen = new Set(previous.resources.map(resource => resource.id));
  let bytes = previous.bytes;
  if (previous.incomplete || previous.resources.length >= limit || page.resources.length > 50 || token !== previous.nextPageToken || previous.tokens.includes(token) ||
    (page.nextPageToken && (!page.resources.length || page.nextPageToken === token || previous.tokens.includes(page.nextPageToken)))) throw new Error("invalid-conversation-page");
  for (const resource of page.resources) {
    bytes += resource.documentJson.byteLength;
    if (!resource.id || seen.has(resource.id) || resource.revision <= 0n || resource.kind !== kind || resource.sessionId !== sessionId || !supportsResourceSchema(resource) || bytes > 16 << 20) throw new Error("invalid-conversation-resource");
    documentOf(resource.documentJson);
    seen.add(resource.id);
  }
  const resources = [...previous.resources, ...page.resources];
  if (resources.length > limit) throw new Error("conversation-page-limit");
  const incomplete = resources.length === limit && !!page.nextPageToken;
  return { resources, tokens: [...previous.tokens, token], nextPageToken: incomplete ? "" : page.nextPageToken, incomplete, bytes };
}
