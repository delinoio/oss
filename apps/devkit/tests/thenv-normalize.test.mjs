import test from "node:test";
import assert from "node:assert/strict";
import {
  normalizeRoleValue,
  parseAuditEventLabel,
  roleCodeFromValue,
  roleLabelFromUnknown,
} from "../src/server/thenv-normalize.mjs";

test("normalizeRoleValue falls back to reader", () => {
  assert.equal(normalizeRoleValue("ADMIN"), "admin");
  assert.equal(normalizeRoleValue("unknown"), "reader");
  assert.equal(roleCodeFromValue("writer"), 2);
  assert.equal(roleLabelFromUnknown(3), "admin");
});

test("parseAuditEventLabel supports numeric and string inputs", () => {
  assert.equal(parseAuditEventLabel("1"), "push");
  assert.equal(parseAuditEventLabel("policy-update"), "policy-update");
  assert.equal(parseAuditEventLabel("noop"), "unspecified");
});
