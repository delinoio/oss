// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { BudgetState, EntityKind, ResourceSchema, ResourceService, SessionBudgetViewSchema, SessionService, SystemService, newRequestId } from "@delinoio/delidev-api-client";
import { document as readDocument, encode, Mode } from "./documents";
import { i18n } from "./localization";
import { MutationIntents } from "./mutation";
import { SessionView } from "./session";

function fixture(state = BudgetState.ALLOW_INCOMPLETE, problem = false) {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({
    name: "Original session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "blocked", recovery: "none",
    ...(problem ? { problem: { code: "unsupported", message: "Original installation evidence", guidance: "Original verification guidance" } } : {}),
  }) });
  const enqueue = vi.fn(async (_request: { requestId: string; sessionId: string; documentJson: Uint8Array }) => ({ change: { session } }));
  const rename = vi.fn(async () => ({ change: { session } }));
  const control = vi.fn(async () => ({ change: { session } }));
  const budget = vi.fn(() => ({ view: create(SessionBudgetViewSchema, { session, state, ...(state === BudgetState.THRESHOLD_REACHED ? { budget: { currency: "USD", threshold: "1" } } : {}) }) }));
  const list = vi.fn(async (_request: { filter?: { kind: EntityKind; pageToken: string } }) => ({ resources: [] as ReturnType<typeof create<typeof ResourceSchema>>[], nextPageToken: "" }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: budget, enqueueInput: enqueue, renameSession: rename, controlSession: control });
    router.service(ResourceService, {
      getSnapshot: () => ({ resources: [session], cursor: "original-snapshot" }),
      listResources: list,
      async *watchEvents(_request, context) {
        if (!context.signal.aborted) await new Promise<void>(resolve => context.signal.addEventListener("abort", () => resolve(), { once: true }));
      },
    });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const draft = vi.fn();
  const view = (value = "Original draft") => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><SessionView id={id} draft={value} setDraft={draft} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { session, client, view, enqueue, rename, control, budget, draft, list };
}

it("retains composer, mode and staged information edits through tool switches and language changes", async () => {
  const f = fixture();
  const mounted = render(f.view());
  const composer = await screen.findByRole("textbox", { name: "Message" });
  await screen.findByRole("heading", { name: "Original session" });
  fireEvent.change(screen.getByRole("combobox", { name: "Mode" }), { target: { value: Mode.Plan } });
  fireEvent.click(screen.getByRole("button", { name: "Info" }));
  fireEvent.click(screen.getByRole("button", { name: "Rename session" }));
  const name = screen.getByRole("textbox", { name: "Session name" });
  fireEvent.change(name, { target: { value: "Staged name" } });
  fireEvent.click(screen.getByRole("button", { name: "Files" }));
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  fireEvent.click(screen.getByRole("button", { name: "Info" }));
  expect(screen.getByRole("textbox", { name: "Session name" })).toBe(name);
  expect(name).toHaveProperty("value", "Staged name");
  fireEvent.keyDown(screen.getByRole("complementary", { name: "Session information" }), { key: "Escape" });
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Info" }));
  mounted.rerender(f.view("Retained draft"));
  expect(composer).toHaveProperty("value", "Retained draft");
  expect(screen.getByRole("combobox", { name: "Mode" })).toHaveProperty("value", Mode.Plan);
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.getByRole("textbox", { name: "메시지" })).toBe(composer);
  fireEvent.click(screen.getByRole("button", { name: "정보" }));
  expect(screen.getByRole("textbox", { name: "세션 이름" })).toBe(name);
  expect(f.enqueue).not.toHaveBeenCalled(); expect(f.rename).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled();
});

it("observes reached budgets with Info hidden and reveals the budget without resuming", async () => {
  const f = fixture(BudgetState.THRESHOLD_REACHED);
  render(f.view());
  await waitFor(() => expect(f.budget).toHaveBeenCalled());
  await waitFor(() => expect(screen.getByRole("button", { name: "Resume" })).toHaveProperty("disabled", true));
  expect(screen.queryByRole("complementary", { name: "Session information" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Show details" }));
  const info = screen.getByRole("complementary", { name: "Session information" });
  expect(within(info).getByText("Usage and budget").closest("details")).toHaveProperty("open", true);
  fireEvent.click(screen.getByRole("button", { name: "Close session information" }));
  expect(screen.getByRole("button", { name: "Resume" })).toHaveProperty("disabled", true);
  expect(f.control).not.toHaveBeenCalled();
});

it("keeps original technical evidence behind Info and opens it from the compact notice", async () => {
  const f = fixture(BudgetState.ALLOW_INCOMPLETE, true);
  render(f.view());
  await screen.findByText("Execution is blocked");
  expect(screen.queryByRole("complementary", { name: "Session information" })).toBeNull();
  expect(screen.getByText(/Original installation evidence/).closest("aside")).toHaveProperty("hidden", true);
  const opener = screen.getByRole("button", { name: "Show details" });
  fireEvent.click(opener);
  const evidence = screen.getByText(/Original installation evidence/);
  expect(evidence.closest("details")).toHaveProperty("open", true);
  expect(evidence.closest("aside")).toHaveProperty("hidden", false);
  fireEvent.keyDown(evidence, { key: "Escape" });
  expect(document.activeElement).toBe(opener);
  expect(f.control).not.toHaveBeenCalled();
});

it("retries the exact queued input after supporting panels were opened and closed", async () => {
  const f = fixture();
  f.enqueue.mockRejectedValueOnce(new ConnectError("Original receipt lost", Code.Unavailable));
  render(f.view());
  await screen.findByRole("heading", { name: "Original session" });
  fireEvent.change(screen.getByRole("combobox", { name: "Mode" }), { target: { value: Mode.Plan } });
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await screen.findByRole("button", { name: "Retry the same message" });
  const original = f.enqueue.mock.calls[0][0];
  fireEvent.click(screen.getByRole("button", { name: "Info" }));
  fireEvent.click(screen.getByRole("button", { name: "Info" }));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same message" }));
  await waitFor(() => expect(f.enqueue).toHaveBeenCalledTimes(2));
  expect(f.enqueue.mock.calls[1][0]).toEqual(original);
  expect(JSON.parse(new TextDecoder().decode(original.documentJson))).toEqual({ prompt: "Original draft", mode: Mode.Plan });
  expect(readDocument(f.session).archive).toBe("active");
});


it("appends forward transcript pages in server order without moving the composer", async () => {
  const f = fixture();
  const secondId = newRequestId(), firstId = newRequestId();
  const message = (id: string, text: string) => create(ResourceSchema, { id, sessionId: f.session.id, kind: EntityKind.MESSAGE, revision: 1n, schemaVersion: 1, documentJson: encode({ role: "user", state: "completed", text }) });
  f.list.mockImplementation(async request => request.filter?.kind === EntityKind.MESSAGE
    ? { resources: [request.filter.pageToken ? message(secondId, "Second forward message") : message(firstId, "First forward message")], nextPageToken: request.filter.pageToken ? "" : "accepted-forward-token" }
    : { resources: [], nextPageToken: "" });
  render(f.view());
  await screen.findByText("First forward message");
  const composer = screen.getByRole("textbox", { name: "Message" });
  composer.focus();
  fireEvent.click(screen.getByRole("button", { name: "Load more Conversation pages" }));
  await screen.findByText("Second forward message");
  expect(screen.getAllByText(/forward message/).map(element => element.textContent)).toEqual(["First forward message", "Second forward message"]);
  expect(document.activeElement).toBe(composer);
  expect(f.list.mock.calls.filter(([request]) => request.filter?.kind === EntityKind.MESSAGE).map(([request]) => request.filter?.pageToken)).toEqual(["", "accepted-forward-token"]);
  expect(f.enqueue).not.toHaveBeenCalled();
});
