import { useState, type ReactNode } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { EntityKind, InteractionService, ResourceSchema, newRequestId } from "@delinoio/delidev-api-client";
import { NativeQuestionResponse, NativePermissionResponse } from "./native-interaction-response";
import { NativeClaudeResponse } from "./native-claude-response";
import { NativeGrokInteraction } from "./native-grok-interactions";
import { MutationIntents } from "./mutation";
import { GrokDraftOutcome, InteractionDraftKind, type InteractionDraftState } from "./inbox-drafts";
import { nativeResponseByteLength, nativeResponseLimit } from "./native-response-bounds";
import { encode } from "./documents";

type Input = { mutation?: { requestId?: string }; responseJson: Uint8Array };
enum Editor { OpenCodeAnswer, OpenCodeFeedback, ClaudeAnswer, ClaudeDenial, GrokAnswer, GrokNotes }
const editors = [Editor.OpenCodeAnswer, Editor.OpenCodeFeedback, Editor.ClaudeAnswer, Editor.ClaudeDenial, Editor.GrokAnswer, Editor.GrokNotes];
const names = ["Custom answer for question 1", /^Correction feedback/, "Custom answer for question 1", "Reason for denial", "Exact answer for question 1", "Notes for question 1"];
const limits = [64 * 1024, 64 * 1024, nativeResponseLimit, 4096, 64 * 1024, 64 * 1024];

function fixture(editor: Editor, cached = false, rows = 1) {
  const resource = create(ResourceSchema, { id: newRequestId(), kind: EntityKind.INTERACTION, sessionId: newRequestId(), revision: 9n, schemaVersion: 1 });
  const send = vi.fn(async (_: Input) => ({ interaction: resource }));
  const save = vi.fn((_value: InteractionDraftState) => {});
  const questions = Array.from({ length: rows }, (_, i) => ({ question: `Question ${i + 1}`, header: `Question ${i + 1}`, options: [{ label: "One", description: "First" }, { label: "Two", description: "Second" }], multiSelect: true }));
  const data = { execution_id: resource.id, native_thread_id: resource.id, native_turn_id: "526452fa-1956-42dd-b5f4-60e2b23dfe92", native_item_id: "original", native_request_id: { kind: "text", text: "native-original" }, type: "user-question", closure: "open", grok: { version: "1.0.41", observation_id: resource.id, request_digest: "ab".repeat(32), proposal_digest: "cd".repeat(32), event: { method: "_x.ai/ask_user_question", request_id: { kind: "text", text: "native-original" }, arrival_id: resource.id, payload: { sessionId: resource.id, toolCallId: "original", questions: questions.map(({ header: _header, ...q }) => q) } } } };
  const initial: InteractionDraftState = editor === Editor.OpenCodeAnswer
    ? { kind: InteractionDraftKind.OpenCodeQuestion, selected: [], custom: {}, customEnabled: {}, unanswered: {} }
    : editor === Editor.OpenCodeFeedback ? { kind: InteractionDraftKind.OpenCodePermission, feedbackEnabled: false, feedback: "" }
    : editor === Editor.ClaudeAnswer || editor === Editor.ClaudeDenial ? { kind: InteractionDraftKind.Claude, selected: [], custom: {}, customEnabled: {}, skipped: {}, denial: false, reason: "", interrupt: false }
    : { kind: InteractionDraftKind.Grok, outcome: GrokDraftOutcome.Accepted, decision: "", answers: {}, notes: {}, partial: {} };
  let retained: InteractionDraftState = initial;
  const form = (draft?: InteractionDraftState, saveDraft?: (v: InteractionDraftState) => void) => {
    const props = { resource, closed: false, accepted: () => {}, draft, saveDraft };
    if (editor === Editor.OpenCodeAnswer) return <NativeQuestionResponse {...props} questions={questions.map((q) => ({ text: q.question, header: q.header, options: q.options, multiple: true }))} />;
    if (editor === Editor.OpenCodeFeedback) return <NativePermissionResponse {...props} />;
    if (editor === Editor.ClaudeAnswer || editor === Editor.ClaudeDenial) return <NativeClaudeResponse {...props} questions={editor === Editor.ClaudeAnswer ? questions : undefined} />;
    return <NativeGrokInteraction {...props} data={data} />;
  };
  function CachedEditor() {
    const [draft, setDraft] = useState<InteractionDraftState>(retained);
    return form(draft, (value) => { retained = value; save(value); setDraft(value); });
  }
  const transport = createRouterTransport((router) => router.service(InteractionService, { respondQuestion: send, respondApproval: send }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = (child: ReactNode) => <TransportProvider transport={transport}><QueryClientProvider client={client}><MutationIntents>{child}</MutationIntents></QueryClientProvider></TransportProvider>;
  const mount = () => render(view(cached ? <CachedEditor /> : form()));
  const activate = () => {
    if (editor === Editor.OpenCodeAnswer) for (const input of screen.getAllByRole("checkbox", { name: "Use a custom answer" })) fireEvent.click(input);
    if (editor === Editor.OpenCodeFeedback) fireEvent.click(screen.getByRole("checkbox", { name: "Include correction feedback with rejection" }));
    if (editor === Editor.ClaudeAnswer) for (const input of screen.getAllByRole("checkbox", { name: /Use an exact custom answer/ })) fireEvent.click(input);
    if (editor === Editor.ClaudeDenial) fireEvent.click(screen.getByRole("checkbox", { name: "Deny this request" }));
  };
  return { send, save, mount, activate, retained: () => retained };
}

for (const cached of [false, true]) for (const editor of editors) it.each(["x", "한", "😀"])(`rejects ${Editor[editor]} UTF-8 overflow before ${cached ? "Inbox cache writes" : "Session retention"}: %s`, (character) => {
  const f = fixture(editor, cached); f.mount(); f.activate();
  const input = screen.getByRole("textbox", { name: names[editor] });
  fireEvent.change(input, { target: { value: "Previous exact answer\n" } });
  const previous = f.retained(); f.save.mockClear();
  const bytes = new TextEncoder().encode(character).length;
  fireEvent.change(input, { target: { value: character.repeat(Math.floor(limits[editor] / bytes) + 1) } });
  expect((input as HTMLTextAreaElement).value).toBe("Previous exact answer\n");
  expect(f.retained()).toBe(previous);
  expect(f.save).not.toHaveBeenCalled();
  expect(screen.getByText(/Your previous draft was kept/)).toBeTruthy();
  expect(f.send).not.toHaveBeenCalled();
  fireEvent.change(input, { target: { value: "" } });
  expect((input as HTMLTextAreaElement).value).toBe("");
  expect(screen.queryByText(/Your previous draft was kept/)).toBeNull();
});

for (const cached of [false, true]) for (const editor of editors.filter((v) => v !== Editor.ClaudeAnswer)) it(`accepts ${Editor[editor]} at its exact UTF-8 field boundary in ${cached ? "Inbox" : "Session"}`, () => {
  const f = fixture(editor, cached); f.mount(); f.activate();
  const value = "한".repeat(Math.floor(limits[editor] / 3)) + "x".repeat(limits[editor] % 3);
  const input = screen.getByRole("textbox", { name: names[editor] });
  fireEvent.change(input, { target: { value } });
  expect((input as HTMLTextAreaElement).value).toBe(value);
  expect(screen.queryByText(/Your previous draft was kept/)).toBeNull();
  if (cached) expect(f.save.mock.calls.at(-1)?.[0]).toBe(f.retained());
});

for (const cached of [false, true]) for (const editor of [Editor.OpenCodeAnswer, Editor.ClaudeAnswer, Editor.GrokAnswer]) it(`bounds the complete ${Editor[editor]} encoding with individually valid fields in ${cached ? "Inbox" : "Session"}`, () => {
  const f = fixture(editor, cached, 4); f.mount(); f.activate();
  const inputs = screen.getAllByRole("textbox", { name: editor === Editor.GrokAnswer ? /Exact answer for question/ : /Custom answer for question/ });
  for (let i = 0; i < 3; i++) fireEvent.change(inputs[i], { target: { value: "a".repeat(64 * 1024) } });
  fireEvent.change(inputs[3], { target: { value: "Previous" } });
  const previous = f.retained(); f.save.mockClear();
  fireEvent.change(inputs[3], { target: { value: "b".repeat(64 * 1024) } });
  expect((inputs[3] as HTMLTextAreaElement).value).toBe("Previous");
  expect(f.retained()).toBe(previous);
  expect(f.save).not.toHaveBeenCalled();
  expect(screen.getByText(/complete response exceeds 256 KiB/)).toBeTruthy();
  expect(f.send).not.toHaveBeenCalled();
});

for (const cached of [false, true]) it(`accepts an exact complete Claude response boundary and rejects the next byte in ${cached ? "Inbox" : "Session"}`, async () => {
  const f = fixture(Editor.ClaudeAnswer, cached); f.mount(); f.activate();
  const overhead = nativeResponseByteLength({ claude: { behavior: "allow", answers: { "Question 1": "" } } });
  const value = "x".repeat(nativeResponseLimit - overhead);
  const input = screen.getByRole("textbox", { name: names[Editor.ClaudeAnswer] });
  fireEvent.change(input, { target: { value } });
  expect((input as HTMLTextAreaElement).value).toBe(value);
  const previous = f.retained(); f.save.mockClear();
  fireEvent.change(input, { target: { value: value + "x" } });
  expect((input as HTMLTextAreaElement).value).toBe(value);
  expect(f.retained()).toBe(previous); expect(f.save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Send answers to Claude" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(1));
  expect(f.send.mock.calls[0][0].responseJson.length).toBe(nativeResponseLimit);
});

for (const cached of [false, true]) for (const editor of [Editor.OpenCodeAnswer, Editor.GrokAnswer]) it(`accepts the exact complete ${Editor[editor]} boundary in ${cached ? "Inbox" : "Session"}`, () => {
  const f = fixture(editor, cached, 4); f.mount(); f.activate();
  const inputs = screen.getAllByRole("textbox", { name: editor === Editor.GrokAnswer ? /Exact answer for question/ : /Custom answer for question/ });
  const empty = editor === Editor.GrokAnswer
    ? { grok: { outcome: "accepted", answers: Object.fromEntries(Array.from({ length: 4 }, (_, i) => [`Question ${i + 1}`, ""])) } }
    : { opencode: { answers: [[""], [""], [""], [""]] } };
  for (let i = 0; i < 3; i++) fireEvent.change(inputs[i], { target: { value: "a".repeat(64 * 1024) } });
  const last = "b".repeat(64 * 1024 - nativeResponseByteLength(empty));
  fireEvent.change(inputs[3], { target: { value: last } });
  expect((inputs[3] as HTMLTextAreaElement).value).toBe(last);
  expect(screen.queryByText(/Your previous draft was kept/)).toBeNull();
  const previous = f.retained(); f.save.mockClear();
  fireEvent.change(inputs[3], { target: { value: last + "b" } });
  expect((inputs[3] as HTMLTextAreaElement).value).toBe(last);
  expect(f.retained()).toBe(previous); expect(f.save).not.toHaveBeenCalled();
});

it("rejects Grok partial-selection and outcome changes that activate an oversized response", () => {
  const f = fixture(Editor.GrokAnswer, true, 4); f.mount();
  fireEvent.change(screen.getByRole("combobox", { name: "Question response" }), { target: { value: "skip_interview" } });
  for (const input of screen.getAllByRole("textbox", { name: /Exact answer/ })) fireEvent.change(input, { target: { value: "a".repeat(64 * 1024) } });
  const partial = screen.getAllByRole("checkbox", { name: "Include this partial answer" });
  for (let i = 0; i < 3; i++) fireEvent.click(partial[i]);
  const previous = f.retained(); f.save.mockClear();
  fireEvent.click(partial[3]);
  expect((partial[3] as HTMLInputElement).checked).toBe(false);
  expect(f.retained()).toBe(previous); expect(f.save).not.toHaveBeenCalled();
  fireEvent.change(screen.getByRole("combobox", { name: "Question response" }), { target: { value: "accepted" } });
  expect((screen.getByRole("combobox", { name: "Question response" }) as HTMLSelectElement).value).toBe("skip_interview");
  expect(f.retained()).toBe(previous); expect(f.save).not.toHaveBeenCalled();
});

it("bounds combined Grok answers and notes before saving notes", () => {
  const f = fixture(Editor.GrokAnswer, true, 2); f.mount();
  for (const input of screen.getAllByRole("textbox", { name: /Exact answer/ })) fireEvent.change(input, { target: { value: "a".repeat(64 * 1024) } });
  const notes = screen.getAllByRole("textbox", { name: /Notes for question/ });
  fireEvent.change(notes[0], { target: { value: "n".repeat(64 * 1024) } });
  const previous = f.retained(); f.save.mockClear();
  fireEvent.change(notes[1], { target: { value: "n".repeat(64 * 1024) } });
  expect((notes[1] as HTMLTextAreaElement).value).toBe("");
  expect(f.retained()).toBe(previous); expect(f.save).not.toHaveBeenCalled();
  expect(screen.getByText(/complete response exceeds 256 KiB/)).toBeTruthy();
});

it.each(["\n", "<", "&", "\u2028"])("includes JSON escaping in the complete OpenCode feedback bound: %j", (character) => {
  const f = fixture(Editor.OpenCodeFeedback, true); f.mount(); f.activate();
  const input = screen.getByRole("textbox", { name: /^Correction feedback/ });
  fireEvent.change(input, { target: { value: "Previous" } }); f.save.mockClear();
  // Newlines double in JSON; three-byte separators expand to six bytes in Go.
  const value = character.repeat(character === "\n" ? 64 * 1024 : 44000);
  if (character === "\n") {
    fireEvent.change(input, { target: { value } });
    expect((input as HTMLTextAreaElement).value).toBe(value);
  } else {
    // U+2028 must stay within the 64 KiB field bound while encoding still grows.
    const text = character === "\u2028" ? character.repeat(21845) : value;
    fireEvent.change(input, { target: { value: text } });
    if (character === "\u2028") expect((input as HTMLTextAreaElement).value).toBe(text);
    else { expect((input as HTMLTextAreaElement).value).toBe("Previous"); expect(f.save).not.toHaveBeenCalled(); }
  }
});

it("keeps the original uncertain response bytes after rejecting an edit", async () => {
  const f = fixture(Editor.ClaudeAnswer, true); f.send.mockRejectedValueOnce(new ConnectError("Lost acknowledgment", Code.Unavailable));
  f.mount(); f.activate();
  const input = screen.getByRole("textbox", { name: names[Editor.ClaudeAnswer] });
  fireEvent.change(input, { target: { value: "Original response" } });
  fireEvent.change(input, { target: { value: "x".repeat(nativeResponseLimit) } });
  fireEvent.click(screen.getByRole("button", { name: "Send answers to Claude" }));
  await screen.findByRole("button", { name: "Retry the same response request" });
  fireEvent.click(screen.getByRole("button", { name: "Retry the same response request" }));
  await waitFor(() => expect(f.send).toHaveBeenCalledTimes(2));
  expect(f.send.mock.calls[0][0]).toEqual(f.send.mock.calls[1][0]);
  expect(JSON.parse(new TextDecoder().decode(f.send.mock.calls[0][0].responseJson))).toEqual({ claude: { behavior: "allow", answers: { "Question 1": "Original response" } } });
});
