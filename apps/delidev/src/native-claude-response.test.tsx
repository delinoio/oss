import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InteractionService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeResponse } from "./native-claude-response";
import { MutationIntents } from "./mutation";
import { InteractionDraftKind, type InteractionDraftState } from "./inbox-drafts";
import { useState, type ReactNode } from "react";

type Input = { mutation?: { requestId?: string; id?: string; expectedRevision?: bigint }; responseJson: Uint8Array };
const question = { question: "Original?", header: "Choice", options: [{ label: "One", description: "First" }, { label: "Two", description: "Second" }], multiSelect: true };
function RetainedClaudeResponse({ resource, questions, closed, accepted, initialDraft }: { resource: ReturnType<typeof resourceFixture>; questions?: typeof question[]; closed: boolean; accepted: () => void; initialDraft: InteractionDraftState }) {
  const [draft, setDraft] = useState(initialDraft);
  return <NativeClaudeResponse resource={resource} questions={questions} closed={closed} accepted={accepted} draft={draft} saveDraft={setDraft} />;
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
  const form = (questions = true, closed = false, multiple = true, draft?: InteractionDraftState) => {
    const values = questions ? [{ ...question, multiSelect: multiple }] : undefined;
    return view(draft
      ? <RetainedClaudeResponse resource={resource} questions={values} closed={closed} accepted={accepted} initialDraft={draft} />
      : <NativeClaudeResponse resource={resource} questions={values} closed={closed} accepted={accepted} />);
  };
  return { resource, send, form };
}
function decoded(v: Input) { return JSON.parse(new TextDecoder().decode(v.responseJson)); }

it("keeps original question text keys and native comma-separated selection order", async () => {
  const f = fixture(); render(f.form());
  fireEvent.click(screen.getByRole("checkbox", { name: "Two Second" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "One First" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers to Claude" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "allow", answers: { "Original?": "Two, One" } } });
  expect(f.send.mock.calls[0][0].mutation).toMatchObject({ id: f.resource.id, expectedRevision: 9n });
});
it("adds question rows when the retained Inbox draft starts with no option rows", async () => {
  const f = fixture();
  render(f.form(true, false, true, { kind: InteractionDraftKind.Claude, selected: [], custom: {}, customEnabled: {}, skipped: {}, denial: false, reason: "", interrupt: false }));
  fireEvent.click(screen.getByRole("checkbox", { name: "One First" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers to Claude" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "allow", answers: { "Original?": "One" } } });
});
it("replaces a single choice with exact custom text", async () => {
  const f = fixture(); render(f.form(true, false, false));
  fireEvent.click(screen.getByRole("radio", { name: "One First" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "Use an exact custom answer for question 1" }));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "  답변\n" } });
  fireEvent.click(screen.getByRole("button", { name: "Send answers to Claude" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "allow", answers: { "Original?": "  답변\n" } } });
});
it.each([true, false])("keeps explicit unanswered and empty-string answers distinct: skip=%s", async (skip) => {
  const f = fixture(); render(f.form());
  fireEvent.click(screen.getByRole("checkbox", { name: skip ? "Leave question 1 unanswered" : "Use an exact custom answer for question 1" }));
  fireEvent.click(screen.getByRole("button", { name: "Send answers to Claude" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "allow", answers: skip ? {} : { "Original?": "" } } });
});
it("sends an explicit one-request allow without editing tool input", async () => {
  const f = fixture(); render(f.form(false));
  fireEvent.click(screen.getByRole("button", { name: "Allow this Claude request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "allow" } });
});
it.each([true, false])("preserves the exact original denial reason: question=%s", async (question) => {
  const f = fixture(); render(f.form(question));
  fireEvent.click(screen.getByRole("checkbox", { name: "Deny this request" }));
  fireEvent.submit(screen.getByRole("form")); expect(f.send).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole("textbox", { name: "Reason for denial" }), { target: { value: " Original denial\n" } });
  fireEvent.click(screen.getByRole("button", { name: "Send denial to Claude" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "deny", message: " Original denial\n" } });
});
it("retries only the retained server receipt after acknowledgment loss and native cancellation", async () => {
  const f = fixture(); f.send.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  const rendered = render(f.form(false));
  fireEvent.click(screen.getByRole("button", { name: "Allow this Claude request" }));
  await screen.findByRole("button", { name: "Retry the same response request" });
  rendered.rerender(f.form(false, true));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same response request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(2));
  expect(f.send.mock.calls[0][0]).toEqual(f.send.mock.calls[1][0]);
});
it.each([true, false])("retains an explicit interrupted denial through an uncertain receipt: question=%s", async (question) => {
  const f = fixture(); f.send.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  const rendered = render(f.form(question));
  fireEvent.click(screen.getByRole("checkbox", { name: "Deny this request" }));
  fireEvent.change(screen.getByRole("textbox", { name: "Reason for denial" }), { target: { value: "Original interrupted denial" } });
  fireEvent.click(screen.getByRole("checkbox", { name: "Also interrupt this Claude run" }));
  fireEvent.click(screen.getByRole("button", { name: "Send denial to Claude" }));
  await screen.findByRole("button", { name: "Retry the same response request" });
  rendered.rerender(f.form(question, true));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same response request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(2));
  expect(f.send.mock.calls[0][0]).toEqual(f.send.mock.calls[1][0]);
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ claude: { behavior: "deny", message: "Original interrupted denial", interrupt: true } });
});
it("blocks closed, missing and oversized answers before RPC", () => {
  const f = fixture(); const rendered = render(f.form());
  fireEvent.submit(screen.getByRole("form")); expect(f.send).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("checkbox", { name: "Use an exact custom answer for question 1" }));
  fireEvent.change(screen.getByRole("textbox"), { target: { value: "x".repeat(256 * 1024) } });
  fireEvent.submit(screen.getByRole("form")); expect(f.send).not.toHaveBeenCalled();
  rendered.rerender(f.form(false, true)); fireEvent.submit(screen.getByRole("form")); expect(f.send).not.toHaveBeenCalled();
});
