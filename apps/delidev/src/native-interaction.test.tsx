import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeInteraction } from "./native-interaction";

const question = { text: "Original <script>question()</script>🙂", header: "Choice", options: [{ label: "First", description: "Original description" }, { label: "Second", description: "Another choice" }], multiple: true };
function fixture(approval = false) {
  return {
    type: approval ? "native-approval" : "user-question", closure: "open",
    native_request_id: { kind: "text", text: `${approval ? "per" : "que"}_01960dcbe1faABCDEFGHIJKLMN` },
    native_thread_id: "ses_01960dcbe1faABCDEFGHIJKLMN", native_turn_id: "msg_01960dcbe1faABCDEFGHIJKLMN", native_item_id: "prt_01960dcbe1fbABCDEFGHIJKLMN",
    opencode: { version: "1.18.32", native_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", native_message_id: "msg_01960dcbe1fbABCDEFGHIJKLMN", call_id: "original-call", permission: approval ? { name: "read", patterns: ["original/*.env", ""], always: ["*.env"], metadata_json: '{"exact":9007199254740993,"content":"<script>metadata()</script>"}' } : undefined, questions: approval ? undefined : [structuredClone(question)] },
  };
}

it("retains original question order and optional flags without exposing unsupported sends", () => {
  const { container } = render(<NativeInteraction data={fixture()} />);
  expect(screen.getByText(question.text)).toBeTruthy();
  expect(screen.getByText("Multiple choices: Allowed · Custom answers: Native default")).toBeTruthy();
  expect([...container.querySelectorAll("strong")].map((e) => e.textContent)).toEqual(["First", "Second"]);
  expect(container.querySelector("button, input, textarea, script, a")).toBeNull();
  expect(screen.getByText(/This request is pending/)).toBeTruthy();
});

it("preserves native permission patterns and exact JSON metadata as inert content", () => {
  const data = fixture(true);
  const { container } = render(<NativeInteraction data={data} />);
  expect(screen.getByText("Requested permission: read")).toBeTruthy();
  expect([...container.querySelectorAll("pre")].map((e) => e.textContent)).toEqual([...data.opencode.permission!.patterns, ...data.opencode.permission!.always, data.opencode.permission!.metadata_json]);
  expect(container.querySelector("button, input, textarea, script, a")).toBeNull();
});

it("keeps an explicitly empty original matrix distinct from missing questions", () => {
  const data = fixture(); data.opencode.questions = [];
  const { rerender } = render(<NativeInteraction data={data} />);
  expect(screen.getByText("The original question list is empty.")).toBeTruthy();
  data.opencode.questions = undefined;
  rerender(<NativeInteraction data={data} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});

it.each([
  { name: "foreign request namespace", change: (d: ReturnType<typeof fixture>) => { d.native_request_id.text = "per_01960dcbe1faABCDEFGHIJKLMN"; } },
  { name: "input used as assistant", change: (d: ReturnType<typeof fixture>) => { d.opencode.native_message_id = d.native_turn_id; } },
  { name: "changed version", change: (d: ReturnType<typeof fixture>) => { d.opencode.version = "unknown"; } },
  { name: "missing tool", change: (d: ReturnType<typeof fixture>) => { d.opencode.call_id = ""; } },
  { name: "invalid Unicode", change: (d: ReturnType<typeof fixture>) => { d.opencode.questions![0]!.text = "\uD800"; } },
])("refuses $name before presenting native content", ({ change }) => {
  const data = fixture(); change(data);
  const { container } = render(<NativeInteraction data={data} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(container.querySelector("pre, input, button")).toBeNull();
});

it("refuses mixed response authority rather than rendering another harness form", () => {
  const { container } = render(<NativeInteraction data={{ ...fixture(), approval_response: { state: "queued" } }} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(container.querySelector("input, button")).toBeNull();
});

it("retains original content after its matching response is queued", () => {
  const { container } = render(<NativeInteraction data={{ ...fixture(), response: { state: "queued", input: { opencode: { answers: [["First"]] } } } }} />);
  expect(screen.getByText(question.text)).toBeTruthy();
  expect(screen.queryByText(/unavailable or inconsistent/)).toBeNull();
  expect(container.querySelector("input, button")).toBeNull();
});

function policyFixture() {
  return { ...fixture(true), closure: "native-closed", opencode_closure: { native_event_id: "evt_01960dcbe1fcABCDEFGHIJKLMN", proposal_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", decision: "always", sources: [{ interaction_id: "01960dcb-e1fa-7000-8000-000000000001", native_request_id: "per_01960dcbe1fbABCDEFGHIJKLMN" }] } };
}

it.each(["always", "reject"])("shows automatic native %s without implying another direct response", (decision) => {
  const data = policyFixture(); data.opencode_closure.decision = decision;
  const { container } = render(<NativeInteraction data={data} />);
  expect(screen.getByText(/No native response was sent for this request/)).toBeTruthy();
  expect(screen.getByText(decision === "always" ? /automatically allowed/ : /automatically rejected/)).toBeTruthy();
  expect(container.querySelector("button, input, textarea")).toBeNull();
});

it.each(["open", "proposal", "duplicate", "self", "namespace", "direct"])("refuses inconsistent native policy closure: %s", (changed) => {
  const data: Record<string, unknown> = policyFixture();
  const proof = data.opencode_closure as ReturnType<typeof policyFixture>["opencode_closure"];
  switch (changed) {
    case "open": data.closure = "open"; break;
    case "proposal": proof.proposal_event_id = "evt_01960dcbe1ffABCDEFGHIJKLMN"; break;
    case "duplicate": proof.sources.push({ ...proof.sources[0]! }); break;
    case "self": proof.sources[0]!.native_request_id = fixture(true).native_request_id.text; break;
    case "namespace": proof.sources[0]!.native_request_id = "que_01960dcbe1fbABCDEFGHIJKLMN"; break;
    case "direct": data.approval_response = { state: "accepted", input: { opencode: { decision: "always" } } }; break;
  }
  const { container } = render(<NativeInteraction data={data} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(container.querySelector("button, input, pre")).toBeNull();
});

function stopFixture(approval = false) {
  return { ...fixture(approval), closure: "turn-ended", opencode_stop: { proposal_event_id: "evt_01960dcbe1faABCDEFGHIJKLMN", tool_interrupted: true, stop: { request_id: "01960dcb-e1fa-7000-8000-000000000001", input_request_id: "01960dcb-e1fa-7000-8000-000000000002", input_part_id: "prt_01960dcbe1faABCDEFGHIJKLMN", assistant_id: "msg_01960dcbe1fbABCDEFGHIJKLMN", history_digest: "ab".repeat(32), http_accepted: false, interrupted_observed: true, terminal_observed: true, idle_observed: true, pending_cleared: true, cleanup_verified: true } } };
}

it.each([false, true])("shows unanswered Stop cancellation independently of native replies: approval=%s", (approval) => {
  const { container } = render(<NativeInteraction data={stopFixture(approval)} />);
  expect(screen.getByText(/canceled after Stop and verified process cleanup/)).toBeTruthy();
  expect(screen.getByText(/No answer or rejection was sent/)).toBeTruthy();
  expect(container.querySelector("button, input, textarea")).toBeNull();
});

it.each(["open", "proposal", "cleanup", "idle", "terminal", "pending", "tool", "unobserved", "digest", "self", "direct", "mixed"])("refuses inconsistent original Stop closure: %s", (changed) => {
  const data: Record<string, unknown> = stopFixture();
  const proof = data.opencode_stop as ReturnType<typeof stopFixture>["opencode_stop"];
  switch (changed) {
    case "open": data.closure = "open"; break;
    case "proposal": proof.proposal_event_id = "evt_01960dcbe1ffABCDEFGHIJKLMN"; break;
    case "cleanup": Object.assign(proof.stop, { cleanup_verified: "true" }); break;
    case "idle": proof.stop.idle_observed = false; break;
    case "terminal": proof.stop.terminal_observed = false; break;
    case "pending": proof.stop.pending_cleared = false; break;
    case "tool": proof.tool_interrupted = false; break;
    case "unobserved": proof.stop.interrupted_observed = false; break;
    case "digest": proof.stop.history_digest = "AB".repeat(32); break;
    case "self": proof.stop.request_id = proof.stop.input_request_id; break;
    case "direct": data.response = { state: "accepted", input: { opencode: { answers: [["First"]] } } }; break;
    case "mixed": data.opencode_closure = policyFixture().opencode_closure; break;
  }
  const { container } = render(<NativeInteraction data={data} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(container.querySelector("button, input, pre")).toBeNull();
});

it.each(["original", "duplicate", "fraction", "missing", "both"])("validates native retry cancellation separately: %s", (changed) => {
  const data = stopFixture();
  const retry = { native_event_id: "evt_01960dcbe1fcABCDEFGHIJKLMN", attempt: 1, next: 12345 };
  const stop = { ...data.opencode_stop.stop, http_accepted: true, interrupted_observed: false, retry_canceled_observed: true, retry_observations: [retry] };
  if (changed === "duplicate") stop.retry_observations.push({ ...retry });
  if (changed === "fraction") retry.attempt = 1.5;
  if (changed === "missing") stop.retry_observations = [];
  if (changed === "both") stop.interrupted_observed = true;
  render(<NativeInteraction data={{ ...data, opencode_stop: { ...data.opencode_stop, stop } }} />);
  if (changed === "original") expect(screen.getByText(/canceled after Stop and verified process cleanup/)).toBeTruthy();
  else expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});

 it("displays Stop with unknown cleanup without presenting a confirmation", () => {
  const data = stopFixture(); data.opencode_stop.stop.cleanup_verified = false;
  render(<NativeInteraction data={data} />);
  expect(screen.getByText(/Process cleanup remains unconfirmed/)).toBeTruthy();
  expect(screen.queryByText(/verified process cleanup/)).toBeNull();
 });
