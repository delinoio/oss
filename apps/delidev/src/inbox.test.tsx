import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InboxReadState, InboxService, InboxSource, InboxViewSchema, InteractionService, ResourceSchema, ResourceService, newRequestId, type InboxView } from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { Inbox } from "./inbox";
import { MutationIntents } from "./mutation";

function fixture() {
  const sessionId = newRequestId(), executionId = newRequestId(), interactionId = newRequestId();
  const session = create(ResourceSchema, { id: sessionId, kind: EntityKind.SESSION, revision: 9n, schemaVersion: 1, documentJson: encode({ name: "Refactor authentication", archive: "active", dispatch: "claimed", recovery: "none", active_execution_id: executionId }) });
  const interaction = create(ResourceSchema, { id: interactionId, sessionId, kind: EntityKind.INTERACTION, revision: 3n, schemaVersion: 1, documentJson: encode({ type: "user-question", execution_id: executionId, closure: "open", questions: { questions: [{ id: "method", header: "Authentication", text: "Which methods should be supported?", secret: false, other: true, options: [] }] } }) });
  const entry = create(ResourceSchema, { id: newRequestId(), sessionId, kind: EntityKind.INBOX, revision: 2n, schemaVersion: 1, createdAt: "2026-09-29T00:10:00Z", documentJson: encode({ source: "interaction", source_id: interactionId, read_state: "unread" }) });
  const questionView = create(InboxViewSchema, { entry, session, interaction });
  const terminalEntry = create(ResourceSchema, { id: newRequestId(), sessionId, kind: EntityKind.INBOX, revision: 1n, schemaVersion: 1, createdAt: "2026-09-29T00:15:00Z", documentJson: encode({ source: "execution-terminal", source_id: executionId, read_state: "read", terminal: { outcome: "succeeded" } }) });
  const terminalView = create(InboxViewSchema, { entry: terminalEntry, session });
  const list = vi.fn((_request: unknown) => ({ entries: [questionView, terminalView], nextPageToken: "cursor-2" }));
  const get = vi.fn(async (request: { id?: string }) => ({ view: request.id === terminalEntry.id ? terminalView : questionView }));
  const setRead = vi.fn((_request: unknown) => ({ view: questionView, requestId: newRequestId(), replayed: false }));
  const answer = vi.fn((_request: unknown) => ({ interaction }));
  const transport = createRouterTransport((router) => {
    router.service(InboxService, { listInbox: list, getInboxEntry: get, setInboxReadState: setRead });
    router.service(ResourceService, { listResources: () => ({ resources: [] }) });
    router.service(InteractionService, { respondQuestion: answer });
  });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity }, mutations: { retry: false } } });
  const renderInbox = (props: { active?: boolean; notificationId?: string; notificationActivation?: number } = {}) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><Inbox active={props.active ?? true} open={() => {}} notificationId={props.notificationId} notificationActivation={props.notificationActivation} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { entry, interaction, questionView, session, terminalEntry, list, get, setRead, answer, renderInbox };
}

it("starts with server All/All filters, selects an exact source read, and keeps row navigation read-only", async () => {
  const value = fixture();
  render(value.renderInbox());
  await screen.findByRole("button", { name: /Agent question, Refactor authentication, Unread/ });
  expect(value.list.mock.calls[0]?.[0]).toMatchObject({ source: InboxSource.UNSPECIFIED, readState: InboxReadState.UNSPECIFIED, pageSize: 20, pageToken: "" });
  expect(screen.getByText("Select an item to view its request or result.")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: /Agent question, Refactor authentication, Unread/ }));
  await screen.findByText("Which methods should be supported?");
  expect(value.get.mock.calls[0]?.[0]).toMatchObject({ id: value.entry.id });
  expect(value.setRead).not.toHaveBeenCalled();
  expect(value.answer).not.toHaveBeenCalled();
  expect(screen.getByText(/Recorded/).closest("span")?.querySelector("time")?.getAttribute("datetime")).toBe("2026-09-29T00:10:00Z");
});

it("keeps read-state writes separate and revision-bound to the inbox entry", async () => {
  const value = fixture();
  render(value.renderInbox());
  await screen.findByRole("button", { name: /Agent question/ });
  fireEvent.click(screen.getByRole("button", { name: /Agent question/ }));
  await screen.findByText("Which methods should be supported?");
  fireEvent.click(screen.getByRole("button", { name: "Mark read" }));
  await waitFor(() => expect(value.setRead).toHaveBeenCalledTimes(1));
  expect(value.setRead.mock.calls[0]?.[0]).toMatchObject({ mutation: { id: value.entry.id, expectedRevision: 2n }, readState: InboxReadState.READ });
  expect(value.answer).not.toHaveBeenCalled();
});

it("applies source and read filters on the server and resets the active cursor", async () => {
  const value = fixture();
  render(value.renderInbox());
  await screen.findByRole("button", { name: /Agent question/ });
  fireEvent.click(screen.getByRole("button", { name: "Next page" }));
  await waitFor(() => expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ pageToken: "cursor-2" }));
  fireEvent.change(screen.getByRole("combobox", { name: "Source" }), { target: { value: InboxSource.INTERACTION } });
  expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ source: InboxSource.UNSPECIFIED, readState: InboxReadState.UNSPECIFIED, pageToken: "cursor-2" });
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await waitFor(() => expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ source: InboxSource.INTERACTION, readState: InboxReadState.UNSPECIFIED, pageToken: "" }));
  fireEvent.click(screen.getByRole("button", { name: "Unread" }));
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await waitFor(() => expect(value.list.mock.calls.at(-1)?.[0]).toMatchObject({ source: InboxSource.INTERACTION, readState: InboxReadState.UNREAD, pageToken: "" }));
});

it("preserves editable response fields when selection and server filters change", async () => {
  const value = fixture();
  render(value.renderInbox());
  await screen.findByRole("button", { name: /Agent question/ });
  fireEvent.click(screen.getByRole("button", { name: /Agent question/ }));
  const answer = await screen.findByRole("textbox", { name: "Your answer" });
  fireEvent.change(answer, { target: { value: "Passkeys and SSO" } });
  fireEvent.click(screen.getByRole("button", { name: /Execution succeeded, Refactor authentication, Read/ }));
  await screen.findByText("Original terminal observation");
  fireEvent.change(screen.getByRole("combobox", { name: "Source" }), { target: { value: InboxSource.INTERACTION } });
  fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
  await screen.findByRole("button", { name: /Agent question/ });
  fireEvent.click(screen.getByRole("button", { name: /Agent question/ }));
  expect((await screen.findByRole("textbox", { name: "Your answer" }) as HTMLTextAreaElement).value).toBe("Passkeys and SSO");
  expect(value.answer).not.toHaveBeenCalled();
});

it("keeps the preceding native answer in the Inbox cache after an oversized paste", async () => {
  const value = fixture();
  const nativeId = (prefix: string, suffix = "A") => `${prefix}_${"a".repeat(12)}${suffix.repeat(14)}`;
  value.interaction.documentJson = encode({
    type: "user-question", execution_id: document(value.interaction).execution_id, closure: "open",
    native_item_id: nativeId("prt"), native_thread_id: nativeId("ses"), native_turn_id: nativeId("msg"),
    native_request_id: { kind: "text", text: nativeId("que") },
    opencode: { version: "1.18.32", native_event_id: nativeId("evt"), native_message_id: nativeId("msg", "B"), call_id: "original-call", questions: [{ text: "Original question", header: "Original", options: [], custom: true }] },
  });
  const rendered = render(value.renderInbox());
  fireEvent.click(await screen.findByRole("button", { name: /Agent question/ }));
  fireEvent.click(await screen.findByRole("checkbox", { name: "Use a custom answer" }));
  const answer = screen.getByRole("textbox", { name: "Custom answer for question 1" });
  fireEvent.change(answer, { target: { value: "Previous exact answer" } });
  fireEvent.change(answer, { target: { value: "x".repeat(1024 * 1024) } });
  expect((answer as HTMLTextAreaElement).value).toBe("Previous exact answer");
  expect(screen.getByText(/previous draft was kept/)).toBeTruthy();
  rendered.rerender(value.renderInbox({ active: false }));
  rendered.rerender(value.renderInbox());
  fireEvent.click(screen.getByRole("button", { name: /Execution succeeded, Refactor authentication, Read/ }));
  await screen.findByText("Original terminal observation");
  fireEvent.click(screen.getByRole("button", { name: /Agent question/ }));
  expect((await screen.findByRole("textbox", { name: "Custom answer for question 1" }) as HTMLTextAreaElement).value).toBe("Previous exact answer");
  expect(value.answer).not.toHaveBeenCalled();
});

it("revalidates every repeated notification activation through the exact inbox read", async () => {
  const value = fixture();
  const rendered = render(value.renderInbox({ notificationId: value.entry.id, notificationActivation: 1 }));
  await screen.findByText("Which methods should be supported?");
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  rendered.rerender(value.renderInbox({ notificationId: value.entry.id, notificationActivation: 2 }));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(2));
  expect(value.setRead).not.toHaveBeenCalled();
  expect(value.answer).not.toHaveBeenCalled();
});

it("starts a fresh exact read for overlapping notification activations and ignores the older result", async () => {
  const value = fixture();
  const changedInteraction = create(ResourceSchema, {
    ...value.interaction,
    revision: 4n,
    documentJson: encode({ type: "user-question", execution_id: document(value.interaction).execution_id, closure: "open", questions: { questions: [{ id: "method", header: "Authentication", text: "Fresh question", secret: false, other: true, options: [] }] } }),
  });
  const freshView = create(InboxViewSchema, { entry: value.entry, session: value.session, interaction: changedInteraction });
  let releaseFirst!: (result: { view: InboxView }) => void;
  const firstRead = new Promise<{ view: InboxView }>((resolve) => { releaseFirst = resolve; });
  value.get.mockImplementationOnce(async () => firstRead).mockImplementationOnce(async () => ({ view: freshView }));

  const rendered = render(value.renderInbox({ notificationId: value.entry.id, notificationActivation: 1 }));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(1));
  rendered.rerender(value.renderInbox({ notificationId: value.entry.id, notificationActivation: 2 }));
  await waitFor(() => expect(value.get).toHaveBeenCalledTimes(2));
  await screen.findByText("Fresh question");
  releaseFirst({ view: value.questionView });
  await screen.findByText("Fresh question");
  expect(screen.queryByText("Which methods should be supported?")).toBeNull();
  expect(value.setRead).not.toHaveBeenCalled();
  expect(value.answer).not.toHaveBeenCalled();
});

it("does not render a current source joined to a foreign session", async () => {
  const value = fixture();
  value.interaction.sessionId = newRequestId();
  render(value.renderInbox());
  await screen.findByRole("button", { name: /Agent question/ });
  fireEvent.click(screen.getByRole("button", { name: /Agent question/ }));
  await screen.findByText(/This item is unavailable/);
  expect(screen.queryByText("Which methods should be supported?")).toBeNull();
  expect(value.setRead).not.toHaveBeenCalled();
  expect(value.answer).not.toHaveBeenCalled();
});


it("renders account quota recovery with no terminal or session authority", async()=>{
 const value=fixture(),account=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.ACCOUNT,schemaVersion:2,revision:1n,documentJson:encode({alias:"Recovered subscription",type:"subscription",subscription_service:"chatgpt"})});
 const observed="2026-10-04T01:23:45Z";
 const entry=create(ResourceSchema,{id:newRequestId(),kind:EntityKind.INBOX,schemaVersion:1,revision:1n,documentJson:encode({source:"subscription-recovery",source_id:newRequestId(),read_state:"unread",recovery:{account_id:account.id,connection_id:newRequestId(),observed_at:observed}})});
 const view=create(InboxViewSchema,{entry,account});
 value.list.mockReturnValue({entries:[view],nextPageToken:""});
 value.get.mockResolvedValue({view});
 render(value.renderInbox());
 fireEvent.click(await screen.findByRole("button",{name:/Subscription quota recovered, Recovered subscription, Unread/}));
 await screen.findByRole("heading",{name:"Subscription quota recovery"});
 expect(screen.getByText("Account: Recovered subscription")).toBeTruthy();
 expect(screen.getByText(observed).getAttribute("datetime")).toBe(observed);
 expect(screen.queryByText("Original terminal observation")).toBeNull();
 expect((screen.getByRole("button",{name:"Open session"}) as HTMLButtonElement).disabled).toBe(true);
 expect(value.answer).not.toHaveBeenCalled(); expect(value.setRead).not.toHaveBeenCalled();
});
