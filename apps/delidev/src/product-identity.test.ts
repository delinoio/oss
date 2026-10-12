// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import {
  ProductIdentities,
  PresentationKind,
  productDiagnostic,
  productMetadata,
  productMetadataJSON,
  generatedIdentityFields,
  type IdentityLabel,
} from "./product-identity";
const first = "0195c9c0-7b13-7000-8000-000000000001";
const second = "0195c9c0-7b13-7000-8000-000000000002";
const third = "0195c9c0-7b13-7000-8000-000000000003";
function fixture() {
  const registry = new ProductIdentities();
  const label: IdentityLabel = (kind, id, name) =>
    registry.label(
      kind,
      id,
      `${kind} unavailable`,
      (value, number) => `${value} · ${number}`,
      name,
    );
  return { registry, label };
}
describe("product identity presentation", () => {
  it("retains original-ID numbers through reorder, disappearance, later pages and locale changes", () => {
    const { registry, label } = fixture();
    expect(label(PresentationKind.Backup, first)).toBe(
      "backup unavailable · 1",
    );
    expect(label(PresentationKind.Backup, second)).toBe(
      "backup unavailable · 2",
    );
    expect(
      [second, first].map((id) => label(PresentationKind.Backup, id)),
    ).toEqual(["backup unavailable · 2", "backup unavailable · 1"]);
    expect(label(PresentationKind.Backup, third)).toBe(
      "backup unavailable · 3",
    );
    expect(
      registry.label(
        PresentationKind.Backup,
        first,
        "백업",
        (name, n) => `${name} · ${n}`,
      ),
    ).toBe("백업 · 1");
    expect(label(PresentationKind.Request, first)).toBe(
      "request unavailable · 1",
    );
    expect(fixture().label(PresentationKind.Backup, second)).toBe(
      "backup unavailable · 1",
    );
  });
  it("disambiguates duplicate observed names without inventing UUID fragments", () => {
    const { registry, label } = fixture();
    registry.observe(PresentationKind.Repository, first, "Same");
    registry.observe(PresentationKind.Repository, second, "Same");
    expect(
      [second, first].map((id) => label(PresentationKind.Repository, id)),
    ).toEqual(["Same · 2", "Same · 1"]);
    expect(label(PresentationKind.Repository, first, "")).toBe("Same · 1");
    expect(label(PresentationKind.Repository, third)).toBe(
      "repository unavailable · 3",
    );
  });
  it("keeps UUID-looking and non-ASCII user names and native provider slugs exactly", () => {
    const { label } = fixture();
    expect(label(PresentationKind.Project, first, second)).toBe(second);
    expect(label(PresentationKind.Account, first, "账户 이름  ")).toBe(
      "账户 이름  ",
    );
    expect(label(PresentationKind.Provider, "anthropic")).toBe("anthropic");
    expect(label(PresentationKind.Provider, first)).not.toContain(first);
    expect(label(PresentationKind.Project, undefined)).toBe(
      "project unavailable",
    );
  });
  it("projects only declared generated metadata and keeps original evidence and source subtrees", () => {
    const { label } = fixture();
    const native = { id: first, content: second };
    const original = {
      session_id: first,
      repositories: [second],
      name: third,
      native,
      metadata: { id: first },
      prompt: first,
      count: 9007199254740993n,
    };
    const projected = productMetadata(
      original,
      generatedIdentityFields,
      label,
    ) as typeof original;
    expect(projected.session_id).toBe("session unavailable · 1");
    expect(projected.repositories).toEqual(["repository unavailable · 1"]);
    expect(projected.native).toBe(native);
    expect(projected.metadata).toBe(original.metadata);
    expect(projected.prompt).toBe(first);
    expect(projected.name).toBe(third);
    expect(projected.count).toBe(9007199254740993n);
    expect(original.session_id).toBe(first);
    expect(original.repositories).toEqual([second]);
  });
  it("preserves JSON numeric tokens, spacing and native/user payload bytes during preview projection", () => {
    const { label } = fixture();
    const raw = `{ "id" : "${first}", "count":9007199254740993, "repositories":["${second}"], "native_request":{"id":"${first}"}, "prompt":{"id":"${second}"}, "name":"${third}", "metadata":{"id":"${third}"}}`;
    const projected = productMetadataJSON(raw, generatedIdentityFields, label);
    expect(projected).toContain('"id" : "resource unavailable · 1"');
    expect(projected).toContain('"count":9007199254740993');
    expect(projected).toContain(
      '"repositories":["repository unavailable · 1"]',
    );
    expect(projected).toContain(`"native_request":{"id":"${first}"}`);
    expect(projected).toContain(`"prompt":{"id":"${second}"}`);
    expect(projected).toContain(`"name":"${third}"`);
    expect(projected).toContain(`"metadata":{"id":"${third}"}`);
    expect(raw).toContain(`"id" : "${first}"`);
  });
  it("omits UUIDs from generated diagnostic prose while retaining code and retry guidance", () => {
    expect(
      productDiagnostic(
        `stale_revision: ${first}; retry the original operation`,
        "Internal reference omitted",
      ),
    ).toBe(
      "stale_revision: Internal reference omitted; retry the original operation",
    );
    expect(productDiagnostic(undefined, "omitted")).toBe("");
  });
});
