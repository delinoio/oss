import { render, screen } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { NativeFunctionOutput } from "./native-function-output";

function fixture() {
  const snapshot = { kind: "function-call-output", text: "", summary: null, content: null, function_output: { name: "original_tool", namespace: "original_namespace", variant: "structured", parts: [
    { kind: "input_text", text: "Original <script>inert()</script> 한글" },
    { kind: "input_image", reference: "file_id", detail: "original" },
    { kind: "encrypted_content" },
    { kind: "input_audio", reference: "audio_url" },
    { kind: "input_text", text: "After protected content" },
  ] } };
  return { started: snapshot, completed: snapshot };
}
it("shows exact ordered safe output as inert text without media or retrieval", () => {
  const fetch = vi.spyOn(globalThis, "fetch");
  try {
    const { container } = render(<NativeFunctionOutput artifact={fixture()} state="complete" />);
    expect(screen.getByText("Function output · original_namespace · original_tool")).toBeTruthy();
    expect([...container.querySelectorAll("li")].map(item => item.textContent)).toEqual(["Original <script>inert()</script> 한글", "Image reference · file_id · original", "Protected content present", "Audio reference", "After protected content"]);
    expect(container.querySelector("script, img, audio, video, a, button, iframe")).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  } finally { fetch.mockRestore(); }
});
it("preserves string and explicit empty structured output without inventing content", () => {
  const snapshot = { kind: "function-call-output", text: "", function_output: { name: "tool", namespace: null, variant: "string", text: "Original string\n", parts: null } };
  const { container, rerender } = render(<NativeFunctionOutput artifact={{ started: snapshot, completed: snapshot }} state="complete" />);
  expect(container.querySelector("pre")?.textContent).toBe("Original string\n");
  const empty = { ...snapshot, function_output: { name: "tool", namespace: "", variant: "structured", parts: [] } };
  rerender(<NativeFunctionOutput artifact={{ started: empty, completed: empty }} state="complete" />);
  expect(container.querySelectorAll("li")).toHaveLength(0);
  expect(screen.queryByRole("status")).toBeNull();
});
it.each(["namespace", "private", "reference", "variant", "deltas", "missing", "oversized"])("rejects changed or malformed output: %s", fault => {
  const artifact = structuredClone(fixture());
  const output = artifact.completed.function_output;
  if (fault === "namespace") output.namespace = "changed";
  if (fault === "private") Object.assign(output.parts[2], { encrypted_content: "private" });
  if (fault === "reference") Object.assign(output.parts[1], { image_url: "https://never-fetch.invalid" });
  if (fault === "variant") output.variant = "dynamic-tool";
  if (fault === "deltas") Object.assign(artifact, { deltas: [{ delta: { kind: "plan-text", text: "x" } }] });
  if (fault === "missing") Object.assign(artifact, { completed: null });
  if (fault === "oversized") output.parts[0].text = "한".repeat(90000);
  const { container } = render(<NativeFunctionOutput artifact={artifact} state="complete" />);
  expect(screen.getByRole("status")).toBeTruthy();
  expect(container.querySelector("pre, img, audio, a")).toBeNull();
});
