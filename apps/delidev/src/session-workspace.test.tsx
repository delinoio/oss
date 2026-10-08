import { sessionInputReceipt } from "./test-session-input";
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

function fixture(state = BudgetState.ALLOW_INCOMPLETE, problem = false, extra: Record<string, unknown> = {}) {
  const id = newRequestId();
  const session = create(ResourceSchema, { id, sessionId: id, kind: EntityKind.SESSION, revision: 7n, schemaVersion: 1, documentJson: encode({
    name: "Original session", workspace: "general-chat", outcome: "stopped", archive: "active", dispatch: "blocked", recovery: "none",
    ...extra,
    ...(problem ? { problem: { code: "unsupported", message: "Original installation evidence", guidance: "Original verification guidance" } } : {}),
  }) });
  const enqueue = vi.fn(async (request: { requestId: string; sessionId: string; documentJson: Uint8Array }) => sessionInputReceipt(session,request));
  const rename = vi.fn(async () => ({ change: { session } }));
  const recover = vi.fn(async (_request: unknown) => ({ change: { session } }));
  const control = vi.fn(async () => ({ change: { session } }));
  const budget = vi.fn(() => ({ view: create(SessionBudgetViewSchema, { session, state, ...(state === BudgetState.THRESHOLD_REACHED ? { budget: { currency: "USD", threshold: "1" } } : {}) }) }));
  const list = vi.fn(async (_request: { filter?: { kind: EntityKind; pageToken: string } }) => ({ resources: [] as ReturnType<typeof create<typeof ResourceSchema>>[], nextPageToken: "" }));
  const transport = createRouterTransport(router => {
    router.service(SystemService, { getStatus: () => ({ capabilities: [] }) });
    router.service(SessionService, { listQueue: () => ({ inputs: [] }), getSessionBudget: budget, enqueueInput: enqueue, renameSession: rename, controlSession: control, recoverSessionExecution: recover });
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
  return { session, client, view, enqueue, rename, control, recover, budget, draft, list };
}

it("retains composer, mode and staged information edits through tool switches and language changes", async () => {
  const f = fixture();
  const mounted = render(f.view());
  const composer = await screen.findByRole("textbox", { name: "Message" });
  await screen.findByRole("heading", { name: "Original session" });
  fireEvent.click(screen.getByRole("checkbox", { name: "Plan Mode" }));
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
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Files" }));
  expect(screen.getByRole("complementary", { name: "Session information" })).toHaveProperty("hidden", false);
  mounted.rerender(f.view("Retained draft"));
  expect(composer).toHaveProperty("value", "Retained draft");
  expect(screen.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", true);
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.getByRole("textbox", { name: "메시지" })).toBe(composer);
  fireEvent.click(screen.getByRole("button", { name: "정보" }));
  expect(screen.getByRole("textbox", { name: "세션 이름" })).toBe(name);
  expect(f.enqueue).not.toHaveBeenCalled(); expect(f.rename).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled();
});

it("submits Execute, Plan and Execute again only through the explicit composer action", async () => {
  const f = fixture(); render(f.view());
  await screen.findByRole("heading", { name: "Original session" });
  const mode = screen.getByRole("checkbox", { name: "Plan Mode" });
  expect(mode).toHaveProperty("checked", false);
  for (const [index, expected] of [Mode.Execute, Mode.Plan, Mode.Execute].entries()) {
    if (index) { fireEvent.click(mode); expect(f.enqueue).toHaveBeenCalledTimes(index); }
    fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
    await waitFor(() => expect(f.enqueue).toHaveBeenCalledTimes(index + 1));
    expect(JSON.parse(new TextDecoder().decode(f.enqueue.mock.calls[index][0].documentJson)).mode).toBe(expected);
    await waitFor(() => expect(mode).toHaveProperty("disabled", false));
  }
});

it("locks the checked mode while its original enqueue request is pending", async () => {
  const f = fixture(); let finish!: (result: ReturnType<typeof sessionInputReceipt>) => void;
  f.enqueue.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
  render(f.view()); await screen.findByRole("heading", { name: "Original session" });
  const mode = screen.getByRole("checkbox", { name: "Plan Mode" }); fireEvent.click(mode);
  expect(f.enqueue).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await waitFor(() => expect(f.enqueue).toHaveBeenCalledTimes(1));
  expect(mode).toHaveProperty("checked", true); expect(mode).toHaveProperty("disabled", true);
  expect(JSON.parse(new TextDecoder().decode(f.enqueue.mock.calls[0][0].documentJson)).mode).toBe(Mode.Plan);
  await act(async () => finish(sessionInputReceipt(f.session, f.enqueue.mock.calls[0][0])));
  await waitFor(() => expect(mode).toHaveProperty("disabled", false)); expect(mode).toHaveProperty("checked", true);
});

it("observes reached budgets in persistent Info and reveals the budget without closing a tool", async () => {
  const f = fixture(BudgetState.THRESHOLD_REACHED);
  render(f.view());
  await waitFor(() => expect(f.budget).toHaveBeenCalled());
  await waitFor(() => expect(screen.getByRole("button", { name: "Resume" })).toHaveProperty("disabled", true));
  expect(screen.getByRole("complementary", { name: "Session information" })).toHaveProperty("hidden", false);
  fireEvent.click(screen.getByRole("button", { name: "Files" }));
  fireEvent.click(screen.getByRole("button", { name: "Show details" }));
  expect(screen.getByRole("button", { name: "Files" }).getAttribute("aria-expanded")).toBe("true");
  const info = screen.getByRole("complementary", { name: "Session information" });
  expect(within(info).getByText("Usage and budget").closest("details")).toHaveProperty("open", true);
  fireEvent.keyDown(info, { key: "Escape" });
  expect(info).toHaveProperty("hidden", false);
  expect(screen.getByRole("button", { name: "Resume" })).toHaveProperty("disabled", true);
  expect(f.control).not.toHaveBeenCalled();
});

it("keeps original technical evidence in persistent Info and reveals it from the compact notice", async () => {
  const f = fixture(BudgetState.ALLOW_INCOMPLETE, true);
  render(f.view());
  await screen.findByText("Execution is blocked");
  expect(screen.getByRole("complementary", { name: "Session information" })).toHaveProperty("hidden", false);
  expect(screen.getByText(/Original installation evidence/).closest("aside")).toHaveProperty("hidden", false);
  const opener = screen.getByRole("button", { name: "Show details" });
  fireEvent.click(opener);
  const evidence = screen.getByText(/Original installation evidence/);
  expect(evidence.closest("details")).toHaveProperty("open", true);
  expect(evidence.closest("aside")).toHaveProperty("hidden", false);
  fireEvent.keyDown(evidence, { key: "Escape" });
  expect(document.activeElement).toBe(evidence.closest(".session-information-evidence"));
  expect(f.control).not.toHaveBeenCalled();
});

it("retries the exact queued input after supporting panels were opened and closed", async () => {
  const f = fixture();
  f.enqueue.mockRejectedValueOnce(new ConnectError("Original receipt lost", Code.Unavailable));
  render(f.view());
  await screen.findByRole("heading", { name: "Original session" });
  fireEvent.click(screen.getByRole("checkbox", { name: "Plan Mode" }));
  fireEvent.click(screen.getByRole("button", { name: "Queue message" }));
  await screen.findByRole("button", { name: "Retry the same message" });
  const original = f.enqueue.mock.calls[0][0];
  expect(screen.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("disabled", true);
  expect(screen.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", true);
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

it("shows original uncertain startup recovery beside the conversation with one confirmation controller", async () => {
  const execution = newRequestId(), job = newRequestId();
  const f = fixture(BudgetState.ALLOW_INCOMPLETE, false, { dispatch: "paused", recovery: "required", execution: { execution_id: execution }, initial_execution: { id: execution }, startup: { job_id: job, execution_id: execution, failure: { state: 3, phase: 5, harness: "codex", problem_code: "recovery_required", correlation_id: job, input_delivery: 4, cleanup: 2 } } });
  render(f.view("Retained first draft"));
  const recovery = await screen.findByRole("button", { name: "Reconcile original execution" });
  expect(screen.getByRole("complementary", { name: "Session information" })).toHaveProperty("hidden", false);
  expect(screen.getByRole("button", { name: "Resume" })).toHaveProperty("disabled", true);
  expect(f.recover).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Files" }));
  fireEvent.click(recovery);
  expect(screen.getByRole("button", { name: "Files" }).getAttribute("aria-expanded")).toBe("true");
  expect(f.recover).not.toHaveBeenCalled();
  expect(screen.getByRole("complementary", { name: "Session information" })).toBeTruthy();
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  const confirmation = screen.getByRole("button", { name: "Confirm selected recovery action" });
  fireEvent.keyDown(confirmation, { key: "Escape" });
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Files" }));
  fireEvent.click(screen.getByRole("button", { name: "Info" }));
  expect(screen.getByRole("button", { name: "Confirm selected recovery action" })).toBe(confirmation);
  expect(screen.getAllByRole("button", { name: "Reconcile original execution" })).toHaveLength(1);
  expect(screen.getByRole("button", { name: "Reconcile original execution" })).toBe(recovery);
  fireEvent.click(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  await waitFor(() => expect(f.recover).toHaveBeenCalledTimes(1));
  expect(f.recover.mock.calls[0][0]).toMatchObject({ mutation: { id: f.session.id, expectedRevision: 7n }, expectedExecutionId: execution });
  expect(screen.getByRole("textbox", { name: "Message" })).toHaveProperty("value", "Retained first draft");
  expect(f.control).not.toHaveBeenCalled(); expect(f.enqueue).not.toHaveBeenCalled();
});

it("keeps malformed startup evidence visible and does not grant retry", async () => {
  const f = fixture(BudgetState.ALLOW_INCOMPLETE, false, { dispatch: "paused", startup: { failure: { state: 2, input_delivery: 1, cleanup: 1, raw_output: "private-native-output" } } });
  render(f.view());
  await screen.findByText(/Startup evidence is unavailable/);
  expect(screen.queryByText(/private-native-output/)).toBeNull();
  expect(screen.getByRole("button", { name: "Resume" })).toHaveProperty("disabled", true);
  expect(f.control).not.toHaveBeenCalled();
});


it("keeps Info independent of every temporary tool and restores the original tool opener", async () => {
  const f = fixture(); render(f.view());
  await screen.findByRole("heading", { name: "Original session" });
  const info = screen.getByRole("complementary", { name: "Session information" });
  const heading = within(info).getByRole("heading", { name: "Session information" });
  const composer = screen.getByRole("textbox", { name: "Message" });
  expect(within(info).getByText("Status and recovery").closest("details")).toHaveProperty("open", true);
  for (const details of info.querySelectorAll(".session-information-section")) expect(details).toHaveProperty("open", false);
  expect(screen.queryByRole("button", { name: "Close session information" })).toBeNull();
  for (const name of ["Files", "Diff", "Terminals", "Browser", "Diagnostics"]) {
    const opener = screen.getByRole("button", { name });
    fireEvent.click(opener);
    expect(opener.getAttribute("aria-expanded")).toBe("true");
    expect(info).toHaveProperty("hidden", false);
    fireEvent.click(screen.getByRole("button", { name: "Info" }));
    expect(document.activeElement).toBe(heading);
    expect(opener.getAttribute("aria-expanded")).toBe("true");
    fireEvent.keyDown(heading, { key: "Escape" });
    expect(document.activeElement).toBe(opener);
    expect(opener.getAttribute("aria-expanded")).toBe("false");
    expect(info).toHaveProperty("hidden", false); expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);
  }
  fireEvent.click(screen.getByRole("button", { name: "Info" }));
  fireEvent.keyDown(heading, { key: "Escape" });
  expect(document.activeElement).toBe(heading); expect(info).toHaveProperty("hidden", false);
  expect(f.enqueue).not.toHaveBeenCalled(); expect(f.rename).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled(); expect(f.recover).not.toHaveBeenCalled();
});

it("keeps one upper tool alongside the independent Terminal dock and restores each opener", async () => {
  const f = fixture(); const mounted = render(f.view());
  await screen.findByRole("heading", { name: "Original session" });
  const composer = screen.getByRole("textbox", { name: "Message" }), browser = screen.getByRole("button", { name: "Browser" }), terminals = screen.getByRole("button", { name: "Terminals" });
  fireEvent.click(browser);fireEvent.click(terminals);
  expect(browser.getAttribute("aria-expanded")).toBe("true");expect(terminals.getAttribute("aria-expanded")).toBe("true");
  const dock = screen.getByRole("complementary", { name: "Session terminals" });
  fireEvent.keyDown(within(dock).getByRole("button", { name: "Details" }), { key: "Escape" });
  expect(document.activeElement).toBe(terminals);expect(terminals.getAttribute("aria-expanded")).toBe("false");expect(browser.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(terminals);fireEvent.click(screen.getByRole("button", { name: "Files" }));
  expect(browser.getAttribute("aria-expanded")).toBe("false");expect(terminals.getAttribute("aria-expanded")).toBe("true");
  fireEvent.keyDown(screen.getByRole("textbox", { name: "Message" }), { key: "Escape" });
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Files" }));expect(terminals.getAttribute("aria-expanded")).toBe("true");
  expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);expect(f.control).not.toHaveBeenCalled();
  mounted.unmount();f.client.clear();
});

it.each([580, 120])("occludes mounted upper content at automatic maximum and restores it at body height %s", async height => {
  vi.stubGlobal("ResizeObserver", class { constructor(private changed: () => void) {} observe(node: HTMLElement) { Object.defineProperties(node, { clientWidth: { configurable: true, value: 1344 }, clientHeight: { configurable: true, value: height } }); this.changed(); } disconnect() {} });
  const f = fixture(), mounted = render(f.view());
  try {
    const composer = await screen.findByRole("textbox", { name: "Message" });composer.focus();
    fireEvent.click(screen.getByRole("button", { name: "Terminals" }));
    const upper = mounted.container.querySelector(".session-upper-content")!;
    await waitFor(() => expect(upper.getAttribute("aria-hidden")).toBe("true"));
    expect(upper.hasAttribute("inert")).toBe(true);expect(upper.contains(composer)).toBe(true);expect(composer).toHaveProperty("value", "Original draft");
    fireEvent.click(screen.getByRole("button", { name: "Restore terminal dock" }));
    await waitFor(() => expect(upper.hasAttribute("inert")).toBe(false));
    expect(screen.getByRole("textbox", { name: "Message" })).toBe(composer);expect(document.activeElement).toBe(composer);
    expect(upper.getAttribute("data-terminal-compact-restored")).toBe(height === 120 ? "true" : null);
    fireEvent.click(screen.getByRole("button", { name: "Maximize terminal dock" }));await waitFor(() => expect(upper.hasAttribute("inert")).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Hide terminals" }));await waitFor(() => expect(upper.hasAttribute("inert")).toBe(false));
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "Terminals" }));expect(f.control).not.toHaveBeenCalled();
  } finally { mounted.unmount();f.client.clear();vi.unstubAllGlobals(); }
});
