// SPDX-License-Identifier: Apache-2.0
// jsdom does not evaluate container queries, so this guard scans every
// committed stylesheet and asserts that the API provider picker is a
// single-column list at every container width, including the page wrappers
// created by shared scroll continuation.
import { readdirSync, readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

interface CssRule {
  file: string;
  context: string[];
  selector: string;
  declarations: Map<string, string>;
}

function parseRules(file: string, css: string): CssRule[] {
  const rules: CssRule[] = [];
  const text = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const walk = (body: string, context: string[]) => {
    let i = 0;
    while (i < body.length) {
      const open = body.indexOf("{", i);
      if (open < 0) break;
      const prelude = body.slice(i, open).trim();
      let depth = 1;
      let j = open + 1;
      while (j < body.length && depth > 0) {
        if (body[j] === "{") depth += 1;
        else if (body[j] === "}") depth -= 1;
        j += 1;
      }
      const inner = body.slice(open + 1, j - 1);
      if (prelude.startsWith("@")) {
        walk(inner, [...context, prelude]);
      } else {
        const declarations = new Map<string, string>();
        for (const declaration of inner.split(";")) {
          const colon = declaration.indexOf(":");
          if (colon > 0)
            declarations.set(
              declaration.slice(0, colon).trim(),
              declaration.slice(colon + 1).trim(),
            );
        }
        rules.push({ file, context, selector: prelude, declarations });
      }
      i = j;
    }
  };
  walk(text, []);
  return rules;
}

const RULES = readdirSync("src")
  .filter((name) => name.endsWith(".css"))
  .flatMap((name) => parseRules(name, readFileSync(`src/${name}`, "utf8")));

const rulesFor = (needle: string) =>
  RULES.filter((rule) => rule.selector.includes(needle));

// Merges the declarations of every rule that has a selector in its selector
// list for which the matcher returns true.
function cascade(match: (selector: string) => boolean): Map<string, string> {
  const merged = new Map<string, string>();
  for (const rule of RULES)
    if (rule.selector.split(",").some((selector) => match(selector.trim())))
      for (const [key, value] of rule.declarations) merged.set(key, value);
  return merged;
}
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
