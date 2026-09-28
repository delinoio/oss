import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { newRequestId } from "@delinoio/delidev-api-client";
import { NativeClaudeDenialCompletion } from "./native-claude-denial-completion";
import { object, type Document } from "./documents";

function fixture(): Document {
  const input = newRequestId(), interaction = newRequestId(), context = newRequestId(), result = newRequestId();
  return { execution_id: newRequestId(), input_id: input, native_thread_id: newRequestId(), native_turn_id: newRequestId(), outcome: "stopped", claude_interruption: { interaction_id: interaction, context_id: context, result_id: result }, claude_denial: { input_id: input, interaction_id: interaction, arrival_id: newRequestId(), context_id: context, result_id: result, command_native_id: newRequestId(), idle_native_id: newRequestId(), native_input_id: null, cleanup_verified: true } };
}

test("keeps original denied-run cleanup separate from absent input result and later workspace report", () => {
  const p = fixture(), view = render(<NativeClaudeDenialCompletion progress={p} />);
  expect(screen.getByText(/without an input result identity/)).toBeTruthy();
  expect(screen.getByText("Confirmed")).toBeTruthy();
  expect(screen.getByText("Not confirmed")).toBeTruthy();
  p.cleanup_verified = true;
  view.rerender(<NativeClaudeDenialCompletion progress={p} />);
  expect(screen.getAllByText("Confirmed")).toHaveLength(2);
  expect(screen.getByText(/does not grant Resume/)).toBeTruthy();
  expect(screen.queryByRole("button")).toBeNull();
});

test.each(["input", "result", "arrival", "reused", "turn", "native-input", "missing-null", "cleanup", "report", "outcome", "mixed", "stop", "extra"])("rejects unproved denial cleanup: %s", (change) => {
  const p = fixture(), v = object(p.claude_denial);
  if (change === "input") v.input_id = newRequestId();
  if (change === "result") v.result_id = newRequestId();
  if (change === "arrival") v.arrival_id = "foreign";
  if (change === "reused") v.idle_native_id = v.command_native_id;
  if (change === "turn") v.command_native_id = p.native_turn_id;
  if (change === "native-input") v.native_input_id = p.input_id;
  if (change === "missing-null") delete v.native_input_id;
  if (change === "cleanup") v.cleanup_verified = false;
  if (change === "report") p.cleanup_verified = "true";
  if (change === "outcome") p.outcome = "succeeded";
  if (change === "mixed") p.claude_terminal = {};
  if (change === "stop") p.claude_stop = {};
  if (change === "extra") v.future = true;
  render(<NativeClaudeDenialCompletion progress={p} />);
  expect(screen.getByText(/denial cleanup is unavailable/)).toBeTruthy();
});
