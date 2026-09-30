// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { EntityKind, EventAction, ResourceSchema, ResourceService, SessionService, SystemCapability, SystemService, WatchEventsResponseSchema, newRequestId } from "@delinoio/delidev-api-client";
import { document, encode, object } from "./documents";
import { subagentFixture } from "./subagent-test-fixture";
import { MutationIntents } from "./mutation";
import { SessionView } from "./session";

test("large child publications preserve root streaming and refresh only the paginated child view", async () => {
  const id = newRequestId();
  const root = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, schemaVersion: 1, revision: 1n, documentJson: encode({ name: "Original root", workspace: "general-chat", archive: "active", recovery: "none" }) });
  const updated = create(ResourceSchema, { ...root, revision: 2n, documentJson: encode({ name: "Root after child publications", workspace: "general-chat", archive: "active", recovery: "none" }) });
  // Each retained document fits the resource bound, but caching the complete
  // child inventory would overflow the transcript's independent 8 MiB bound.
  const children = Array.from({ length: 20 }, () => create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.SUBAGENT, schemaVersion: 1, revision: 1n, documentJson: encode({ observed: "x".repeat(512000) }) }));
  const message = create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.MESSAGE, schemaVersion: 1, revision: 1n, documentJson: encode({ role: "assistant", text: "Root transcript still streams" }) });
  const page = subagentFixture(id), record = document(page);
  object(record.observation).output = { native_message_id: "original-paged-message", text: "paged-child", partial: true };
  page.documentJson = encode(record);
  let published = false, childFetches = 0;
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [SystemCapability.SUBAGENT_OBSERVATION_V1] }) });
    router.service(SessionService, { listQueue: () => ({ inputs: [] }) });
    router.service(ResourceService, {
      getSnapshot: () => ({ resources: [root], cursor: "original-snapshot" }),
      getResource: request => {
        if (request.kind === EntityKind.SUBAGENT) { childFetches++; return { resource: children.find(child => child.id === request.id) }; }
        return { resource: request.kind === EntityKind.SESSION ? updated : message };
      },
      listResources: request => ({ resources: request.filter?.kind === EntityKind.SUBAGENT && published ? [page] : [] }),
      async *watchEvents(_request, context) {
        for (const resource of [...children, updated, message]) {
          if (resource === updated) published = true;
          yield create(WatchEventsResponseSchema, { id: newRequestId(), entityId: resource.id, sessionId: id, kind: resource.kind, revision: resource.revision, action: EventAction.UPDATED, cursor: `original-${resource.id}` });
        }
        if (!context.signal.aborted) await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
      },
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const mounted = render(<TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionView id={id} draft="" setDraft={() => {}} /></MutationIntents></QueryClientProvider></TransportProvider>);
  try {
    expect(await screen.findByRole("heading", { name: "Root after child publications" })).toBeTruthy();
    expect(await screen.findByText("Root transcript still streams")).toBeTruthy();
    expect(await screen.findByText("paged-child")).toBeTruthy();
    expect(childFetches).toBe(0);
    expect(screen.queryByText("Connection requires attention")).toBeNull();
    expect((screen.getByRole("button", { name: "Stop" }) as HTMLButtonElement).disabled).toBe(false);
  } finally { mounted.unmount(); client.clear(); }
}, 15000);
