// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  SessionService,
  newRequestId,
  type MoveQueuedInputRequest,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import { MutationIntents } from "./mutation";
import { WaitingQueue } from "./waiting-queue";

function fixture() {
  const session = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SESSION,
    schemaVersion: 1,
    revision: 1n,
    documentJson: encode({ archive: "active" }),
  });
  const rows = ["First", "Second", "Third"].map((prompt, index) =>
    create(ResourceSchema, {
      id: newRequestId(),
      sessionId: session.id,
      kind: EntityKind.QUEUE,
      schemaVersion: 1,
      revision: BigInt(index + 3),
      documentJson: encode({
        prompt,
        sequence: index + 1,
        delivery: "queued",
        mode: "execute",
      }),
    }),
  );
  let visible = rows.slice();
  const read = vi.fn(async (_request?: { pageToken: string }) => ({
    inputs: visible,
    nextPageToken: "",
    currentQueueGeneration: 9007199254740993n,
    waitingCount: visible.length,
  }));
  const move = vi.fn(async (request: MoveQueuedInputRequest) => ({
    change: { requestId: request.mutation!.requestId, session },
    currentQueueGeneration: 9007199254740994n,
  }));
  const transport = createRouterTransport((router) =>
    router.service(SessionService, {
      listWaitingQueue: read,
      moveQueuedInput: move,
    }),
  );
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const view = (revision = "1", excludeInputIds?: ReadonlySet<string>) => (
    <TransportProvider transport={transport}>
      <QueryClientProvider client={client}>
        <MutationIntents>
          <WaitingQueue
            sessionId={session.id}
            session={session}
            active
            revision={revision}
            drafts={new Map()}
            saveDraft={() => {}}
            readOnly={false}
            refreshHistory={() => {}}
            excludeInputIds={excludeInputIds}
          />
        </MutationIntents>
      </QueryClientProvider>
    </TransportProvider>
  );
  return {
    session,
    rows,
    read,
    move,
    view,
    replace: (next: typeof rows) => {
      visible = next;
    },
  };
}
it("uses authoritative move-before revisions and exact bigint generations without optimistic ordering", async () => {
  const f = fixture();
  let acknowledge: () => void = () => {};
  f.move.mockImplementationOnce(async (request) => {
    await new Promise<void>((resolve) => {
      acknowledge = resolve;
    });
    return {
      change: { requestId: request.mutation!.requestId, session: f.session },
      currentQueueGeneration: 9007199254740994n,
    };
  });
  render(f.view());
  await screen.findByText("Third");
  const second = screen.getByText("Second").closest("article")!;
  fireEvent.click(
    within(second).getByRole("button", { name: "More input actions" }),
  );
  fireEvent.click(within(second).getByRole("menuitem", { name: "Move up" }));
  await waitFor(() => expect(f.move).toHaveBeenCalledOnce());
  expect(f.move.mock.calls[0][0]).toMatchObject({
    mutation: { id: f.rows[1].id, expectedRevision: 4n },
    sessionId: f.session.id,
    expectedQueueGeneration: 9007199254740993n,
    beforeInputId: f.rows[0].id,
    beforeInputRevision: 3n,
  });
  expect(
    screen
      .getAllByRole("article")
      .map((row) => row.querySelector(".queue-preview")?.textContent),
  ).toEqual(["First", "Second", "Third"]);
  f.replace([f.rows[1], f.rows[0], f.rows[2]]);
  await act(async () => acknowledge());
  await waitFor(() =>
    expect(
      screen
        .getAllByRole("article")
        .map((row) => row.querySelector(".queue-preview")?.textContent),
    ).toEqual(["Second", "First", "Third"]),
  );
  expect(screen.getByRole("status").textContent).toBe("Waiting input moved.");
});
it("retains an uncertain movement outside retired rows and retries its original request", async () => {
  const f = fixture();
  f.move.mockRejectedValueOnce(
    new ConnectError("Unknown original outcome", Code.Unavailable),
  );
  const view = render(f.view());
  await screen.findByText("Third");
  const third = screen.getByText("Third").closest("article")!;
  fireEvent.click(
    within(third).getByRole("button", { name: "More input actions" }),
  );
  fireEvent.click(within(third).getByRole("menuitem", { name: "Move up" }));
  await screen.findByRole("button", { name: "Retry the same movement" });
  const original = f.move.mock.calls[0][0];
  f.replace([]);
  view.rerender(f.view("2"));
  await waitFor(() => expect(screen.queryByText("Third")).toBeNull());
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same movement" }),
  );
  await waitFor(() => expect(f.move).toHaveBeenCalledTimes(2));
  expect(f.move.mock.calls[1][0]).toEqual(original);
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry the same movement" }),
    ).toBeNull(),
  );
});

it("retains movement when the response has no original acknowledgement", async () => {
  const f = fixture();
  f.move.mockImplementationOnce(
    async () =>
      ({ currentQueueGeneration: 9007199254740994n }) as Awaited<
        ReturnType<typeof f.move>
      >,
  );
  render(f.view());
  await screen.findByText("Third");
  const second = screen.getByText("Second").closest("article")!;
  fireEvent.click(
    within(second).getByRole("button", { name: "More input actions" }),
  );
  fireEvent.click(within(second).getByRole("menuitem", { name: "Move up" }));
  await screen.findByRole("button", { name: "Retry the same movement" });
  expect(screen.getByRole("status").textContent).not.toBe(
    "Waiting input moved.",
  );
  const original = f.move.mock.calls[0][0];
  fireEvent.click(
    screen.getByRole("button", { name: "Retry the same movement" }),
  );
  await waitFor(() => expect(f.move).toHaveBeenCalledTimes(2));
  expect(f.move.mock.calls[1][0]).toEqual(original);
  await waitFor(() =>
    expect(
      screen.queryByRole("button", { name: "Retry the same movement" }),
    ).toBeNull(),
  );
});

it("keeps the empty queue hidden while an automatic reread is pending", async () => {
  const f = fixture();
  f.replace([]);
  const view = render(f.view());
  await waitFor(() => expect(f.read).toHaveBeenCalledOnce());
  await waitFor(() =>
    expect(
      view.container
        .querySelector(".queue-compact-list")
        ?.hasAttribute("hidden"),
    ).toBe(true),
  );
  let release: () => void = () => {};
  f.read.mockImplementationOnce(async () => {
    await new Promise<void>((resolve) => {
      release = resolve;
    });
    return {
      inputs: [],
      nextPageToken: "",
      currentQueueGeneration: 1n,
      waitingCount: 0,
    };
  });
  view.rerender(f.view("2"));
  await waitFor(() => expect(f.read).toHaveBeenCalledTimes(2));
  expect(
    view.container.querySelector(".queue-compact-list")?.hasAttribute("hidden"),
  ).toBe(true);
  expect(screen.getByText("Loading input queue…").className).toContain(
    "sidebar-sr-only",
  );
  await act(async () => release());
  await waitFor(() =>
    expect(screen.queryByText("Loading input queue…")).toBeNull(),
  );
});

it("keeps the end drop inside the final row and sends one move to the end", async () => {
  const f = fixture();
  const view = render(f.view());
  await screen.findByText("Third");
  expect(view.container.querySelector(".queue-drop-end")).toBeNull();

  const handle = screen
    .getByText("First")
    .closest("article")!
    .querySelector<HTMLButtonElement>(".queue-drag-handle")!;
  fireEvent.dragStart(handle, { dataTransfer: { setData: vi.fn() } });
  const target = view.container.querySelector(".queue-drop-end")!;
  expect(target.parentElement).toBe(
    screen.getByText("Third").closest(".queue-drop-row"),
  );
  fireEvent.dragOver(target);
  expect(target.classList.contains("queue-insertion")).toBe(true);
  expect(target.parentElement?.classList.contains("queue-insertion")).toBe(
    false,
  );
  fireEvent.drop(target);
  await waitFor(() => expect(f.move).toHaveBeenCalledOnce());
  expect(f.move.mock.calls[0][0]).toMatchObject({
    mutation: { id: f.rows[0].id, expectedRevision: 3n },
    sessionId: f.session.id,
    expectedQueueGeneration: 9007199254740993n,
    beforeInputId: "",
    beforeInputRevision: 0n,
  });
  fireEvent.dragEnd(handle);
  expect(view.container.querySelector(".queue-drop-end")).toBeNull();
});

it("omits the excluded original input while later waiting inputs keep their controls", async () => {
  const f = fixture();
  render(f.view("1", new Set([f.rows[0].id])));
  await screen.findByText("Second");
  expect(screen.queryByText("First")).toBeNull();
  expect(screen.getByText("Third")).toBeDefined();
});
it("hides the waiting list when only the excluded original input is waiting", async () => {
  const f = fixture();
  f.replace([f.rows[0]]);
  render(f.view("1", new Set([f.rows[0].id])));
  await waitFor(() => expect(f.read).toHaveBeenCalled());
  expect(screen.queryByText("First")).toBeNull();
});

it("keeps the end-drop target on the last visible row when the excluded input is last", async () => {
  const f = fixture();
  const view = render(f.view("1", new Set([f.rows[2].id])));
  await screen.findByText("Second");
  expect(screen.queryByText("Third")).toBeNull();
  const handle = screen
    .getByText("First")
    .closest("article")!
    .querySelector<HTMLButtonElement>(".queue-drag-handle")!;
  fireEvent.dragStart(handle, { dataTransfer: { setData: vi.fn() } });
  const target = view.container.querySelector(".queue-drop-end")!;
  expect(target.parentElement).toBe(
    screen.getByText("Second").closest(".queue-drop-row"),
  );
  fireEvent.drop(target);
  await waitFor(() => expect(f.move).toHaveBeenCalledOnce());
  expect(f.move.mock.calls[0][0]).toMatchObject({
    mutation: { id: f.rows[0].id },
    beforeInputId: "",
  });
});
it("continues a page-boundary move down using the filtered order", async () => {
  const f = fixture();
  const [first, second, hidden] = f.rows;
  const last = create(ResourceSchema, {
    ...hidden,
    id: newRequestId(),
    documentJson: encode({
      prompt: "Fourth",
      sequence: 4,
      delivery: "queued",
      mode: "execute",
    }),
  });
  f.read.mockImplementation(async (request?: { pageToken: string }) => ({
    inputs: request?.pageToken ? [hidden, last] : [first, second],
    nextPageToken: request?.pageToken ? "" : "page-2",
    currentQueueGeneration: 9007199254740993n,
    waitingCount: 4,
  }));
  render(f.view("1", new Set([hidden.id])));
  await screen.findByText("Second");
  const row = screen.getByText("Second").closest("article")!;
  fireEvent.click(
    within(row).getByRole("button", { name: "More input actions" }),
  );
  fireEvent.click(within(row).getByRole("menuitem", { name: "Move down" }));
  await waitFor(() => expect(f.move).toHaveBeenCalledOnce());
  // The hidden row must not become the anchor: the target is the visible end.
  expect(f.move.mock.calls[0][0]).toMatchObject({
    mutation: { id: second.id },
    beforeInputId: "",
    beforeInputRevision: 0n,
  });
});
