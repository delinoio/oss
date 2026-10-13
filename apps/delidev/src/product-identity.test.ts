// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import {
  ProductIdentities,
  PresentationKind,
  productDiagnostic,
  productMetadata,
  productMetadataJSON,
  generatedIdentityFields,
  configurationPreviewPaths,
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
  it("keeps duplicate labels numbered after another identity is renamed", () => {
    const { registry, label } = fixture();
    registry.observe(PresentationKind.Project, first, "Shared");
    registry.observe(PresentationKind.Project, second, "Shared");
    expect(label(PresentationKind.Project, second)).toBe("Shared · 2");
    expect(label(PresentationKind.Project, first, "Renamed")).toBe("Renamed");
    expect(label(PresentationKind.Project, second)).toBe("Shared · 2");
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
  describe("configuration preview path projection", () => {
    const external = "9007199254740993";
    const plan = (change: Record<string, unknown>) =>
      JSON.stringify({
        plan: { version: 4, changes: [change], machines: [{ id: third }] },
        token: "t",
      });
    it("keeps reviewer selector IDs byte-for-byte while projecting generated identities", () => {
      const { label } = fixture();
      const raw = `{"plan":{"version":4,"changes":[{"source_id":"${first}","id":"${second}","kind":"server-preferences","after":{"remediation":{"agent_id":"${third}","reviewer_selectors":[{"kind":"user","id":"${external}","node_id":"MDQ6VXNlcjE="},{"kind":"app","id":"${first}"}]},"count":9007199254740993}}],"machines":[{"id":"${third}"}]}}`;
      const projected = productMetadataJSON(
        raw,
        generatedIdentityFields,
        label,
        configurationPreviewPaths,
      );
      expect(projected).toContain(`"id":"${external}"`);
      expect(projected).toContain(`{"kind":"app","id":"${first}"}`);
      expect(projected).toContain('"count":9007199254740993');
      expect(projected).toContain('"id":"resource unavailable · 2"');
      expect(projected).toContain('"agent_id":"agent unavailable · 1"');
      expect(projected).toContain(
        '"machines":[{"id":"resource unavailable · 3"}]',
      );
      expect(projected).not.toContain(second);
      expect(raw).toContain(`"id":"${second}"`);
    });
    it("projects plan change source IDs without adding a global source_id rule", () => {
      const { label } = fixture();
      const raw = plan({
        source_id: first,
        id: second,
        after: { native: { source_id: third }, source_id: third },
        before: { metadata: { source_id: second } },
      });
      const projected = productMetadataJSON(
        raw,
        generatedIdentityFields,
        label,
        configurationPreviewPaths,
      );
      expect(projected).toContain('"source_id":"resource unavailable · 1"');
      expect(projected).not.toContain(`"source_id":"${first}"`);
      // Other source_id uses are external, native or user-owned.
      expect(projected).toContain(`"native":{"source_id":"${third}"}`);
      expect(projected).toContain(
        `"after":{"native":{"source_id":"${third}"},"source_id":"${third}"}`,
      );
      expect(projected).toContain(`"metadata":{"source_id":"${second}"}`);
      expect(generatedIdentityFields).not.toHaveProperty("source_id");
      // Generic callers never project source_id.
      expect(
        productMetadataJSON(raw, generatedIdentityFields, label),
      ).toContain(`"source_id":"${first}"`);
    });
    it("projects Agent templates and Project agents.ids and accounts.ids by path", () => {
      const { label } = fixture();
      const raw = plan({
        source_id: first,
        id: first,
        kind: "project",
        before: {
          templates: [first],
          agents: { configured: true, ids: [second] },
        },
        after: {
          templates: [first, second],
          agents: { configured: true, ids: [second] },
          accounts: { configured: true, ids: [third] },
          other: { ids: [third] },
          ids: [third],
          native_options: { templates: [first], accounts: { ids: [third] } },
          name: { templates: [first] },
        },
      });
      const projected = productMetadataJSON(
        raw,
        generatedIdentityFields,
        label,
        configurationPreviewPaths,
      );
      expect(projected).toContain('"templates":["template unavailable · 1"]');
      expect(projected).toContain(
        '"templates":["template unavailable · 1","template unavailable · 2"]',
      );
      expect(projected).toContain(
        '"agents":{"configured":true,"ids":["agent unavailable · 1"]}',
      );
      expect(projected).toContain(
        '"accounts":{"configured":true,"ids":["account unavailable · 1"]}',
      );
      expect(projected).toContain(`"other":{"ids":["${third}"]}`);
      expect(projected).toContain(`"ids":["${third}"],"native_options"`);
      expect(projected).toContain(
        `"native_options":{"templates":["${first}"],"accounts":{"ids":["${third}"]}}`,
      );
      expect(projected).toContain(`"name":{"templates":["${first}"]}`);
    });
    it("does not apply configuration paths outside plan changes and keeps malformed JSON stable", () => {
      const { label } = fixture();
      const outside = `{"templates":["${first}"],"agents":{"ids":["${second}"]},"source_id":"${third}"}`;
      expect(
        productMetadataJSON(
          outside,
          generatedIdentityFields,
          label,
          configurationPreviewPaths,
        ),
      ).toBe(outside);
      const malformed = `{"plan":{"changes":[{"source_id":"${first}",`;
      expect(
        productMetadataJSON(
          malformed,
          generatedIdentityFields,
          label,
          configurationPreviewPaths,
        ),
      ).toBe(`{"plan":{"changes":[{"source_id":"resource unavailable · 1",`);
      expect(
        productMetadataJSON(
          "not json",
          generatedIdentityFields,
          label,
          configurationPreviewPaths,
        ),
      ).toBe("not json");
    });
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
