import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { NativeClaudeMessage } from "./native-claude-message";

afterEach(cleanup);
function fixture() {
  return { model: "fixture-model", blocks: [
    { index: 0, block: { kind: "thinking", text: "Original reasoning 🐦" }, state: "stopped" },
    { index: 1, block: { kind: "redacted_thinking", text: "" }, state: "stopped" },
    { index: 2, block: { kind: "text", text: "First <script>native()</script> block" }, state: "stopped" },
    { index: 3, block: { kind: "text", text: "Second 한국어 block" }, state: "stopped" },
  ], stop_reason: "end_turn" as string | null, stop_sequence: "" as string | null };
}
test("keeps whole native messages, ordered blocks and inert text", () => {
  const { container } = render(<NativeClaudeMessage content={fixture()} state="complete" />);
  const blocks = screen.getAllByRole("listitem");
  expect(blocks).toHaveLength(4);
  expect(blocks[0]?.textContent).toContain("Original reasoning 🐦");
  expect(blocks[1]?.textContent).toContain("redacted");
  expect(blocks[2]?.textContent).toContain("<script>native()</script>");
  expect(blocks[3]?.textContent).toContain("Second 한국어 block");
  expect(container.querySelector("script")).toBeNull();
  expect(screen.getByText("Native stop reason: end_turn")).toBeTruthy();
  expect(screen.getByText("Native stop sequence")).toBeTruthy();
  expect(container.querySelectorAll("button,a,input")).toHaveLength(0);
});
test("retains partial content without fabricating provider closure", () => {
  const content = fixture();
  content.blocks = [{ index: 0, block: { kind: "text", text: "" }, state: "streaming" }];
  content.stop_reason = null; content.stop_sequence = null;
  const { rerender } = render(<NativeClaudeMessage content={content} state="streaming" />);
  expect(screen.getByText("Streaming")).toBeTruthy();
  expect(screen.queryByText(/Native stop reason/)).toBeNull();
  rerender(<NativeClaudeMessage content={content} state="complete" />);
  expect(screen.getByLabelText("Claude message unavailable")).toBeTruthy();
});
for (const [name, change] of Object.entries<(value: ReturnType<typeof fixture>) => void>({
  index: (v) => { v.blocks[1]!.index = 0; },
  kind: (v) => { v.blocks[1]!.block.kind = "tool_use"; },
  redacted: (v) => { v.blocks[1]!.block.text = "private opaque data"; },
  state: (v) => { v.blocks[0]!.state = "streaming"; },
  stop: (v) => { v.stop_reason = "unknown"; },
  overflow: (v) => { v.blocks[2]!.block.text = "🐦".repeat(65537); },
  malformed: (v) => { v.blocks[2]!.block.text = "\ud800"; },
  signature: (v) => { Object.assign(v.blocks[0]!.block, { signature: "private" }); },
})) test(`rejects ${name} without rendering partial content`, () => {
  const content = fixture(); change(content);
  render(<NativeClaudeMessage content={content} state="complete" />);
  expect(screen.getByLabelText("Claude message unavailable")).toBeTruthy();
  expect(screen.queryByText("Original reasoning 🐦")).toBeNull();
});
