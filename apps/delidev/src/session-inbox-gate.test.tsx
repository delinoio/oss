// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  InboxService,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  newRequestId,
  type MarkSessionInboxReadRequest,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useConversationPages } from "./conversation-pagination";
import { MutationIntents } from "./mutation";
import {
  SessionInboxReadBoundary,
  useSessionInboxRead,
} from "./session-inbox-read";

const native = vi.hoisted(() => ({ invoke: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({
  isTauri: () => true,
  invoke: native.invoke,
}));
vi.mock("@tauri-apps/api/event", () => ({ listen: async () => () => {} }));
beforeEach(() => {
  vi.spyOn(document, "hasFocus").mockImplementation(() => true);
  vi.spyOn(document, "visibilityState", "get").mockImplementation(
    () => "visible",
  );
  native.invoke.mockReset();
  native.invoke.mockImplementation(async (command: string) =>
    command === "begin_inbox_badge"
      ? "scope"
      : command === "read_inbox_badge_selection"
        ? { scope: "scope", generation: 1 }
        : undefined,
  );
});
afterEach(() => vi.restoreAllMocks());

// Mirrors the transcript terms of the Session conversation Inbox gate.
function Conversation({ sessionId, active }: { sessionId: string; active: boolean }) {
  const messages = useConversationPages(EntityKind.MESSAGE, sessionId, active);
  useSessionInboxRead(
    sessionId,
    active,
    Boolean(
      messages.loaded &&
      !messages.error &&
      !messages.isFetching &&
      messages.observed,
    ),
  );
  return (
    <>
      <output>
        {messages.rows.length}:{messages.payloadPages.length}:
        {String(messages.observed)}:{messages.isFetching ? "busy" : "idle"}
      </output>
      <button onClick={messages.append}>Append</button>
    </>
  );
}

it("sends exactly one acknowledgment per reselected visit after the first payload page was evicted", async () => {
  const sessionId = newRequestId();
  const records = Array.from({ length: 6 }, (_, index) =>
    create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.MESSAGE,
      revision: 1n,
      documentJson: encode({ text: `Page ${index}` }),
    }),
  );
  const reads: string[] = [];
  const acknowledgments: MarkSessionInboxReadRequest[] = [];
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => ({
        capabilities: [SystemCapability.SESSION_INBOX_READ_V1],
      }),
    });
    router.service(ResourceService, {
      listResources: (request) => {
        const token = request.filter?.pageToken ?? "";
        reads.push(token);
        const index = Number(token || 0);
        return {
          resources: [records[index]],
          nextPageToken: index < 5 ? String(index + 1) : "",
        };
      },
    });
    router.service(InboxService, {
      markSessionInboxRead: (request) => {
        acknowledgments.push(request);
        return {
          requestId: request.requestId,
          sessionId: request.sessionId,
          markedCount: 1n,
          observedAt: "2026-10-11T00:00:00Z",
        };
      },
      getUnreadInboxCount: () => ({
        unreadCount: 0n,
        observedAt: "2026-10-11T00:00:00Z",
      }),
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = (active: boolean) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <SessionInboxReadBoundary ready>
            <Conversation sessionId={sessionId} active={active} />
          </SessionInboxReadBoundary>
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  const mounted = render(view(true));
  await screen.findByText("1:1:true:idle");
  await waitFor(() => expect(acknowledgments).toHaveLength(1));
  // Loading older pages inside the visit never acknowledges again.
  for (let index = 2; index <= 6; index++) {
    await act(async () => {
      screen.getByRole("button", { name: "Append" }).click();
    });
    await screen.findByText(`${index}:${Math.min(index, 3)}:true:idle`);
  }
  expect(acknowledgments).toHaveLength(1);
  // Reselecting the same loaded Session starts a new visit; the successful
  // refresh proves the load although the empty-token payload stays evicted.
  mounted.rerender(view(false));
  mounted.rerender(view(true));
  await waitFor(() => expect(acknowledgments).toHaveLength(2));
  await screen.findByText("6:3:true:idle");
  expect(acknowledgments[1].requestId).not.toBe(acknowledgments[0].requestId);
  expect(acknowledgments[1].sessionId).toBe(sessionId);
  await act(async () => {
    await client.invalidateQueries();
  });
  expect(acknowledgments).toHaveLength(2);
});
