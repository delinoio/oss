import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeClaudeInteraction } from "./native-claude-interaction";
import type { Document } from "./documents";
afterEach(cleanup);
export function claudeRequestFixture(): Document {
  return { type: "native-approval", closure: "open", native_item_id: "tool_original", native_request_id: { kind: "text", text: "request_original" }, claude: {
    version: "2.1.236", kind: "tool-permission", arrival_id: "01900000-0000-7000-8000-000000000001", tool: { id: "01900000-0000-7000-8000-000000000002", native_id: "tool_original", name: "Bash" }, message_id: "01900000-0000-7000-8000-000000000003", native_message_id: "msg_original", index: 0, caller: null, input_json: '{ "command":"printf original", "exact":9007199254740993 }', metadata: {
      permission_suggestions: [{ type: "addRules", rules: [{ toolName: "Bash", ruleContent: "printf:*" }], behavior: "allow", destination: "session" }], blocked_path: null, decision_reason: null, decision_reason_type: null, requires_user_interaction: false, agent_id: null, title: "Original <script>native()</script> request", display_name: "Original tool", description: "Original reason",
    },
  } };
}
test("preserves callback inputs and suggestions without granting approval", () => {
  const { container } = render(<NativeClaudeInteraction data={claudeRequestFixture()} />);
  expect(screen.getByText('{ "command":"printf original", "exact":9007199254740993 }')).toBeTruthy();
  expect(screen.getByText("Original <script>native()</script> request")).toBeTruthy();
  expect(screen.getByText(/Native suggestions do not grant/)).toBeTruthy();
  expect(container.querySelectorAll("script,button,a,input,textarea")).toHaveLength(0);
});
test("shows original question keys, multiple selection and ordered choices", () => {
  const data = claudeRequestFixture(), r = data.claude as Document;
  data.type = "user-question"; r.kind = "user-question"; (r.tool as Document).name = "AskUserQuestion";
  r.input_json = JSON.stringify({ questions: [{ question: "Original question?", header: "Native header", multiSelect: true, options: [{ label: "One", description: "First native option" }, { label: "Two", description: "Second native option" }] }] });
  render(<NativeClaudeInteraction data={data} />);
  expect(screen.getByText("Original question?")).toBeTruthy();
  expect(screen.getByText("Multiple selections offered")).toBeTruthy();
  expect(screen.getByText("One — First native option")).toBeTruthy();
});
test("shows native enriched plan and canceled request without opening its path", () => {
  const data = claudeRequestFixture(), r = data.claude as Document;
  r.kind = "plan-approval"; (r.tool as Document).name = "ExitPlanMode";
  r.input_json = JSON.stringify({ plan: "# Original plan", planFilePath: "/private/original-plan.md" });
  data.closure = "native-closed"; data.claude_cancellation = { arrival_id: r.arrival_id };
  const { container } = render(<NativeClaudeInteraction data={data} />);
  expect(screen.getByText("# Original plan")).toBeTruthy();
  expect(screen.getByText("Native plan path: /private/original-plan.md")).toBeTruthy();
  expect(screen.getByText(/original native request was canceled/)).toBeTruthy();
  expect(container.querySelectorAll("a,button")).toHaveLength(0);
});
for (const change of ["mixed", "tool", "version", "arrival", "cancellation", "suggestion", "invalid-input", "overflow", "response"]) test(`rejects ${change} without displaying partial requests`, () => {
  const data = claudeRequestFixture(), r = data.claude as Document;
  switch (change) {
    case "mixed": data.opencode = {}; break;
    case "tool": data.native_item_id = "foreign"; break;
    case "version": r.version = "future"; break;
    case "arrival": r.arrival_id = "foreign"; break;
    case "cancellation": data.closure = "native-closed"; data.claude_cancellation = { arrival_id: "foreign" }; break;
    case "suggestion": (r.metadata as Document).permission_suggestions = [{ type: "unknown" }]; break;
    case "invalid-input": r.input_json = "[]"; break;
    case "overflow": (r.metadata as Document).description = "🐦".repeat(1025); break;
    case "response": data.approval_response = { state: "accepted" }; break;
  }
  render(<NativeClaudeInteraction data={data} />);
  expect(screen.getByLabelText("Claude request unavailable")).toBeTruthy();
  expect(screen.queryByText("Original reason")).toBeNull();
});

test("retains an original transmitted response and echo without claiming acceptance", () => {
  const data = claudeRequestFixture(), r = data.claude as Document;
  data.approval_response = { id: r.message_id, state: "transmitted", accepted_at: "2026-09-27T00:00:00Z", input: { claude: { behavior: "allow" } }, claim: { id: r.arrival_id, job_id: r.message_id, machine_id: r.arrival_id, instance_id: r.message_id, device_id: r.arrival_id, claimed_at: "2026-09-27T00:00:00Z" }, delivery: { state: "transmitted", sequence: 9 }, claude_echo: { arrival_id: r.arrival_id, body_sha256: "ab".repeat(32), sequence: 10 } };
  const rendered = render(<NativeClaudeInteraction data={data} />);
  expect(screen.getByText(/Claude echoed the original response/)).toBeTruthy();
  expect(screen.getByText(/Transmission and native acceptance are separate/)).toBeTruthy();
  ((data.approval_response as Document).claude_echo as Document).arrival_id = r.message_id;
  rendered.rerender(<NativeClaudeInteraction data={data} />);
  expect(screen.getByLabelText("Claude request unavailable")).toBeTruthy();
  expect(screen.queryByText("Original reason")).toBeNull();
});

for (const change of ["valid", "arrival", "tool", "result", "evidence", "sequence", "acceptance", "echo", "cancellation", "open"]) test(`validates original callback settlement: ${change}`, () => {
  const data = claudeRequestFixture(), r = data.claude as Document;
  const evidence = "native-claude-tool-result";
  data.closure = "native-closed";
  const settlement: Document = { arrival_id: r.arrival_id, tool_message_id: (r.tool as Document).id, result_native_id: "01900000-0000-7000-8000-000000000004", evidence, sequence: 12 };
  const response: Document = { id: r.message_id, state: "accepted", accepted_at: "2026-09-27T00:00:00Z", input: { claude: { behavior: "allow" } }, claim: { id: r.arrival_id, job_id: r.message_id, machine_id: r.arrival_id, instance_id: r.message_id, device_id: r.arrival_id, claimed_at: "2026-09-27T00:00:00Z" }, delivery: { state: "transmitted", sequence: 9 }, claude_echo: { arrival_id: r.arrival_id, body_sha256: "ab".repeat(32), sequence: 10 }, acceptance: { evidence, sequence: 12 } };
  data.claude_settlement = settlement; data.approval_response = response;
  switch (change) {
    case "arrival": settlement.arrival_id = r.message_id; break;
    case "tool": settlement.tool_message_id = r.message_id; break;
    case "result": settlement.result_native_id = "foreign"; break;
    case "evidence": settlement.evidence = "native-claude-question-answers"; break;
    case "sequence": settlement.sequence = 10; break;
    case "acceptance": delete response.acceptance; break;
    case "echo": delete response.claude_echo; break;
    case "cancellation": data.claude_cancellation = { arrival_id: r.arrival_id }; break;
    case "open": data.closure = "open"; break;
  }
  render(<NativeClaudeInteraction data={data} />);
  if (change === "valid") {
    expect(screen.getByText(/Claude processed the original callback/)).toBeTruthy();
    expect(screen.queryByText(/was canceled/)).toBeNull();
    expect(screen.queryByText(/does not prove.*answer was accepted/)).toBeNull();
  } else expect(screen.getByLabelText("Claude request unavailable")).toBeTruthy();
});
