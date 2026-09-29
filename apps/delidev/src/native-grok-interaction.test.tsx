import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InteractionService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { NativeGrokInteraction, GrokDecision, GrokQuestionOutcome, GrokRequestKind } from "./native-grok-interaction";
import { MutationIntents } from "./mutation";
import { type Document } from "./documents";

type Input = { mutation?: { requestId?: string; id?: string; expectedRevision?: bigint }; responseJson: Uint8Array };
const turn = "00000000-0000-4000-8000-000000000001";
function fixture(kind = GrokRequestKind.File) {
  const resource = create(ResourceSchema, { id: newRequestId(), sessionId: newRequestId(), kind: EntityKind.INTERACTION, schemaVersion: 1, revision: 9n });
  const thread = newRequestId();
  const request: Document = { version: "1.0.41", kind, arrival_id: resource.id, request_digest: "ab".repeat(32), proposal_digest: "cd".repeat(32), mode: kind === GrokRequestKind.Plan ? "plan" : "default", tool_name: kind === GrokRequestKind.File ? "write" : kind === GrokRequestKind.Plan ? "exit_plan_mode" : "ask_user_question" };
  if (kind === GrokRequestKind.File) Object.assign(request, { path: "Original file", content: " Exact contents\n" });
  if (kind === GrokRequestKind.Plan) request.plan = { entry_tool_id: "enter", entry_event_id: `${thread}-9`, write_tool_id: "write", revision: 2, content: " Original revision\n", content_digest: "ef".repeat(32) };
  if (kind === GrokRequestKind.Question) request.questions = [{ question: "Original, exact question?", multiSelect: null, options: [{ label: "One", description: "First" }, { label: "Two", description: "Second" }] }, { question: "Another?", multiSelect: true, options: [{ label: "Three", description: "Third" }] }];
  const data: Document = { execution_id: newRequestId(), native_thread_id: thread, native_turn_id: turn, native_item_id: "native-tool", native_request_id: { kind: "text", text: resource.id }, type: kind === GrokRequestKind.Question ? "user-question" : "native-approval", closure: "open", grok: request };
  const send = vi.fn(async (_: Input) => ({ interaction: create(ResourceSchema, { ...resource, revision: 10n }) }));
  const accepted = vi.fn();
  const transport = createRouterTransport((r) => r.service(InteractionService, { respondQuestion: send, respondApproval: send }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const form = (value = data, allowed = true) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents><NativeGrokInteraction data={value} resource={resource} accepted={accepted} submissionAllowed={allowed} /></MutationIntents></QueryClientProvider></TransportProvider>;
  return { resource, request, data, send, form };
}
const decoded = (v: Input) => JSON.parse(new TextDecoder().decode(v.responseJson));
it.each([GrokDecision.Once, GrokDecision.Session, GrokDecision.Reject])("sends only the selected original Write decision: %s", async (decision) => {
  const f = fixture(); render(f.form());
  fireEvent.change(screen.getByRole("combobox", { name: "Decision" }), { target: { value: decision } });
  fireEvent.click(screen.getByRole("button", { name: "Send response to Grok" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ grok: { decision } });
  expect(f.send.mock.calls[0][0].mutation).toMatchObject({ id: f.resource.id, expectedRevision: 9n });
});
it.each([GrokDecision.Approve, GrokDecision.Revise, GrokDecision.Abandon])("responds to the original Plan revision without a common approval gate: %s", async (decision) => {
  const f = fixture(GrokRequestKind.Plan); render(f.form());
  expect(screen.getByText("Plan revision 2")).toBeTruthy();
  fireEvent.change(screen.getByRole("combobox", { name: "Decision" }), { target: { value: decision } });
  fireEvent.click(screen.getByRole("button", { name: "Send response to Grok" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ grok: { decision } });
});
it("preserves exact question keys, answer Unicode and whitespace", async () => {
  const f = fixture(GrokRequestKind.Question); render(f.form());
  fireEvent.change(screen.getByRole("textbox", { name: "Exact answer for question 1" }), { target: { value: "  답변, 🙂\n" } });
  fireEvent.change(screen.getByRole("textbox", { name: "Exact answer for question 2" }), { target: { value: "Three, custom" } });
  fireEvent.click(screen.getByRole("button", { name: "Send response to Grok" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ grok: { outcome: "accepted", answers: { "Original, exact question?": "  답변, 🙂\n", "Another?": "Three, custom" } } });
});
it.each([GrokQuestionOutcome.Skipped, GrokQuestionOutcome.Cancelled])("keeps interview outcomes distinct from Stop: %s", async (outcome) => {
  const f = fixture(GrokRequestKind.Question); render(f.form());
  fireEvent.change(screen.getByRole("textbox", { name: "Exact answer for question 1" }), { target: { value: "One" } });
  fireEvent.change(screen.getByRole("combobox", { name: "Interview outcome" }), { target: { value: outcome } });
  fireEvent.click(screen.getByRole("button", { name: "Send response to Grok" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(decoded(f.send.mock.calls[0][0])).toEqual({ grok: outcome === GrokQuestionOutcome.Skipped ? { outcome, partial_answers: { "Original, exact question?": "One" } } : { outcome } });
});
it("retries only the identical receipt after lost acknowledgement and native closure", async () => {
  const f = fixture(); f.send.mockRejectedValueOnce(new ConnectError("Lost acknowledgement", Code.Unavailable));
  const view = render(f.form());
  fireEvent.change(screen.getByRole("combobox", { name: "Decision" }), { target: { value: GrokDecision.Session } });
  fireEvent.click(screen.getByRole("button", { name: "Send response to Grok" }));
  await screen.findByRole("button", { name: "Retry the same response request" });
  view.rerender(f.form({ ...f.data, closure: "native-closed" }, false));
  fireEvent.click(screen.getByRole("button", { name: "Retry the same response request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(2));
  expect(f.send.mock.calls[0][0]).toEqual(f.send.mock.calls[1][0]);
});
it("blocks malformed ownership, foreign payloads, incomplete and oversized answers before RPC", () => {
  const f = fixture(GrokRequestKind.Question); const view = render(f.form());
  fireEvent.submit(screen.getByRole("form")); expect(f.send).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole("textbox", { name: "Exact answer for question 1" }), { target: { value: "x".repeat(256 * 1024) } });
  fireEvent.submit(screen.getByRole("form")); expect(f.send).not.toHaveBeenCalled();
  view.rerender(f.form({ ...f.data, opencode: {} }));
  expect(screen.queryByRole("form")).toBeNull();
  view.rerender(f.form({ ...f.data, native_request_id: { kind: "text", text: newRequestId() } }));
  expect(screen.queryByRole("form")).toBeNull();
});
