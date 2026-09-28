import { expect, it } from "vitest";
import { validPRRules } from "./github-rules";

const item = { base_ref: "release/next", base_sha: "a".repeat(40), head_sha: "b".repeat(40) };
function fixture() {
  return { ...item, digest: "c".repeat(64), rules: [{ type: "required_status_checks", ruleset_id: "9007199254740993", source_kind: "organization", native_source_kind: "Organization", source: "fixture-owner", digest: "d".repeat(64), required_checks: { strict: true, checks: [{ context: "CI Result", integration_id: "15368" }] } }] };
}
it("validates exact PR/ruleset scope and retains absent and explicit zero App IDs", () => {
  expect(validPRRules(fixture(), item)).toBe(true);
  const absent = fixture(); delete (absent.rules[0].required_checks.checks[0] as { integration_id?: string }).integration_id;
  expect(validPRRules(absent, item)).toBe(true);
  const zero = fixture(); zero.rules[0].required_checks.checks[0].integration_id = "0";
  expect(validPRRules(zero, item)).toBe(true);
  expect(validPRRules({ ...fixture(), base_ref: "main" }, item)).toBe(false);
  expect(validPRRules({ ...fixture(), head_sha: "a".repeat(40) }, item)).toBe(false);
});
it("rejects partial required rules, duplicates and conflicting provenance", () => {
  const missing = fixture(); delete (missing.rules[0] as { required_checks?: unknown }).required_checks;
  expect(validPRRules(missing, item)).toBe(false);
  const duplicate = fixture(); duplicate.rules.push(duplicate.rules[0]);
  expect(validPRRules(duplicate, item)).toBe(false);
  const conflict = fixture();
  const second = { ...conflict.rules[0], type: "deletion", source: "foreign" };
  delete (second as { required_checks?: unknown }).required_checks;
  conflict.rules.push(second);
  expect(validPRRules(conflict, item)).toBe(false);
  const invalidID = fixture(); invalidID.rules[0].required_checks.checks[0].integration_id = "-1";
  expect(validPRRules(invalidID, item)).toBe(false);
});
