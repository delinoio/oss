// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "vitest";
import { claudeRetainedJSONBytes } from "./native-claude-json";
import { claudeCitationHistoryBytes } from "./native-claude-citations";

test.each(["<", ">", "&", "\u2028", "\u2029"])(
  "counts Go's six-byte JSON escape for %j",
  (character) => {
    expect(claudeRetainedJSONBytes({ text: character })).toBe(17);
    expect(claudeRetainedJSONBytes({ text: character.repeat(50_000) })).toBe(
      300_011,
    );
  },
);

test("preserves ordinary UTF-8 and JSON string escaping", () => {
  expect(claudeRetainedJSONBytes({ text: "é😀" })).toBe(17);
  expect(
    claudeRetainedJSONBytes({ text: '\b\f\n\r\t"\\<>&\u2028\u2029' }),
  ).toBe(55);
  expect(claudeRetainedJSONBytes(null)).toBe(4);
});

test("keeps citation byte accounting on the same implementation", () => {
  expect(claudeCitationHistoryBytes).toBe(claudeRetainedJSONBytes);
  expect(claudeCitationHistoryBytes({ text: "<>&\u2028\u2029" })).toBe(41);
});
