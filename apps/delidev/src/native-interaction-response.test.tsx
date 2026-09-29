import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InteractionService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { NativePermissionResponse, NativeQuestionResponse } from "./native-interaction-response";
import { MutationIntents } from "./mutation";
import { InteractionDraftKind, type InteractionDraftState } from "./inbox-drafts";
import { useState, type ReactNode } from "react";

type Input = { mutation?: { requestId?: string; id?: string; expectedRevision?: bigint }; responseJson: Uint8Array };
const question = { text: "Original", header: "Choice", options: [{ label: "Second", description: "" }, { label: "First", description: "" }], multiple: true };
function RetainedQuestionResponse({ resource, questions, closed, accepted, initialDraft }: { resource: ReturnType<typeof resourceFixture>; questions: typeof question[]; closed: boolean; accepted: () => void; initialDraft: InteractionDraftState }) {
  const [draft, setDraft] = useState(initialDraft);
  return <NativeQuestionResponse resource={resource} questions={questions} closed={closed} accepted={accepted} draft={draft} saveDraft={setDraft} />;
}
function resourceFixture() {
  return create(ResourceSchema, { id: newRequestId(), sessionId: newRequestId(), kind: EntityKind.INTERACTION, schemaVersion: 1, revision: 9n });
}
function fixture() {
  const resource = resourceFixture();
  const send = vi.fn(async (_: Input) => ({ interaction: create(ResourceSchema, { ...resource, revision: 10n }) }));
  const accepted = vi.fn();
  const transport = createRouterTransport((r) => r.service(InteractionService, { respondQuestion: send, respondApproval: send }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (child: ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{child}</MutationIntents></QueryClientProvider></TransportProvider>;
  const questions = (values = [question], closed = false, draft?: InteractionDraftState) => view(draft
    ? <RetainedQuestionResponse resource={resource} questions={values} closed={closed} accepted={accepted} initialDraft={draft} />
    : <NativeQuestionResponse resource={resource} questions={values} closed={closed} accepted={accepted} />);
  const permission = (closed = false) => view(<NativePermissionResponse resource={resource} closed={closed} accepted={accepted} />);
  return { resource, send, questions, permission };
}
function decoded(v: Input) { return JSON.parse(new TextDecoder().decode(v.responseJson)); }

it("sends the original ordered matrix and explicit unanswered rows", async () => {
  const f = fixture(); render(f.questions([question, { ...question, header: "Optional", options: [] }]));
  fireEvent.click(screen.getByRole("checkbox", { name: "First" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Second" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Leave question 2 unanswered" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { answers: [["First", "Second"], []] } });
  expect(f.send.mock.calls[0][0].mutation).toMatchObject({ id: f.resource.id, expectedRevision: 9n });
});

it("adds question rows when the retained Inbox draft starts with no option rows", async () => {
  const f = fixture();
  render(f.questions([question], false, { kind: InteractionDraftKind.OpenCodeQuestion, selected: [], custom: {}, customEnabled: {}, unanswered: {} }));
  fireEvent.click(screen.getByRole("checkbox", { name: "First" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { answers: [["First"]] } });
});

it("keeps a selected empty string distinct from an unanswered row", async () => {
  const f = fixture(); render(f.questions([{ ...question, options: [{ label: "", description: "" }], multiple: false }]));
  fireEvent.click(screen.getByRole("radio", { name: "(Empty option)" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { answers: [[""]] } });
});

it("replaces a single choice with exact custom text", async () => {
  const f = fixture(); render(f.questions([{ ...question, multiple: false }]));
  fireEvent.click(screen.getByRole("radio", { name: "First" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Use a custom answer" }));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "  답변\n" } });
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { answers: [["  답변\n"]] } });
});

it("does not auto-answer missing rows or ambiguous labels", () => {
  const f = fixture(); render(f.questions([{ ...question, options: [{ label: "Same", description: "" }, { label: "Same", description: "" }] }]));
  for (const input of screen.getAllByRole("checkbox", { name: "Same" })) expect((input as HTMLInputElement).disabled).toBe(true);
  fireEvent.submit(screen.getByRole("button", { name: "Send answers" }).closest("form")!);
  expect(f.send).not.toHaveBeenCalled();
});

it("keeps the same response identity after acknowledgement loss and closure", async () => {
  const f = fixture(); f.send.mockRejectedValueOnce(new ConnectError("Lost acknowledgement", Code.Unavailable));
  const rendered = render(f.questions());
  fireEvent.click(screen.getByRole("checkbox", { name: "First" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers" }));
  await screen.findByRole("button", { name: "Retry the same response request" });
  rendered.rerender(f.questions([question], true));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same response request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(2));
  expect(f.send.mock.calls[0][0]).toEqual(f.send.mock.calls[1][0]);
});

it("sends only an explicit original one-request permission", async () => {
  const f = fixture(); render(f.permission());
  expect(screen.queryByRole("textbox")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Allow once" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { decision: "once" } });
});

it("refuses a closed permission request", () => {
  const f = fixture(); render(f.permission(true));
  fireEvent.submit(screen.getByRole("button", { name: "Allow once" }).closest("form")!);
  expect(f.send).not.toHaveBeenCalled();
});

it("rejects a question through its dedicated native response without inventing empty answers", async () => {
  const f = fixture(); render(f.questions());
  fireEvent.click(screen.getByRole("button", { name: "Reject question request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { reject: true } });
});

it("keeps correction feedback exclusive to an explicit rejection", async () => {
  const f = fixture(); render(f.permission());
  fireEvent.click(screen.getByRole("checkbox", { name: "Include correction feedback with rejection" }));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: " Original correction\n" } });
  fireEvent.click(screen.getByRole("button", { name: "Reject permission request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { decision: "reject", feedback: " Original correction\n" } });
});

it("does not attach entered feedback to a native session allowance", async () => {
  const f = fixture(); render(f.permission());
  fireEvent.click(screen.getByRole("checkbox", { name: "Include correction feedback with rejection" }));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "Not rejection" } });
  fireEvent.click(screen.getByRole("button", { name: "Allow for this native session" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ opencode: { decision: "always" } });
});
