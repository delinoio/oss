// SPDX-License-Identifier: Apache-2.0
// jsdom does not evaluate container queries, so this guard scans every
// committed stylesheet and asserts that the API provider picker is a
// single-column list at every container width, including the page wrappers
// created by shared scroll continuation.
import { describe, expect, it } from "vitest";
import { cascade, rulesFor } from "./css-test-rules";

const SINGLE_COLUMN = "minmax(0, 1fr)";

describe("API provider picker stylesheet", () => {
  it("never declares more than one column for the list or its page wrappers", () => {
    const rules = rulesFor("account-provider-choices");
    expect(rules.length).toBeGreaterThan(0);
    for (const rule of rules) {
      for (const property of [
        "grid-template-columns",
        "columns",
        "column-count",
      ]) {
        const value = rule.declarations.get(property);
        if (value !== undefined && property === "grid-template-columns")
          expect(value).toBe(SINGLE_COLUMN);
        else expect(value).toBeUndefined();
      }
      expect(rule.declarations.get("display")).not.toBe("inline-grid");
      expect(rule.declarations.get("grid-auto-flow")).toBeUndefined();
    }
  });

  it("keeps list-level rules unconditional so no viewport or container query changes the layout", () => {
    for (const rule of rulesFor("account-provider-")) {
      expect(rule.context).toEqual([]);
    }
  });

  it("caps the list at 720px with a consistent 10px gap", () => {
    const merged = cascade(
      (selector) => selector === ".account-provider-choices",
    );
    expect(merged.get("display")).toBe("grid");
    expect(merged.get("grid-template-columns")).toBe(SINGLE_COLUMN);
    expect(merged.get("max-width")).toBe("720px");
    expect(merged.get("gap")).toBe("10px");
    expect(merged.get("width")).toBe("100%");
  });

  it("stacks every scroll continuation page wrapper with the same single column and gap", () => {
    const merged = cascade((selector) =>
      /^\.account-provider-choices\s*>\s*\[data-payload-page\]$/.test(selector),
    );
    expect(merged.size).toBeGreaterThan(0);
    expect(merged.get("display")).toBe("grid");
    expect(merged.get("grid-template-columns")).toBe(SINGLE_COLUMN);
    expect(merged.get("gap")).toBe("10px");
    expect(merged.get("min-width")).toBe("0");
  });

  it("gives each row the full width, a 64px hit target, 12px padding and an 8px radius", () => {
    const merged = cascade(
      (selector) => selector === ".account-provider-action",
    );
    expect(merged.get("width")).toBe("100%");
    expect(merged.get("min-height")).toBe("64px");
    expect(merged.get("padding")).toBe("12px");
    expect(merged.get("border-radius")).toBe("8px");
    expect(merged.get("border")).toBe("1px solid var(--border)");
    expect(merged.get("display")).toBe("flex");
    expect(merged.get("justify-content")).toBe("space-between");
  });

  it("wraps long names without letting them overlap the fixed-size chevron", () => {
    const label = rulesFor(".account-provider-action > span:first-child")[0];
    expect(label.declarations.get("min-width")).toBe("0");
    expect(label.declarations.get("overflow-wrap")).toBe("anywhere");
    const chevron = rulesFor(".account-provider-chevron")[0];
    expect(chevron.declarations.get("flex")).toBe("0 0 auto");
  });

  it("does not override the single-column rule from other stylesheets", () => {
    const files = new Set(
      rulesFor("account-provider-choices").map((rule) => rule.file),
    );
    expect([...files]).toEqual(["styles.css"]);
  });
});
