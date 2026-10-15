// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { PromptEditHistory, PromptEditKind } from "./prompt-edit-history";

const snap = (value: string) => ({
  value,
  bindings: [],
  selection: {
    start: value.length,
    end: value.length,
    direction: "none" as const,
  },
});

it("coalesces typing and keeps discrete edits as separate steps", () => {
  const history = new PromptEditHistory<never>();
  history.record(snap(""), snap("a"), PromptEditKind.Typing, true);
  history.record(snap("a"), snap("ab"), PromptEditKind.Typing, true);
  history.record(snap("ab"), snap("ab [x](y)"), PromptEditKind.Discrete, false);
  expect(history.undo(snap("ab [x](y)"))?.value).toBe("ab");
  expect(history.undo(snap("ab"))?.value).toBe("");
  expect(history.undo(snap(""))).toBeUndefined();
  expect(history.redo(snap(""))?.value).toBe("ab");
  expect(history.redo(snap("ab"))?.value).toBe("ab [x](y)");
  expect(history.redo(snap("ab [x](y)"))).toBeUndefined();
});

it("drops stale history when the draft changed outside the adapter", () => {
  const history = new PromptEditHistory<never>();
  history.record(snap(""), snap("a"), PromptEditKind.Typing, true);
  expect(history.undo(snap("restored"))).toBeUndefined();
});

it("truncates redo entries after a new edit and reverts a rejected step", () => {
  const history = new PromptEditHistory<never>();
  history.record(snap(""), snap("a"), PromptEditKind.Discrete, false);
  history.record(snap("a"), snap("b"), PromptEditKind.Discrete, false);
  expect(history.undo(snap("b"))?.value).toBe("a");
  history.revert("undo");
  expect(history.undo(snap("b"))?.value).toBe("a");
  history.record(snap("a"), snap("c"), PromptEditKind.Discrete, false);
  expect(history.redo(snap("c"))).toBeUndefined();
});
