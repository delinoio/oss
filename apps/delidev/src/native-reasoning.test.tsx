import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { NativeReasoning } from "./native-reasoning";

function fixture() {
  return {
    started: { kind: "reasoning-text", text: "Original 한글\n", summary: null, content: null },
    deltas: [
      { sequence: 4, delta: { kind: "reasoning-text", index: null, text: "  retained " } },
      { sequence: 7, delta: { kind: "reasoning-text", index: null, text: "<script>untrusted()</script>🙂" } },
    ],
  };
}

it("presents original streaming and completed reasoning once as inert text", () => {
  const artifact = fixture();
  const expected = "Original 한글\n  retained <script>untrusted()</script>🙂";
  const { container, rerender } = render(<NativeReasoning artifact={artifact} state="streaming" />);
  const disclosure = screen.getByText("Reasoning · Streaming");
  expect(container.querySelector("details")?.open).toBe(false);
  fireEvent.click(disclosure);
  expect(container.querySelectorAll("pre")).toHaveLength(1);
  expect(container.querySelector("pre")?.textContent).toBe(expected);
  expect(container.querySelector("script")).toBeNull();
  rerender(<NativeReasoning artifact={{ ...artifact, completed: { ...artifact.started, text: expected } }} state="complete" />);
  expect(screen.getByText("Reasoning · Complete")).toBeTruthy();
  expect(container.querySelectorAll("pre")).toHaveLength(1);
  expect(container.querySelector("pre")?.textContent).toBe(expected);
});

it("preserves explicitly empty completed reasoning", () => {
  const snapshot = { kind: "reasoning-text", text: "", summary: null, content: null };
  const { container } = render(<NativeReasoning artifact={{ started: snapshot, completed: snapshot }} state="complete" />);
  expect(screen.getByText("Reasoning · Complete")).toBeTruthy();
  expect(container.querySelector("pre")?.textContent).toBe("");
});

it.each([
  { name: "missing completion", state: "complete", change: (value: Record<string, unknown>) => value },
  { name: "contradictory completion", state: "complete", change: (value: Record<string, unknown>) => ({ ...value, completed: { kind: "reasoning-text", text: "replacement", summary: null, content: null } }) },
  { name: "indexed summary", state: "streaming", change: (value: Record<string, unknown>) => ({ ...value, started: { kind: "reasoning-text", text: "", summary: ["foreign summary"], content: null } }) },
  { name: "foreign delta", state: "streaming", change: (value: Record<string, unknown>) => ({ ...value, deltas: [{ sequence: 4, delta: { kind: "reasoning-content", index: 0, text: "foreign" } }] }) },
  { name: "duplicate sequence", state: "streaming", change: (value: Record<string, unknown>) => ({ ...value, deltas: [fixture().deltas[0], fixture().deltas[0]] }) },
  { name: "imprecise sequence", state: "streaming", change: (value: Record<string, unknown>) => ({ ...value, deltas: [{ sequence: Number.MAX_SAFE_INTEGER + 1, delta: fixture().deltas[0].delta }] }) },
  { name: "oversized Unicode", state: "streaming", change: (value: Record<string, unknown>) => ({ ...value, started: { kind: "reasoning-text", text: "한".repeat(90000) } }) },
  { name: "unpaired surrogate", state: "streaming", change: (value: Record<string, unknown>) => ({ ...value, started: { kind: "reasoning-text", text: "\uD800" } }) },
])("keeps $name unavailable without partial or inferred text", ({ state, change }) => {
  const { container } = render(<NativeReasoning artifact={change(fixture())} state={state} />);
  expect(screen.getByText("Reasoning · Unavailable")).toBeTruthy();
  expect(container.querySelector("pre")).toBeNull();
});
