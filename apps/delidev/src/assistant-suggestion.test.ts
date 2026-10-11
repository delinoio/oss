// SPDX-License-Identifier: Apache-2.0
import { expect, test, vi } from "vitest";
import {
  placeAssistantSuggestion,
  type SuggestionState,
} from "./assistant-suggestion";
const guard = {
  sessionId: "original",
  transport: {},
  generation: 1,
  revision: 2n,
  draftGeneration: 3n,
};
const state: SuggestionState = {
  ...guard,
  active: true,
  supported: true,
  draft: "",
  images: 0,
  skills: 0,
  blocked: false,
  composing: false,
};
test("explicit choice changes only the eligible unsent empty draft", () => {
  const change = vi.fn(() => true);
  expect(
    placeAssistantSuggestion(guard, state, "Original option", change),
  ).toBe(true);
  expect(change).toHaveBeenCalledExactlyOnceWith("Original option");
});
test.each([
  { draft: "existing" },
  { draft: " " },
  { images: 1 },
  { skills: 1 },
  { blocked: true },
  { composing: true },
  { active: false },
  { supported: false },
  { sessionId: "replacement" },
  { transport: {} },
  { generation: 2 },
  { revision: 3n },
  { draftGeneration: 4n },
])("preserves draft and original ownership after %s", (update) => {
  const change = vi.fn();
  expect(
    placeAssistantSuggestion(
      guard,
      { ...state, ...update },
      "Original",
      change,
    ),
  ).toBe(false);
  expect(change).not.toHaveBeenCalled();
});
test("rejects intervening same-value edits and once-only placement before another render", () => {
  let generation = guard.draftGeneration;
  const change = vi.fn(() => {
    generation += 1n;
    return true;
  });
  expect(
    placeAssistantSuggestion(
      guard,
      { ...state, draftGeneration: generation },
      "Original",
      change,
    ),
  ).toBe(true);
  expect(
    placeAssistantSuggestion(
      guard,
      { ...state, draftGeneration: generation },
      "Original",
      change,
    ),
  ).toBe(false);
  expect(change).toHaveBeenCalledTimes(1);
});
test("failed draft admission and invalid native text create no alternative action", () => {
  expect(placeAssistantSuggestion(guard, state, "Original", () => false)).toBe(
    false,
  );
  const change = vi.fn();
  expect(placeAssistantSuggestion(guard, state, "x\0y", change)).toBe(false);
  expect(
    placeAssistantSuggestion(guard, state, "x".repeat(262145), change),
  ).toBe(false);
  expect(change).not.toHaveBeenCalled();
});
