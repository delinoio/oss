// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { validateSessionName } from "../src/session-name.ts";

const fallback = "New session";
for (const [title, valid] of [
  ["가".repeat(100), false],
  ["a".repeat(256), true],
  ["a".repeat(257), false],
  ["가".repeat(84) + "😀", true],
  ["가".repeat(84) + "😀a", false],
  ["a".repeat(249) + "가😀", true],
  ["a".repeat(250) + "가😀", false],
]) {
  test(`validates ${new TextEncoder().encode(title).length} UTF-8 bytes`, () => {
    assert.deepEqual(validateSessionName(title, fallback), { name: title, valid });
  });
}
test("validates the trimmed payload without truncating the draft", () => {
  const name = "가".repeat(84) + "😀";
  assert.deepEqual(validateSessionName(`  ${name}  `, fallback), { name, valid: true });
});
test("validates the exact localized empty-title fallback", () => {
  for (const name of [fallback, "새 세션", "가".repeat(100)]) {
    assert.deepEqual(validateSessionName(" \t\n ", name), {
      name,
      valid: new TextEncoder().encode(name).length <= 256,
    });
  }
});
