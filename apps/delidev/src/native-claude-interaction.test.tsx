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
