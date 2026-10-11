// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "vitest";
import { claudeRetainedJSONBytes } from "./native-claude-json";
import { claudeCitationHistoryBytes } from "./native-claude-citations";

test.each(["<", ">", "&", "\u2028", "\u2029"])("counts Go's six-byte escape for %s", (character) => {
  expect(claudeRetainedJSONBytes({ text: character })).toBe(17);
  expect(claudeCitationHistoryBytes({ text: character })).toBe(17);
});

test("preserves ordinary JSON escaping and UTF-8 bytes", () => {
  expect(claudeRetainedJSONBytes({ text: '"\\\n🐦' })).toBe(21);
  expect(claudeRetainedJSONBytes({ text: "<>&\u2028\u2029" })).toBe(41);
});
