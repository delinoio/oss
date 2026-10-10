// SPDX-License-Identifier: Apache-2.0
import { expect, test } from "vitest";
import { claudeRetainedJSONBytes } from "./native-claude-json";

test("counts Go JSON HTML and line separators as six-byte escapes", () => {
  expect(claudeRetainedJSONBytes("<>&\u2028\u2029")).toBe(32);
  expect(claudeRetainedJSONBytes({ text: "<".repeat(50000) })).toBe(300011);
  expect(claudeRetainedJSONBytes({ text: "ordinary 한글" })).toBe(new TextEncoder().encode(JSON.stringify({ text: "ordinary 한글" })).length);
});
