// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { PromptHistoryNavigation } from "./project-prompt-history";
describe("project first-prompt navigation", () => {
 it("freezes order, preserves exact draft/caret, and stops at the oldest entry", () => {
  const navigation = new PromptHistoryNavigation(); const draft = { text: " current\n한글 ", start: 0, end: 0 };
  expect(navigation.move("newer", ["new", "old"], draft)).toBeUndefined();
  expect(navigation.move("older", ["new", "old"], draft)).toEqual({ text: "new", start: 0, end: 0 });
  expect(navigation.move("older", ["later", "new", "old"], draft)).toEqual({ text: "old", start: 0, end: 0 });
  expect(navigation.move("older", [], draft)).toBeUndefined();
  expect(navigation.move("newer", [], draft)).toEqual({ text: "new", start: 3, end: 3 });
  expect(navigation.move("newer", [], draft)).toEqual(draft);
 });
 it("edits and scope changes start a new cycle without restoring prior drafts", () => {
  const navigation = new PromptHistoryNavigation(); navigation.move("older", ["old"], { text: "initial", start: 0, end: 0 }); navigation.reset();
  const edited = { text: "edited", start: 0, end: 0 };
  navigation.move("older", ["next"], edited);
  expect(navigation.move("newer", [], edited)).toEqual(edited);
 });
});
