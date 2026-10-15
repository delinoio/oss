// SPDX-License-Identifier: Apache-2.0
// jsdom does not resolve selector specificity, so these guards parse the
// committed stylesheets and compare selector specificity directly.
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const directory = join(process.cwd(), "src");
const strip = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, "");
const read = (name: string) =>
  strip(readFileSync(join(directory, name), "utf8"));
type Specificity = [number, number, number];

function splitTopLevel(text: string, separator: string): string[] {
  const parts: string[] = [];
  let depth = 0,
    start = 0;
  for (let index = 0; index < text.length; index++) {
    const char = text[index];
    if (char === "(" || char === "[") depth++;
    else if (char === ")" || char === "]") depth--;
    else if (char === separator && depth === 0) {
      parts.push(text.slice(start, index));
      start = index + 1;
    }
  }
  parts.push(text.slice(start));
  return parts.map((part) => part.trim()).filter(Boolean);
}
function add(a: Specificity, b: Specificity): Specificity {
  return [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
}
function compare(a: Specificity, b: Specificity) {
  for (let index = 0; index < 3; index++)
    if (a[index] !== b[index]) return a[index] - b[index];
  return 0;
}
function list(arguments_: string): Specificity {
  return splitTopLevel(arguments_, ",")
    .map(specificity)
    .reduce((best, item) => (compare(item, best) > 0 ? item : best), [0, 0, 0]);
}
/** Selector specificity for the syntax used in the DeliDev stylesheets. */
function specificity(selector: string): Specificity {
  let result: Specificity = [0, 0, 0];
  let index = 0;
  const identifier = () => {
    const match = /^[-\w\\]+/.exec(selector.slice(index));
    index += match ? match[0].length : 1;
  };
  while (index < selector.length) {
    const char = selector[index];
    if (char === "#") {
      index++;
      identifier();
      result = add(result, [1, 0, 0]);
    } else if (char === ".") {
      index++;
      identifier();
      result = add(result, [0, 1, 0]);
    } else if (char === "[") {
      index = selector.indexOf("]", index) + 1;
      result = add(result, [0, 1, 0]);
    } else if (char === ":") {
      const pseudoElement = selector[index + 1] === ":";
      index += pseudoElement ? 2 : 1;
      const start = index;
      identifier();
      const name = selector.slice(start, index);
      let inner: string | undefined;
      if (selector[index] === "(") {
        let depth = 0,
          end = index;
        for (; end < selector.length; end++) {
          if (selector[end] === "(") depth++;
          if (selector[end] === ")" && --depth === 0) break;
        }
        inner = selector.slice(index + 1, end);
        index = end + 1;
      }
      if (pseudoElement) result = add(result, [0, 0, 1]);
      else if (name === "where") continue;
      else if (inner !== undefined && ["is", "not", "has"].includes(name))
        result = add(result, list(inner));
      else result = add(result, [0, 1, 0]);
    } else if (/[\w-]/.test(char)) {
      identifier();
      result = add(result, [0, 0, 1]);
    } else if (char === "(") {
      index = selector.indexOf(")", index) + 1;
    } else index++;
  }
  return result;
}
function rules(css: string) {
  return [...css.matchAll(/([^{}@]+)\{([^{}]*)\}/g)].flatMap((match) =>
    splitTopLevel(match[1], ",").map((selector) => ({
      selector,
      declarations: match[2],
    })),
  );
}
const dangerSelectors = (css: string) =>
  rules(css).filter(({ selector }) =>
    selector.includes('[data-action-tone="danger"]'),
  );

it("measures selector specificity with :is, :where and :not semantics", () => {
  expect(specificity("button")).toEqual([0, 0, 1]);
  expect(specificity(".a.b button:hover")).toEqual([0, 3, 1]);
  expect(specificity(":root:root button[data-x]")).toEqual([0, 3, 1]);
  expect(specificity("button:where(:not(.tab-item))")).toEqual([0, 0, 1]);
  expect(specificity("a:is(.x, #y)")).toEqual([1, 0, 1]);
  expect(specificity("button:not(:disabled)")).toEqual([0, 1, 1]);
});

it("keeps the desktop Danger rules above every competing button color, border and hover rule", () => {
  const actions = read("settings-action.css");
  const danger = dangerSelectors(actions);
  const base = danger.find(({ selector }) => !selector.includes(":hover"))!;
  const hover = danger.find(({ selector }) => selector.includes(":hover"))!;
  expect(base.declarations).toMatch(/background:\s*transparent/);
  expect(base.declarations).toMatch(/border:\s*1px solid var\(--danger-text\)/);
  expect(base.declarations).toMatch(/color:\s*var\(--danger-text\)/);
  expect(hover.declarations).toMatch(
    /background:\s*var\(--danger-background\)/,
  );
  expect(hover.selector).toContain(":not(:disabled)");
  const baseSpecificity = specificity(base.selector),
    hoverSpecificity = specificity(hover.selector);
  const toned = new Set<string>();
  // Generic button rules and the per-feature classes carried by toned controls.
  const generic = [
    "settings-action.css",
    "settings-presentation.css",
    "settings-task.css",
    "styles.css",
  ];
  const featureClasses =
    /\.(paired-device-revoke|integration-danger|backups-destructive|api-entry-delete|account-deletion-confirm|api-account-deletion-confirm|repository-delete|queue-icon|queue-compact-actions)\b/;
  for (const name of readdirSync(directory).filter((n) => n.endsWith(".css"))) {
    for (const { selector, declarations } of rules(read(name))) {
      if (selector.includes('[data-action-tone="danger"]')) continue;
      const subject =
        /^button(:[\w-]+)*$/.test(selector.trim()) ||
        (/(^|[^\w-])button(?![\w-])/.test(selector) &&
          /\.settings-content-column|\.settings-task/.test(selector)) ||
        /\.settings-action-/.test(selector);
      if (!(generic.includes(name) ? subject : featureClasses.test(selector)))
        continue;
      if (
        !/(^|[;\s])(color|background|background-color|border|border-color|border-width|border-style):/.test(
          declarations,
        )
      )
        continue;
      const limit = selector.includes(":hover")
        ? hoverSpecificity
        : baseSpecificity;
      expect(
        compare(limit, specificity(selector)),
        `${name}: ${selector.trim()}`,
      ).toBeGreaterThan(0);
      toned.add(name);
    }
  }
  // The audited competitors include the icon-only color rule and the generic
  // Settings border rule.
  expect(toned.has("settings-action.css")).toBe(true);
  expect(toned.has("settings-presentation.css")).toBe(true);
  expect(toned.has("styles.css")).toBe(true);
});

it("selects Danger only by the semantic marker, never by icon or label", () => {
  for (const name of readdirSync(directory).filter((n) => n.endsWith(".css"))) {
    for (const { selector } of dangerSelectors(read(name)))
      expect(selector, `${name}: ${selector}`).not.toMatch(
        /data-settings-action(?!-presentation)|aria-label|:contains|:has\(|svg/,
      );
  }
});

it("uses the existing semantic danger tokens for light, dark and system dark", () => {
  const themes = readFileSync(join(directory, "themes.css"), "utf8");
  const palettes = [
    ...themes.matchAll(/color-scheme: (?:light|dark);([^}]+)/g),
  ];
  expect(palettes).toHaveLength(3);
  const expected = [
    ["#fff3f4", "#b42332"],
    ["#392127", "#ffb6bf"],
    ["#392127", "#ffb6bf"],
  ];
  palettes.forEach((palette, index) => {
    expect(palette[1]).toContain(`--danger-background: ${expected[index][0]};`);
    expect(palette[1]).toContain(`--danger-text: ${expected[index][1]};`);
  });
});
