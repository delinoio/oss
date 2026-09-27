import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeClaudeTool } from "./native-claude-tool";
afterEach(cleanup);
const id = "01900000-0000-7000-8000-000000000001";
const props = { id, native: "tool_original", parent: "msg_original", state: "complete" };
function fixture() {
  return { reference: { id, native_id: props.native, name: "Read" }, message_id: "01900000-0000-7000-8000-000000000002", native_message_id: props.parent, index: 0, caller: null as string | null, initial_input: "{}", input_delta: '{ "n":9007199254740993 }' as string | null, proposal: { proposed: '{ "n":9007199254740993 }', applied: '{"n":9007199254740993,"native":true}' } as { proposed: string; applied: string } | null, result: { native_event_id: "123e4567-e89b-42d3-a456-426614174000", is_error: null as boolean | null, text: '<script>native()</script> /private/original' as string | null, blocks: null as { kind: string; text: string }[] | null, structured: '{"n":9007199254740993}' as string | null } as { native_event_id: string; is_error: boolean | null; text: string | null; blocks: { kind: string; text: string }[] | null; structured: string | null } | null };
}
test("preserves original proposal, applied JSON, nullable error and inert result", () => {
  const { container } = render(<NativeClaudeTool {...props} content={fixture()} />);
  expect(screen.getAllByText('{ "n":9007199254740993 }')).toHaveLength(2);
  expect(screen.getByText('{"n":9007199254740993,"native":true}')).toBeTruthy();
  expect(screen.getByText("Native error flag: Not reported.")).toBeTruthy();
  expect(screen.getByText('<script>native()</script> /private/original')).toBeTruthy();
  expect(container.querySelectorAll("script,button,a,input")).toHaveLength(0);
  expect(container.querySelector("details")?.open).toBe(false);
});
test("distinguishes proposal from result and native error from session outcome", () => {
  const content = fixture(); content.result = null;
  const { rerender } = render(<NativeClaudeTool {...props} state="streaming" content={content} />);
  expect(screen.getByText("Read · Proposal complete")).toBeTruthy();
  expect(screen.getByText("No tool result has been observed.")).toBeTruthy();
  content.proposal = null;
  rerender(<NativeClaudeTool {...props} state="streaming" content={content} />);
  expect(screen.getByText("Read · Receiving proposal")).toBeTruthy();
  const complete = fixture(); complete.result!.is_error = true;
  complete.result!.text = null; complete.result!.blocks = [{ kind: "text", text: "" }, { kind: "text", text: "Original tool failure" }];
  rerender(<NativeClaudeTool {...props} content={complete} />);
  expect(screen.getByText("Native error flag: Error reported.")).toBeTruthy();
  expect(screen.getAllByRole("listitem")).toHaveLength(2);
});
for (const [name, mutate] of Object.entries<(v: ReturnType<typeof fixture>) => void>({
  owner: (v) => { v.reference.id = v.message_id; },
  native: (v) => { v.reference.native_id = "foreign"; },
  parent: (v) => { v.native_message_id = "foreign"; },
  index: (v) => { v.index = 0.5; },
  caller: (v) => { v.caller = "provider"; },
  proposal: (v) => { v.proposal!.proposed = "{}"; },
  lifecycle: (v) => { v.proposal = null; },
  mixed: (v) => { v.result!.blocks = []; },
  rich: (v) => { v.result!.text = null; v.result!.blocks = [{ kind: "thinking", text: "hidden" }]; },
  envelope: (v) => { v.result!.native_event_id = "foreign"; },
  overflow: (v) => { v.result!.text = "🐦".repeat(65537); },
  malformed: (v) => { v.input_delta = "\ud800"; },
  unknown: (v) => { Object.assign(v.result!, { permission: "approved" }); },
})) test(`rejects ${name} before displaying content`, () => {
  const v = fixture(); mutate(v);
  render(<NativeClaudeTool {...props} content={v} />);
  expect(screen.getByLabelText("Claude tool unavailable")).toBeTruthy();
  expect(screen.queryByText("Original proposed input")).toBeNull();
});

test("retains native string metadata only with an explicit error result", () => {
  const v = fixture(); v.result!.structured = '"Original native error\\nmetadata"'; v.result!.is_error = true;
  const { rerender } = render(<NativeClaudeTool {...props} content={v} />);
  expect(screen.getByText('"Original native error\\nmetadata"')).toBeTruthy();
  v.result!.is_error = null;
  rerender(<NativeClaudeTool {...props} content={v} />);
  expect(screen.getByLabelText("Claude tool unavailable")).toBeTruthy();
});

test("preserves original non-execution classification without treating generic errors as policy denial", () => {
  const v = fixture(); v.result!.is_error = true;
  Object.assign(v.result!, { non_execution: { id: props.native, non_execution_kind: "permission-rule" } });
  const rendered = render(<NativeClaudeTool {...props} content={v} />);
  expect(screen.getByText(/Native permission policy prevented this tool/)).toBeTruthy();
  v.result!.is_error = false;
  rendered.rerender(<NativeClaudeTool {...props} content={v} />);
  expect(screen.getByLabelText("Claude tool unavailable")).toBeTruthy();
});
