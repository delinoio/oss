// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { configuredEffort } from "../src/configured-effort.ts";
test("preserves exact saved root effort strings as inert text", () => {
 for (const effort of ["high", "none", "max", "xhigh", " Future-Effort ", "   "]) {
  assert.equal(configuredEffort(effort, "Native default", "Unavailable"), effort);
 }
});
test("distinguishes native defaults from malformed values", () => {
 for (const [defaultLabel, unavailable] of [["Native default", "Unavailable"], ["네이티브 기본값", "사용 불가"]]) {
  for (const effort of [undefined, ""]) assert.equal(configuredEffort(effort, defaultLabel, unavailable), defaultLabel);
  for (const effort of [null, 3, {}, [], false]) assert.equal(configuredEffort(effort, defaultLabel, unavailable), unavailable);
 }
});
