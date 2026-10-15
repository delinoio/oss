import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  ResourceService,
  SessionService,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  MESSAGE_RESIDENT_PAGES,
  messageRetention,
  useConversationPages,
} from "./conversation-pagination";
import { SessionActivityProvider } from "./session-activity";
import { pagesResident } from "./scroll-pagination";

it("bounds full transcript payloads and restores the exact reached forward request without caching documents", async () => {
  const sessionId = newRequestId();
  const records = Array.from({ length: 5 }, (_, index) =>
    create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.MESSAGE,
      revision: 1n,
      documentJson: encode({ text: `Page ${index}` }),
    }),
  );
  const read = vi.fn(async (request: { filter?: { pageToken: string } }) => {
    const index = Number(request.filter?.pageToken || 0);
    return {
      resources: [records[index]],
      nextPageToken: index < 4 ? String(index + 1) : "",
    };
  });
  const transport = createRouterTransport((router) =>
    router.service(ResourceService, { listResources: read }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const query = useConversationPages(EntityKind.MESSAGE, sessionId);
    return (
      <>
        <output>
          {query.rows.length}:{query.payloadPages.length}
        </output>
        <button disabled={Boolean(query.loading)} onClick={query.append}>
          Append
        </button>
        <button onClick={() => query.restore("")}>Restore initial</button>
      </>
    );
  }
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <View />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText("1:1");
  for (let index = 2; index <= 5; index++) {
    fireEvent.click(screen.getByRole("button", { name: "Append" }));
    await screen.findByText(`${index}:${Math.min(index, 3)}`);
  }
  expect(read.mock.calls.map(([request]) => request.filter?.pageToken)).toEqual(
    ["", "1", "2", "3", "4"],
  );
  fireEvent.click(screen.getByRole("button", { name: "Restore initial" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(6));
  expect(read.mock.calls[5][0].filter?.pageToken).toBe("");
  await screen.findByText("5:3");
  expect(
    client
      .getQueryCache()
      .getAll()
      .every((query) => query.state.data === null),
  ).toBe(true);
});

it("rejects a foreign additional page atomically and retries only the exact failed token", async () => {
  const sessionId = newRequestId(),
    id = newRequestId();
  const row = create(ResourceSchema, {
    id,
    sessionId,
    kind: EntityKind.MESSAGE,
    revision: 1n,
  });
  const read = vi.fn(async (request: { filter?: { pageToken: string } }) => ({
    resources: request.filter?.pageToken
      ? [{ ...row, id: newRequestId(), sessionId: newRequestId() }]
      : [row],
    nextPageToken: request.filter?.pageToken ? "" : "original-next",
  }));
  const transport = createRouterTransport((router) =>
    router.service(ResourceService, { listResources: read }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const query = useConversationPages(EntityKind.MESSAGE, sessionId);
    return (
      <>
        <output>
          {query.rows.length}:{query.error ? "failed" : "ready"}
        </output>
        <button onClick={query.append}>Append</button>
        <button onClick={query.retry}>Retry</button>
      </>
    );
  }
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <View />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText("1:ready");
  fireEvent.click(screen.getByRole("button", { name: "Append" }));
  await screen.findByText("1:failed");
  fireEvent.click(screen.getByRole("button", { name: "Append" }));
  expect(read).toHaveBeenCalledTimes(2);
  fireEvent.click(screen.getByRole("button", { name: "Retry" }));
  await waitFor(() => expect(read).toHaveBeenCalledTimes(3));
  expect(
    read.mock.calls.slice(1).map(([request]) => request.filter?.pageToken),
  ).toEqual(["original-next", "original-next"]);
});

it.each([
  EntityKind.MESSAGE,
  EntityKind.QUEUE,
  EntityKind.INTERACTION,
  EntityKind.REVIEW,
])(
  "settles initial failure loading and preserves explicit original retry for kind %s",
  async (kind) => {
    const sessionId = newRequestId();
    const read = vi
      .fn(async (_request: object) => ({
        resources: [],
        inputs: [],
        nextPageToken: "",
      }))
      .mockRejectedValueOnce(
        new ConnectError("Fixture unavailable", Code.Unavailable),
      );
    const transport = createRouterTransport((router) => {
      router.service(ResourceService, { listResources: read });
      router.service(SessionService, { listQueue: read });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const query = useConversationPages(kind, sessionId);
      return (
        <>
          <output>
            {query.isPending ? "pending" : query.error ? "failed" : "loaded"}
          </output>
          <button onClick={query.retry}>Retry</button>
        </>
      );
    }
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <View />
        </QueryClientProvider>
      </TransportProvider>,
    );
    expect(screen.getByText("pending")).toBeTruthy();
    await screen.findByText("failed");
    expect(read).toHaveBeenCalledTimes(1);
    const original = read.mock.calls[0][0];
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await screen.findByText("loaded");
    expect(read).toHaveBeenCalledTimes(2);
    expect(read.mock.calls[1][0]).toEqual(original);
  },
);

it.each(["initial", "additional"])(
  "recovers an %s failure through explicit conversation refresh without automatic retry",
  async (stage) => {
    const sessionId = newRequestId();
    const first = create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.MESSAGE,
      revision: 1n,
    });
    const second = create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.MESSAGE,
      revision: 1n,
    });
    let fail = true;
    const read = vi.fn(async (request: { filter?: { pageToken: string } }) => {
      const token = request.filter?.pageToken ?? "";
      if (fail && (stage === "initial" || token)) {
        fail = false;
        throw new ConnectError("Fixture unavailable", Code.Unavailable);
      }
      return {
        resources: token ? [second] : [first],
        nextPageToken: token ? "" : "original-next",
      };
    });
    const transport = createRouterTransport((router) =>
      router.service(ResourceService, { listResources: read }),
    );
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const query = useConversationPages(EntityKind.MESSAGE, sessionId);
      return (
        <>
          <output>
            {query.rows.length}:{query.error ? "failed" : "ready"}
          </output>
          <button onClick={query.append}>Append</button>
          <button onClick={query.refresh}>Automatic refresh</button>
          <button onClick={query.refetch}>Refresh conversation</button>
        </>
      );
    }
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <View />
        </QueryClientProvider>
      </TransportProvider>,
    );
    if (stage === "additional") {
      await screen.findByText("1:ready");
      fireEvent.click(screen.getByRole("button", { name: "Append" }));
    }
    await screen.findByText(`${stage === "initial" ? 0 : 1}:failed`);
    const before = read.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "Automatic refresh" }));
    expect(read).toHaveBeenCalledTimes(before);
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh conversation" }),
    );
    await screen.findByText(`${stage === "initial" ? 1 : 2}:ready`);
    // The explicit recovery re-reads the failed token. A failed Initial read
    // recovers from the start token, which supersedes the automatic refresh
    // that arrived while the error was held. For a failed Additional read the
    // retained refresh runs exactly once afterwards over the accepted pages;
    // nothing retries on its own.
    const reached = stage === "initial" ? 0 : 2;
    await waitFor(() =>
      expect(read).toHaveBeenCalledTimes(before + 1 + reached),
    );
    expect(read.mock.calls[before][0]).toEqual(read.mock.calls[before - 1][0]);
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(read).toHaveBeenCalledTimes(before + 1 + reached);
  },
);

it("retains one tool turn across rows 50/51 and five accepted pages under the three-payload limit", async () => {
  const { ToolTurnTranscript } = await import("./tool-turn-transcript");
  const { createRef } = await import("react");
  const sessionId = newRequestId(),
    execution = newRequestId(),
    root = createRef<HTMLDivElement>();
  const pages = Array.from({ length: 5 }, (_, page) =>
    Array.from({ length: 50 }, (_, index) =>
      create(ResourceSchema, {
        id: newRequestId(),
        sessionId,
        kind: EntityKind.MESSAGE,
        schemaVersion: 1,
        revision: 1n,
        documentJson: encode(
          index === (page === 0 ? 49 : 0)
            ? {
                role: "tool",
                execution_id: execution,
                native_thread_id: "original",
                native_turn_id: "turn",
                tool: {
                  started: {
                    kind: `original-${page}`,
                    command: { command: "DO NOT RETAIN OUTPUT IN PROJECTION" },
                  },
                },
              }
            : { role: "assistant", text: `Comment ${page}:${index}` },
        ),
      }),
    ),
  );
  const read = vi.fn(async (request: { filter?: { pageToken: string } }) => {
    const page = Number(request.filter?.pageToken || 0);
    return {
      resources: pages[page],
      nextPageToken: page < 4 ? String(page + 1) : "",
    };
  });
  const transport = createRouterTransport((router) =>
    router.service(ResourceService, { listResources: read }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View() {
    const query = useConversationPages(EntityKind.MESSAGE, sessionId);
    return (
      <>
        <output>
          {query.rows.length}:{query.payloadPages.length}
        </output>
        <output data-projection>
          {query.rows.some((row) =>
            JSON.stringify(row, (_, value) =>
              typeof value === "bigint" ? String(value) : value,
            ).includes("DO NOT RETAIN"),
          )
            ? "leaked"
            : "bounded"}
        </output>
        <button disabled={Boolean(query.loading)} onClick={query.append}>
          Append tool page
        </button>
        <div ref={root}>
          <ToolTurnTranscript
            sessionId={sessionId}
            query={query}
            live={new Map()}
            removed={new Set()}
            arrivals={[]}
            root={root}
            render={(row) => <p>{row.id}</p>}
          />
        </div>
      </>
    );
  }
  const view = render(
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <View />
      </QueryClientProvider>
    </TransportProvider>,
  );
  await screen.findByText("50:1");
  for (let page = 2; page <= 5; page++) {
    fireEvent.click(screen.getByRole("button", { name: "Append tool page" }));
    await screen.findByText(`${page * 50}:${Math.min(page, 3)}`);
  }
  expect(view.container.querySelectorAll(".tool-turn")).toHaveLength(1);
  expect(view.container.querySelectorAll(".tool-turn li")).toHaveLength(5);
  expect(screen.getByText("bounded")).toBeTruthy();
  const group = view.container.querySelector<HTMLDetailsElement>(".tool-turn")!;
  group.open = true;
  fireEvent(group, new Event("toggle"));
  expect(read).toHaveBeenCalledTimes(5);
  const firstEntry = group.querySelector<HTMLDetailsElement>("li > details")!;
  firstEntry.open = true;
  fireEvent(firstEntry, new Event("toggle"));
  fireEvent.click(firstEntry.querySelector("button")!);
  await waitFor(() => expect(read).toHaveBeenCalledTimes(6));
  expect(read.mock.calls.map(([request]) => request.filter?.pageToken)).toEqual(
    ["", "1", "2", "3", "4", ""],
  );
  await screen.findByText("250:3");
  expect(view.container.querySelector(".tool-turn")).toBe(group);
  expect(group.open).toBe(true);
  expect(firstEntry.open).toBe(true);
});

it.each([EntityKind.MESSAGE, EntityKind.INTERACTION, EntityKind.QUEUE])(
  "does not report %s payload pages resident after eviction behind a terminal token",
  async (kind) => {
    const sessionId = newRequestId();
    const records = Array.from({ length: 4 }, (_, index) =>
      create(ResourceSchema, {
        id: newRequestId(),
        sessionId,
        kind,
        revision: 1n,
        documentJson: encode({ text: `Page ${index}` }),
      }),
    );
    const page = (token: string | undefined) => {
      const index = Number(token || 0);
      return {
        rows: [records[index]!],
        nextPageToken: index < records.length - 1 ? String(index + 1) : "",
      };
    };
    const transport = createRouterTransport((router) => {
      router.service(ResourceService, {
        listResources: (request: { filter?: { pageToken: string } }) => {
          const { rows, nextPageToken } = page(request.filter?.pageToken);
          return { resources: rows, nextPageToken };
        },
      });
      router.service(SessionService, {
        listQueue: (request: { pageToken: string }) => {
          const { rows, nextPageToken } = page(request.pageToken);
          return { inputs: rows, nextPageToken };
        },
      });
    });
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    function View() {
      const query = useConversationPages(kind, sessionId);
      return (
        <>
          <output>
            {query.pages.length}:{query.payloadPages.length}:
            {query.nextPageToken || "terminal"}:
            {pagesResident(query) ? "resident" : "evicted"}
          </output>
          <button disabled={Boolean(query.loading)} onClick={query.append}>
            Append
          </button>
          <button onClick={() => query.restore("")}>Restore initial</button>
        </>
      );
    }
    render(
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <View />
        </QueryClientProvider>
      </TransportProvider>,
    );
    await screen.findByText("1:1:1:resident");
    for (let index = 2; index <= 4; index++) {
      fireEvent.click(screen.getByRole("button", { name: "Append" }));
      await screen.findByText(
        new RegExp(
          `^${index}:${Math.min(index, 3)}:${index < 4 ? index : "terminal"}:${
            index <= 3 ? "resident" : "evicted"
          }$`,
        ),
      );
    }
    // Restoration stays reachable; the bounded window never reports complete.
    fireEvent.click(screen.getByRole("button", { name: "Restore initial" }));
    await waitFor(() =>
      expect(screen.getByRole("status").textContent).toMatch(
        /^4:3:terminal:(resident|evicted)$/,
      ),
    );
  },
);

it("retains up to twenty message pages across inactivity only for the opted-in transcript chain", async () => {
  expect(messageRetention).toEqual({
    residentPages: MESSAGE_RESIDENT_PAGES,
    retainPayloadOnSuspend: true,
  });
  expect(MESSAGE_RESIDENT_PAGES).toBe(20);
  const sessionId = newRequestId();
  const records = Array.from({ length: 4 }, (_, index) =>
    create(ResourceSchema, {
      id: newRequestId(),
      sessionId,
      kind: EntityKind.MESSAGE,
      revision: 1n,
      documentJson: encode({ text: `Page ${index}` }),
    }),
  );
  const read = vi.fn(async (request: { filter?: { pageToken: string } }) => {
    const index = Number(request.filter?.pageToken || 0);
    return {
      resources: [records[index]],
      nextPageToken: index < 3 ? String(index + 1) : "",
    };
  });
  const transport = createRouterTransport((router) =>
    router.service(ResourceService, { listResources: read }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  function View({ retained }: { retained: boolean }) {
    const query = useConversationPages(
      EntityKind.MESSAGE,
      sessionId,
      true,
      50,
      undefined,
      undefined,
      undefined,
      undefined,
      retained ? messageRetention : undefined,
    );
    return (
      <>
        <output>
          {query.rows.length}:{query.payloadPages.length}
        </output>
        <button disabled={Boolean(query.loading)} onClick={query.append}>
          Append
        </button>
      </>
    );
  }
  function Harness({
    active,
    retained,
  }: {
    active: boolean;
    retained: boolean;
  }) {
    return (
      <TransportProvider transport={transport}>
        <QueryClientProvider client={client}>
          <SessionActivityProvider active={active}>
            <View retained={retained} />
          </SessionActivityProvider>
        </QueryClientProvider>
      </TransportProvider>
    );
  }
  // Default chains keep three pages and dispose payloads while inactive.
  const plain = render(<Harness active retained={false} />);
  await screen.findByText("1:1");
  for (let index = 2; index <= 4; index++) {
    fireEvent.click(screen.getByRole("button", { name: "Append" }));
    await screen.findByText(`${index}:${Math.min(index, 3)}`);
  }
  plain.rerender(<Harness active={false} retained={false} />);
  await screen.findByText("4:0");
  plain.unmount();
  read.mockClear();
  // The opted-in chain keeps all four pages while inactive and reads nothing.
  const kept = render(<Harness active retained />);
  await screen.findByText("1:1");
  for (let index = 2; index <= 4; index++) {
    fireEvent.click(screen.getByRole("button", { name: "Append" }));
    await screen.findByText(`${index}:${index}`);
  }
  read.mockClear();
  kept.rerender(<Harness active={false} retained />);
  await screen.findByText("4:4");
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(read).not.toHaveBeenCalled();
  // Return refresh holds bodies visible until it settles atomically.
  kept.rerender(<Harness active retained />);
  expect(screen.getByText("4:4")).toBeTruthy();
  await waitFor(() => expect(read).toHaveBeenCalledTimes(4));
  await screen.findByText("4:4");
});
