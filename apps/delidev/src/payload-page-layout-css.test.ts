// SPDX-License-Identifier: Apache-2.0
// Every scroll payload window wrapper is either resident (full payload, natural
// height) or a measured placeholder (previous page height as inline min-height
// around a single Restore control). jsdom does not lay out boxes, so this guard
// scans the committed stylesheets and pins the layout rule of each wrapper
// stylesheet against the state x layout table in the desktop contract.
import { describe, expect, it } from "vitest";
import { RULES, type CssRule } from "./css-test-rules";

enum WrapperLayout {
  Grid = "grid",
  FlexWrap = "flex-wrap",
  FlexColumn = "flex-column",
}

interface WrapperContract {
  name: string;
  file: string;
  layout: WrapperLayout;
  resident: string;
  /** Undefined when the layout cannot stretch a lone item to the min-height. */
  placeholder?: string;
  placeholderAlignment?: { content: string; items: string };
}

const PLACEHOLDER = '[data-payload-state="placeholder"]';
const CONTRACTS: WrapperContract[] = [
  {
    name: "provider picker",
    file: "styles.css",
    layout: WrapperLayout.Grid,
    resident: ".account-provider-choices > [data-payload-page]",
    placeholder: `.account-provider-choices > [data-payload-page]${PLACEHOLDER}`,
    placeholderAlignment: { content: "start", items: "start" },
  },
  {
    name: "repository list",
    file: "repository-list.css",
    layout: WrapperLayout.Grid,
    resident:
      ".settings-content.settings-repositories .repository-list > [data-payload-page]",
    placeholder: `.settings-content.settings-repositories .repository-list > [data-payload-page]${PLACEHOLDER}`,
    placeholderAlignment: { content: "start", items: "start" },
  },
  {
    name: "terminal tabs",
    file: "terminal-dock.css",
    layout: WrapperLayout.FlexWrap,
    resident: ".terminal-tabs [data-payload-page]",
    placeholder: `.terminal-tabs [data-payload-page]${PLACEHOLDER}`,
    placeholderAlignment: { content: "flex-start", items: "flex-start" },
  },
  {
    name: "session transcript",
    file: "session.css",
    layout: WrapperLayout.FlexColumn,
    resident: ".session-workspace .transcript > [data-payload-page]",
  },
];

const selectors = (rule: CssRule) =>
  rule.selector.split(",").map((selector) => selector.trim());
const declaring = (file: string, selector: string) =>
  RULES.filter(
    (rule) => rule.file === file && selectors(rule).includes(selector),
  );
const merged = (file: string, selector: string) => {
  const values = new Map<string, string>();
  for (const rule of declaring(file, selector))
    for (const [key, value] of rule.declarations) values.set(key, value);
  return values;
};

describe("scroll payload window wrapper stylesheets", () => {
  it("lists every stylesheet rule that targets a payload page wrapper", () => {
    const known = new Set(
      CONTRACTS.flatMap((contract) =>
        [contract.resident, contract.placeholder].filter(Boolean),
      ),
    );
    const unknown = RULES.flatMap((rule) =>
      selectors(rule)
        .filter((selector) => selector.includes("[data-payload-page]"))
        .filter((selector) => !known.has(selector))
        .map((selector) => `${rule.file}: ${selector}`),
    );
    // Spacing-only overrides of a listed resident rule are allowed; any other
    // new wrapper layout must join the contract table and this test.
    expect(
      unknown.filter(
        (entry) =>
          !/themes\.css: .*\.transcript > \[data-payload-page\]$/.test(entry),
      ),
    ).toEqual([]);
  });

  describe.each(CONTRACTS)("$name wrapper", (contract) => {
    const resident = () => merged(contract.file, contract.resident);

    it("resident state uses its documented layout and natural height", () => {
      const rule = resident();
      expect(rule.size).toBeGreaterThan(0);
      expect(rule.get("min-height")).toBeUndefined();
      expect(rule.get("height")).toBeUndefined();
      switch (contract.layout) {
        case WrapperLayout.Grid:
          expect(rule.get("display")).toBe("grid");
          expect(rule.get("grid-template-columns")).toBe("minmax(0, 1fr)");
          break;
        case WrapperLayout.FlexWrap:
          expect(rule.get("display")).toBe("flex");
          expect(rule.get("flex-wrap")).toBe("wrap");
          break;
        case WrapperLayout.FlexColumn:
          expect(rule.get("display")).toBe("flex");
          expect(rule.get("flex-direction")).toBe("column");
          expect(rule.get("flex-wrap")).toBeUndefined();
          break;
      }
    });

    it("placeholder state keeps its lone Restore control at content height", () => {
      if (!contract.placeholder) {
        // A nowrap column container never stretches an item along its main
        // axis, so the measured min-height cannot resize Restore.
        expect(contract.layout).toBe(WrapperLayout.FlexColumn);
        expect(resident().get("flex-wrap")).toBeUndefined();
        expect(resident().get("align-content")).toBeUndefined();
        return;
      }
      const rule = merged(contract.file, contract.placeholder);
      expect(rule.get("align-content")).toBe(
        contract.placeholderAlignment?.content,
      );
      expect(rule.get("align-items")).toBe(
        contract.placeholderAlignment?.items,
      );
      expect(rule.get("grid-auto-rows")).toBeUndefined();
      expect(rule.get("height")).toBeUndefined();
      expect(rule.get("min-height")).toBeUndefined();
    });

    it("applies placeholder alignment unconditionally", () => {
      for (const selector of [contract.resident, contract.placeholder])
        for (const rule of declaring(contract.file, selector ?? ""))
          expect(rule.context).toEqual([]);
    });
  });
});
