import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, ResourceSchema, ResourceService, SessionService, newRequestId } from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { useConversationPages } from "./conversation-pagination";

it("bounds full transcript payloads and restores the exact reached forward request without caching documents", async () => {
  const sessionId = newRequestId();
  const records = Array.from({ length: 5 }, (_, index) => create(ResourceSchema, { id: newRequestId(), sessionId, kind: EntityKind.MESSAGE, revision: 1n, documentJson: encode({ text: `Page ${index}` }) }));
  const read = vi.fn(async (request: { filter?: { pageToken: string } }) => {
    const index = Number(request.filter?.pageToken || 0);
    return { resources: [records[index]], nextPageToken: index < 4 ? String(index + 1) : "" };
  });
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    const query = useConversationPages(EntityKind.MESSAGE, sessionId);
    return <><output>{query.rows.length}:{query.payloadPages.length}</output><button disabled={Boolean(query.loading)} onClick={query.append}>Append</button><button onClick={() => query.restore("")}>Restore initial</button></>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><View /></QueryClientProvider></TransportProvider>);
  await screen.findByText("1:1");
  for (let index = 2; index <= 5; index++) {
    fireEvent.click(screen.getByRole("button", { name: "Append" }));
    await screen.findByText(`${index}:${Math.min(index, 3)}`);
  }
  expect(read.mock.calls.map(([request]) => request.filter?.pageToken)).toEqual(["", "1", "2", "3", "4"]);
  fireEvent.click(screen.getByRole("button", { name: "Restore initial" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(6));
  expect(read.mock.calls[5][0].filter?.pageToken).toBe("");
  await screen.findByText("5:3");
  expect(client.getQueryCache().getAll().every(query => query.state.data === null)).toBe(true);
});

it("rejects a foreign additional page atomically and retries only the exact failed token", async () => {
  const sessionId = newRequestId(), id = newRequestId();
  const row = create(ResourceSchema, { id, sessionId, kind: EntityKind.MESSAGE, revision: 1n });
  const read = vi.fn(async (request: { filter?: { pageToken: string } }) => ({ resources: request.filter?.pageToken ? [{ ...row, id: newRequestId(), sessionId: newRequestId() }] : [row], nextPageToken: request.filter?.pageToken ? "" : "original-next" }));
  const transport = createRouterTransport(router => router.service(ResourceService, { listResources: read }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    const query = useConversationPages(EntityKind.MESSAGE, sessionId);
    return <><output>{query.rows.length}:{query.error ? "failed" : "ready"}</output><button onClick={query.append}>Append</button><button onClick={query.retry}>Retry</button></>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><View /></QueryClientProvider></TransportProvider>);
  await screen.findByText("1:ready");
  fireEvent.click(screen.getByRole("button", { name: "Append" }));
  await screen.findByText("1:failed");
  fireEvent.click(screen.getByRole("button", { name: "Append" }));
  expect(read).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  expect(read.mock.calls.slice(1).map(([request]) => request.filter?.pageToken)).toEqual(["original-next", "original-next"]);
});


it.each([EntityKind.MESSAGE, EntityKind.QUEUE, EntityKind.INTERACTION, EntityKind.REVIEW])("settles initial failure loading and preserves explicit original retry for kind %s", async kind => {
  const sessionId = newRequestId();
  const read = vi.fn(async (_request: object) => ({ resources: [], inputs: [], nextPageToken: "" }))
    .mockRejectedValueOnce(new ConnectError("Fixture unavailable", Code.Unavailable));
  const transport = createRouterTransport(router => {
    router.service(ResourceService, { listResources: read });
    router.service(SessionService, { listQueue: read });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function View() {
    const query = useConversationPages(kind, sessionId);
    return <><output>{query.isPending ? "pending" : query.error ? "failed" : "loaded"}</output><button onClick={query.retry}>Retry</button></>;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><View /></QueryClientProvider></TransportProvider>);
  expect(screen.getByText("pending")).toBeTruthy();
  await screen.findByText("failed");
  expect(read).toHaveBeenCalledTimes(1);
  const original = read.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await screen.findByText("loaded");
  expect(read).toHaveBeenCalledTimes(2);
  expect(read.mock.calls[1][0]).toEqual(original);
});
