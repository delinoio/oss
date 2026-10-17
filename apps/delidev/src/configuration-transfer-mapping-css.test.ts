// SPDX-License-Identifier: Apache-2.0
// jsdom does not evaluate selector matching across a wrapper or width caps, so
// this guard reads the committed stylesheets. Wrapping the configuration
// mapping panel in a form must not narrow it or drop the separators that the
// adjacent-sibling `.transfer-panel + .transfer-panel` rule drew around it.
import { describe, expect, it } from "vitest";
import { rulesFor } from "./css-test-rules";

describe("configuration transfer mapping form stylesheet", () => {
  it("does not apply the generic 720px form cap to the mapping form", () => {
    const rules = rulesFor("form.transfer-mapping-form").filter((rule) =>
      rule.declarations.has("max-width"),
    );
    expect(rules.length).toBeGreaterThan(0);
    for (const rule of rules) {
      expect(rule.context).toEqual([]);
      expect(rule.declarations.get("max-width")).toBe("none");
      expect(rule.selector).toContain(".settings-content-column");
    }
  });

  it("leaves the 720px panel cap on the panel itself so its width is unchanged", () => {
    const panel = rulesFor(".settings-content-column .transfer-panel").filter(
      (rule) => rule.selector.endsWith(".transfer-panel"),
    );
    expect(
      panel.some((rule) => rule.declarations.get("max-width") === "720px"),
    ).toBe(true);
  });

  it("keeps the panel separators across the form boundary", () => {
    const separators = rulesFor(".transfer-panel + .transfer-panel").filter(
      (rule) =>
        rule.declarations.get("border-top") === "1px solid var(--border)",
    );
    expect(separators.length).toBe(1);
    const selector = separators[0]!.selector;
    expect(selector).toContain(
      ".transfer-panel + .transfer-mapping-form > .transfer-panel",
    );
    expect(selector).toContain(".transfer-mapping-form + .transfer-panel");
    expect(separators[0]!.declarations.get("margin-top")).toBe("24px");
    expect(separators[0]!.declarations.get("padding-top")).toBe("24px");
  });
});
