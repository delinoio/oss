// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { readCodexReasoning } from "./codex-reasoning-observation";
const snapshot = (summary: string[] = [], content: string[] = []) => ({ kind: "reasoning", text: "", summary, content });
it("selects an original final summary first without trimming, splitting or reconstructing streamed text", () => {
  const started = snapshot(["Initial summary"]), completed = snapshot([" \n", "  Final\nsummary🙂  "]);
  const source = { started, completed, deltas: [{ sequence: 7, delta: { kind: "reasoning-summary", index: 2, text: "Different stream" } }] };
  expect(readCodexReasoning(source, "complete")?.summary).toBe("  Final\nsummary🙂  ");
  expect(readCodexReasoning({ ...source, completed: snapshot() }, "complete")?.summary).toBe("Initial summary");
  expect(readCodexReasoning({ ...source, started: snapshot(), completed: snapshot() }, "complete")?.summary).toBeUndefined();
  expect(source.deltas[0].delta.text).toBe("Different stream");
});
it("retains the original summary/content families, zero-based indices and ordered delta kinds independently", () => {
  const source = { started: snapshot(["", "initial"], ["\tinitial content\n"]), completed: snapshot(["final"], ["final content"]), deltas: [
    { sequence: 4, delta: { kind: "reasoning-summary-added", index: 2, text: "" } },
    { sequence: 7, delta: { kind: "reasoning-content", index: 4, text: "<script>native()</script>" } },
    { sequence: 9, delta: { kind: "reasoning-summary", index: 2, text: "independent summary" } },
  ] };
  const result = readCodexReasoning(source, "complete")!;
  expect(result.initial.summary).toEqual([{ index: 0, text: "" }, { index: 1, text: "initial" }]);
  expect(result.initial.content[0].text).toBe("\tinitial content\n");
  expect(result.deltas.map(({ sequence, kind, index, text }) => ({ sequence, kind, index, text }))).toEqual(source.deltas.map(value => ({ sequence: value.sequence, ...value.delta })));
  expect(result.final?.content).toEqual([{ index: 0, text: "final content" }]);
});
it("does not recognize malformed, unknown, cross-kind or contradictory sources as simplified reasoning", () => {
  const empty = snapshot();
  for (const artifact of [
    { started: { ...empty, summary: [null] }, completed: empty },
    { started: { ...empty, content: null }, completed: empty },
    { started: empty, completed: empty, deltas: null },
    { started: empty, completed: empty, deltas: [{ sequence: 2, delta: { kind: "reasoning-summary", text: "one", index: 0 } }, { sequence: 2, delta: { kind: "reasoning-summary", text: "duplicate sequence", index: 1 } }] },
    { started: empty, completed: { ...empty, kind: "plan" } },
    { started: empty, completed: empty, deltas: [{ sequence: 1, delta: { kind: "reasoning-text", text: "foreign", index: null } }] },
    { started: empty, completed: empty, deltas: [{ sequence: 1, delta: { kind: "reasoning-summary", text: "invalid index", index: 1024 } }] },
    { started: empty, completed: empty, deltas: [{ sequence: 1, delta: { kind: "reasoning-summary-added", text: "fabricated", index: 0 } }] },
  ]) expect(readCodexReasoning(artifact, "complete")).toBeUndefined();
  expect(readCodexReasoning({ started: empty }, "complete")).toBeUndefined();
  expect(readCodexReasoning({ started: empty, completed: empty }, "streaming")).toBeUndefined();
  expect(readCodexReasoning({ started: empty }, "unknown-state")).toBeUndefined();
  expect(readCodexReasoning({ started: snapshot(["\0"]) }, "streaming")).toBeUndefined();
  expect(readCodexReasoning({ started: snapshot(["\ud800"]) }, "streaming")).toBeUndefined();
});
