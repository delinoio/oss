// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { ProductReferenceLabels, ProductReferenceKind as Kind, productDiagnosticText } from "./product-references.js";
const a = "01900000-0000-7000-8000-000000000001", b = "01900000-0000-7000-8000-000000000002";
it("retains view-owned labels across refresh/reorder and distinguishes equal backup metadata", () => {
  const labels = new ProductReferenceLabels();
  expect(labels.label(a, Kind.Backup)).toBe("Backup 1");
  expect(labels.label(b, Kind.Backup)).toBe("Backup 2");
  expect(labels.label(b, Kind.Backup, "ko")).toBe("백업 2");
  expect(labels.label(a, Kind.Backup)).toBe("Backup 1");
  expect(new ProductReferenceLabels().label(b, Kind.Backup)).toBe("Backup 1");
});
it("keeps exact user names and unavailable observations separate from reference identities", () => {
  const labels = new ProductReferenceLabels();
  expect(labels.label(a, Kind.Session, "en", a)).toBe(a);
  expect(labels.label(undefined, Kind.Account)).toBe("Account unavailable");
  expect(labels.label("", Kind.Account, "ko")).toBe("계정 정보 없음");
  expect(labels.label(a, Kind.Account)).toBe("Account 1");
});
it("removes UUIDs only from explicitly classified generated diagnostic prose without mutating evidence", () => {
  const evidence = { message: `Cannot inspect ${a}; retry the original operation.`, id: a };
  expect(productDiagnosticText(evidence.message)).toBe("Cannot inspect […]; retry the original operation.");
  expect(evidence.id).toBe(a);
  expect(evidence.message).toContain(a);
});
it("projects typed configuration references without changing native names, arbitrary options or integer tokens", async () => {
  const { productConfigurationReview } = await import("./product-configuration-review.js");
  const raw = `{"token":"${a}","plan":{"version":4,"machines":[{"id":"${a}","name":"${a}"}],"changes":[{"id":"${a}","source_id":"${b}","expected_revision":18446744073709551615,"after":{"name":"${a}","provider_id":"${b}","options":{"id":"${a}"},"native_id":"${a}","repositories":["${b}"]}}]}}`;
  const shown = productConfigurationReview(raw, new ProductReferenceLabels());
  expect(shown).toContain("18446744073709551615");
  const value = JSON.parse(shown);
  expect(value.token).toBe("Internal inspection proof");
  expect(value.plan.changes[0].id).not.toBe(a);
  expect(value.plan.changes[0].after.provider_id).not.toBe(b);
  expect(value.plan.changes[0].after.repositories[0]).not.toBe(b);
  expect(value.plan.machines[0].name).toBe(a);
  expect(value.plan.changes[0].after.name).toBe(a);
  expect(value.plan.changes[0].after.options.id).toBe(a);
  expect(value.plan.changes[0].after.native_id).toBe(a);
  expect(JSON.parse(raw).plan.changes[0].id).toBe(a);
});
