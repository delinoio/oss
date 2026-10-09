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

function fixture(state = BudgetState.ALLOW_INCOMPLETE, problem = false, extra: Record<string, unknown> = {}, queueInputs: (id: string) => ReturnType<typeof create<typeof ResourceSchema>>[] = () => []) {
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
    router.service(SessionService, { listQueue: () => ({ inputs: queueInputs(id) }), getSessionBudget: budget, enqueueInput: enqueue, renameSession: rename, controlSession: control, recoverSessionExecution: recover });
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
  screen.getByRole("heading", { name: "Session information" }).focus();
  fireEvent.click(screen.getByRole("button", { name: "Rename session" }));
  const name = screen.getByRole("textbox", { name: "Session name" });
  fireEvent.change(name, { target: { value: "Staged name" } });
  fireEvent.click(screen.getByRole("button", { name: "Files" }));
  expect(composer.isConnected).toBe(true);
  expect(composer.closest("[hidden]")).not.toBeNull();
  screen.getByRole("heading", { name: "Session information" }).focus();
  expect(screen.getByRole("textbox", { name: "Session name" })).toBe(name);
  expect(name).toHaveProperty("value", "Staged name");
  fireEvent.keyDown(screen.getByRole("complementary", { name: "Session information" }), { key: "Escape" });
  fireEvent.click(screen.getByRole("tab", {name:"Conversation"}));
  expect(screen.getByRole("complementary", { name: "Session information" })).toHaveProperty("hidden", false);
  mounted.rerender(f.view("Retained draft"));
  expect(composer).toHaveProperty("value", "Retained draft");
  expect(screen.getByRole("checkbox", { name: "Plan Mode" })).toHaveProperty("checked", true);
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect(screen.getByRole("textbox", { name: "메시지" })).toBe(composer);
  screen.getByRole("heading", { name: "세션 정보" }).focus();
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
  fireEvent.click(screen.getByRole("button", { name: "Show details" }));
  fireEvent.click(screen.getByRole("button", { name: "Files" }));
  expect(screen.getByRole("tab", { name: "Files" }).getAttribute("aria-selected")).toBe("true");
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
  screen.getByRole("heading", { name: "Session information" }).focus();
  screen.getByRole("heading", { name: "Session information" }).focus();
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
  fireEvent.click(screen.getByRole("button", {name:"Reconcile original execution"}));
  expect(screen.getByRole("tab", { name: "Files" }).getAttribute("aria-selected")).toBe("true");
  expect(f.recover).not.toHaveBeenCalled();
  expect(screen.getByRole("complementary", { name: "Session information" })).toBeTruthy();
  expect(document.activeElement).toBe(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  const confirmation = screen.getByRole("button", { name: "Confirm selected recovery action" });
  fireEvent.keyDown(confirmation, { key: "Escape" });
  expect(screen.getByRole("tab", {name:"Files"}).getAttribute("aria-selected")).toBe("true");
  screen.getByRole("heading", { name: "Session information" }).focus();
  expect(screen.getByRole("button", { name: "Confirm selected recovery action" })).toBe(confirmation);
  expect(screen.getAllByRole("button", { name: "Reconcile original execution" })).toHaveLength(1);
  expect(recovery.isConnected).toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Confirm selected recovery action" }));
  await waitFor(() => expect(f.recover).toHaveBeenCalledTimes(1));
  expect(f.recover.mock.calls[0][0]).toMatchObject({ mutation: { id: f.session.id, expectedRevision: 7n }, expectedExecutionId: execution });
  fireEvent.click(screen.getByRole("tab", {name:"Conversation"}));
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


it("keeps Info independent of all tabs with one active content region and retained authoring", async()=>{
 const f=fixture();render(f.view());const composer=await screen.findByRole("textbox",{name:"Message"});const info=screen.getByRole("complementary",{name:"Session information"});
 for(const name of ["Files","Diff","Terminals","Browser","Diagnostics"]){fireEvent.click(screen.getByRole("button",{name}));expect(screen.getByRole("tab",{name}).getAttribute("aria-selected")).toBe("true");expect(screen.getAllByRole("tabpanel")).toHaveLength(1);expect(info).toHaveProperty("hidden",false);expect(composer.isConnected).toBe(true);expect(composer.closest("[hidden]")).not.toBeNull();info.focus();fireEvent.keyDown(info,{key:"Escape"});expect(screen.getByRole("tab",{name}).getAttribute("aria-selected")).toBe("true");}
 fireEvent.click(screen.getByRole("tab",{name:"Conversation"}));expect(screen.getByRole("textbox",{name:"Message"})).toBe(composer);expect(f.control).not.toHaveBeenCalled();expect(f.enqueue).not.toHaveBeenCalled();expect(f.rename).not.toHaveBeenCalled();
});
it("replaces dock/split coexistence with exact selection and presentation-only close",async()=>{
 const f=fixture();render(f.view());await screen.findByRole("heading",{name:"Original session"});fireEvent.click(screen.getByRole("button",{name:"Browser"}));fireEvent.click(screen.getByRole("button",{name:"Terminals"}));expect(screen.getByRole("tab",{name:"Terminals"}).getAttribute("aria-selected")).toBe("true");expect(screen.queryByRole("region",{name:"Session browser"})).toBeNull();fireEvent.click(screen.getByRole("button",{name:"Close Browser tab"}));expect(screen.getByRole("tab",{name:"Terminals"}).getAttribute("aria-selected")).toBe("true");fireEvent.click(screen.getByRole("button",{name:"Close Terminals tab"}));expect(screen.getByRole("tab",{name:"Conversation"}).getAttribute("aria-selected")).toBe("true");expect(document.activeElement).toBe(screen.getByRole("tab",{name:"Conversation"}));expect(f.control).not.toHaveBeenCalled();
});
it.each([580,120])("keeps retained composer isolated from terminal pane at body height %s",async height=>{
 const f=fixture();render(f.view());const composer=await screen.findByRole("textbox",{name:"Message"});fireEvent.click(screen.getByRole("button",{name:"Terminals"}));expect(composer.isConnected).toBe(true);expect(composer.closest("[inert]")).not.toBeNull();expect(screen.queryByRole("button",{name:"Maximize terminal dock"})).toBeNull();expect(screen.getAllByRole("tabpanel")).toHaveLength(1);fireEvent.click(screen.getByRole("tab",{name:"Conversation"}));expect(screen.getByRole("textbox",{name:"Message"})).toBe(composer);expect(composer).toHaveProperty("value","Original draft");expect(height).toBeGreaterThan(0);expect(f.control).not.toHaveBeenCalled();
});

it.each([false, true])("shows only waiting queue inputs while retaining accepted history (mixed=%s)", async mixed => {
  const f = fixture(BudgetState.ALLOW_INCOMPLETE, false, {}, id => [
    ...["accepted", "claimed", "uncertain", "rejected-before-start", "unknown", undefined].map((delivery, i) => create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ sequence: i + 1, delivery, prompt: `Hidden history ${i}`, mode: "execute" }) })),
    ...(mixed ? [create(ResourceSchema, { id: newRequestId(), sessionId: id, kind: EntityKind.QUEUE, schemaVersion: 1, revision: 1n, documentJson: encode({ sequence: 8, delivery: "queued", prompt: "Waiting input", mode: "execute" }) })] : []),
  ]);
  render(f.view());
  await screen.findByRole("heading", { name: "Original session" });
  const summary = await screen.findByText(`Input queue · ${mixed ? 1 : 0} waiting`);
  fireEvent.click(summary);
  expect(document.querySelectorAll(".queue-item")).toHaveLength(mixed ? 1 : 0);
  expect(screen.queryByText(/Hidden history/)).toBeNull();
  if (mixed) expect(screen.getByText("Waiting input")).toBeTruthy();
});

it("keeps exactly five workspace tools and no Info action in either locale", async () => {
  const f = fixture(); render(f.view()); await screen.findByRole("heading", { name: "Original session" });
  const toolbar = document.querySelector(".session-toolbar-actions")!;
  expect([...toolbar.querySelectorAll("button")].map(button => button.textContent)).toEqual(["Diff", "Files", "Terminals", "Browser", "Diagnostics"]);
  expect(screen.queryByRole("button", { name: "Info" })).toBeNull();
  const info = screen.getByRole("complementary", { name: "Session information" });
  const composer = screen.getByRole("textbox", { name: "Message" });
  await act(async () => { await i18n.changeLanguage("ko"); });
  expect([...toolbar.querySelectorAll("button")].map(button => button.textContent)).toEqual(["변경 사항", "파일", "터미널", "브라우저", "진단"]);
  expect(screen.queryByRole("button", { name: "정보" })).toBeNull();
  expect(screen.getByRole("complementary", { name: "세션 정보" })).toBe(info);
  expect(screen.getByRole("textbox", { name: "메시지" })).toBe(composer);
  expect(f.enqueue).not.toHaveBeenCalled(); expect(f.rename).not.toHaveBeenCalled(); expect(f.control).not.toHaveBeenCalled();
});

it("keeps a compact header and all independent status/title evidence in the expanded inspector", async () => {
 const f=fixture(BudgetState.ALLOW_INCOMPLETE,false,{workspace:"worktree",outcome:"succeeded",dispatch:"ready",archive:"active",preparation:{state:"ready"},recovery:"none",name_mode:"automatic",title_state:"unsupported",title_reason:"worker-capability-absent"});
 render(f.view());await screen.findByRole("heading",{name:"Original session"});
 const header=document.querySelector(".session-header")!,info=screen.getByRole("complementary",{name:"Session information"});
 expect(header.querySelector(".session-title-status")).toBeNull();expect(header.textContent).not.toContain("Succeeded");expect(header.textContent).not.toContain("Title generation unsupported");
 const values=info.querySelector(".session-status-values")!;
 expect([...values.querySelectorAll("dt")].map(node=>node.textContent)).toEqual(["Session ID","Workspace","Result","Dispatch","Archive","Preparation","Recovery","Automatic title"]);
 expect([...values.querySelectorAll("dd")].slice(0,7).map(node=>node.textContent)).toEqual([f.session.id,"Worktree","succeeded","ready","active","ready","none"]);
 expect(values.textContent).toContain("The original Worker did not prove the required title capability.");
 expect(info.querySelector(".session-tools")).toHaveProperty("open",true);expect([...info.querySelectorAll(".session-information-section")].every(node=>(node as HTMLDetailsElement).open)).toBe(true);
 expect(info.querySelectorAll(".session-information-section")).toHaveLength(5);
 expect(screen.queryByRole("button",{name:"Show PR associations"})).toBeNull();
 expect(info.querySelector(".execution-configuration")?.tagName).toBe("DIV");expect(info.querySelector(".conversation-page-scroll")?.tagName).toBe("DIV");
 expect(f.control).not.toHaveBeenCalled();expect(f.rename).not.toHaveBeenCalled();expect(f.enqueue).not.toHaveBeenCalled();
});

it("retains primary section choices and editor/input identities across tools and locale changes",async()=>{
 const f=fixture();render(f.view());const composer=await screen.findByRole("textbox",{name:"Message"});
 const info=screen.getByRole("complementary",{name:"Session information"});
 const execution=[...info.querySelectorAll<HTMLDetailsElement>(".session-information-section")].find(node=>node.querySelector(".execution-configuration"))!;
 const projection=info.querySelector(".execution-configuration");
 await act(async()=>{execution.open=false;fireEvent(execution,new Event("toggle"));});
 fireEvent.click(screen.getByRole("button",{name:"Files"}));expect(execution.open).toBe(false);expect(info.querySelector(".execution-configuration")).toBe(projection);
 await act(async()=>{await i18n.changeLanguage("ko");});expect(execution.open).toBe(false);expect(info.querySelector(".execution-configuration")).toBe(projection);
 await act(async()=>{await i18n.changeLanguage("en");});fireEvent.click(screen.getByRole("tab",{name:"Conversation"}));expect(screen.getByRole("textbox",{name:"Message"})).toBe(composer);
 expect(f.control).not.toHaveBeenCalled();expect(f.enqueue).not.toHaveBeenCalled();
});
