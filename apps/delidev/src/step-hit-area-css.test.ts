// SPDX-License-Identifier: Apache-2.0
// jsdom resolves competing declarations by source order only, so it cannot say
// which gap or margin a browser applies. This guard parses the committed
// stylesheets, evaluates the real cascade (media conditions, specificity,
// source order) for the Agent Worker step strip at every breakpoint, and proves
// that the hit areas of adjacent step buttons never overlap.
import { readdirSync, readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";

const WIZARD_CSS = readFileSync("src/agent-worker-wizard.css", "utf8");
const PRESENTATION_CSS = readFileSync("src/wizard-presentation.css", "utf8");

// Minimum hit height of a step button, from the Worker wizard step indicator
// contract.
const MIN_HIT_HEIGHT_PX = 36;

interface CssRule {
  selectors: string[];
  declarations: Map<string, { value: string; important: boolean }>;
  order: number;
}

function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

function matchingBrace(css: string, open: number): number {
  let depth = 1;
  let i = open + 1;
  while (i < css.length && depth > 0) {
    if (css[i] === "{") depth += 1;
    else if (css[i] === "}") depth -= 1;
    i += 1;
  }
  return i;
}

// Returns whether an at-rule query applies at the viewport width. Only width
// conditions are evaluated; any other feature (height, color scheme, motion)
// is reported as unknown so the caller can fail loudly or skip knowingly.
function mediaApplies(query: string, width: number): boolean | null {
  const conditions = [...query.matchAll(/\(([^)]*)\)/g)].map((m) => m[1]!);
  if (conditions.length === 0) return null;
  for (const condition of conditions) {
    const max = /^\s*max-width:\s*(\d+)px\s*$/.exec(condition);
    const min = /^\s*min-width:\s*(\d+)px\s*$/.exec(condition);
    if (max !== null) {
      if (width > Number(max[1])) return false;
    } else if (min !== null) {
      if (width < Number(min[1])) return false;
    } else {
      return null;
    }
  }
  return true;
}

interface ParsedSheet {
  rules: CssRule[];
  // Selector text of every rule that sits inside an at-rule the evaluator does
  // not model (container queries, height or preference media).
  unmodeled: string[];
}

function parseSheet(source: string, width: number): ParsedSheet {
  const css = stripComments(source);
  const rules: CssRule[] = [];
  const unmodeled: string[] = [];
  let order = 0;

  const walk = (text: string, modeled: boolean) => {
    let i = 0;
    while (i < text.length) {
      const open = text.indexOf("{", i);
      if (open < 0) break;
      let head = text.slice(i, open).trim();
      // Skip statements such as @import that precede the next rule.
      const statementEnd = head.lastIndexOf(";");
      if (statementEnd >= 0) head = head.slice(statementEnd + 1).trim();
      const close = matchingBrace(text, open);
      const body = text.slice(open + 1, close - 1);
      if (head.startsWith("@")) {
        if (head.startsWith("@media")) {
          const applies = mediaApplies(head, width);
          if (applies === true) walk(body, modeled);
          else if (applies === null) walk(body, false);
        } else {
          walk(body, false);
        }
      } else if (!modeled) {
        unmodeled.push(head);
      } else {
        const declarations = new Map<
          string,
          { value: string; important: boolean }
        >();
        for (const part of body.split(";")) {
          const colon = part.indexOf(":");
          if (colon < 0) continue;
          const name = part.slice(0, colon).trim().toLowerCase();
          let value = part.slice(colon + 1).trim();
          const important = /!important\s*$/.test(value);
          if (important) value = value.replace(/!important\s*$/, "").trim();
          for (const [longhand, longValue] of expandShorthand(name, value)) {
            declarations.set(longhand, { value: longValue, important });
          }
        }
        rules.push({
          selectors: splitTopLevel(head),
          declarations,
          order,
        });
        order += 1;
      }
      i = close;
    }
  };
  walk(css, true);
  return { rules, unmodeled };
}

function splitTopLevel(list: string): string[] {
  const parts: string[] = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < list.length; i += 1) {
    const ch = list[i];
    if (ch === "(" || ch === "[") depth += 1;
    else if (ch === ")" || ch === "]") depth -= 1;
    else if (ch === "," && depth === 0) {
      parts.push(list.slice(start, i).trim());
      start = i + 1;
    }
  }
  parts.push(list.slice(start).trim());
  return parts.filter((part) => part.length > 0);
}

function splitValues(value: string): string[] {
  return value.split(/\s+/).filter((part) => part.length > 0);
}

// Expands the shorthands the guard reads into physical longhands (left to
// right layout, which is the only direction the app ships).
function expandShorthand(name: string, value: string): [string, string][] {
  const box = (prefix: string): [string, string][] | null => {
    const v = splitValues(value);
    if (v.length < 1 || v.length > 4) return null;
    const top = v[0]!;
    const right = v[1] ?? top;
    const bottom = v[2] ?? top;
    const left = v[3] ?? right;
    return [
      [`${prefix}-top`, top],
      [`${prefix}-right`, right],
      [`${prefix}-bottom`, bottom],
      [`${prefix}-left`, left],
    ];
  };
  const pair = (first: string, second: string): [string, string][] => {
    const v = splitValues(value);
    return [
      [first, v[0] ?? value],
      [second, v[1] ?? v[0] ?? value],
    ];
  };
  switch (name) {
    case "margin":
    case "padding":
      return box(name) ?? [[name, value]];
    case "margin-inline":
    case "padding-inline": {
      const base = name.replace("-inline", "");
      return pair(`${base}-left`, `${base}-right`);
    }
    case "margin-block":
    case "padding-block": {
      const base = name.replace("-block", "");
      return pair(`${base}-top`, `${base}-bottom`);
    }
    case "margin-inline-start":
    case "padding-inline-start":
      return [[name.replace("-inline-start", "-left"), value]];
    case "margin-inline-end":
    case "padding-inline-end":
      return [[name.replace("-inline-end", "-right"), value]];
    case "margin-block-start":
    case "padding-block-start":
      return [[name.replace("-block-start", "-top"), value]];
    case "margin-block-end":
    case "padding-block-end":
      return [[name.replace("-block-end", "-bottom"), value]];
    case "gap":
      return pair("row-gap", "column-gap");
    default:
      return [[name, value]];
  }
}

type Specificity = [number, number, number];

function addSpecificity(a: Specificity, b: Specificity): Specificity {
  return [a[0] + b[0], a[1] + b[1], a[2] + b[2]];
}

function compareSpecificity(a: Specificity, b: Specificity): number {
  for (let i = 0; i < 3; i += 1) {
    if (a[i] !== b[i]) return a[i]! - b[i]!;
  }
  return 0;
}

function specificity(selector: string): Specificity {
  let rest = selector;
  let total: Specificity = [0, 0, 0];
  // Functional pseudo-classes: :where() counts as zero, :is()/:not() as their
  // most specific argument.
  for (;;) {
    const match = /:(where|is|not|matches)\(/.exec(rest);
    if (match === null) break;
    const start = match.index + match[0].length;
    let depth = 1;
    let end = start;
    while (end < rest.length && depth > 0) {
      if (rest[end] === "(") depth += 1;
      else if (rest[end] === ")") depth -= 1;
      end += 1;
    }
    const args = rest.slice(start, end - 1);
    if (match[1] !== "where") {
      let best: Specificity = [0, 0, 0];
      for (const arg of splitTopLevel(args)) {
        const candidate = specificity(arg);
        if (compareSpecificity(candidate, best) > 0) best = candidate;
      }
      total = addSpecificity(total, best);
    }
    rest = rest.slice(0, match.index) + rest.slice(end);
  }
  total = addSpecificity(total, [(rest.match(/#[\w-]+/g) ?? []).length, 0, 0]);
  const withoutAttributes = rest.replace(/\[[^\]]*\]/g, () => {
    total = addSpecificity(total, [0, 1, 0]);
    return " ";
  });
  const classes = withoutAttributes.match(/\.[\w-]+/g) ?? [];
  const pseudoElements = withoutAttributes.match(/::[\w-]+/g) ?? [];
  const pseudoClasses = (withoutAttributes.match(/(?<!:):[\w-]+/g) ?? [])
    .length;
  total = addSpecificity(total, [0, classes.length + pseudoClasses, 0]);
  const types = withoutAttributes
    .replace(/#[\w-]+|\.[\w-]+|::?[\w-]+/g, " ")
    .split(/[\s>+~]+/)
    .filter((part) => /^[a-zA-Z][\w-]*$/.test(part));
  return addSpecificity(total, [0, 0, types.length + pseudoElements.length]);
}

// nwsapi rejects some valid selectors (for example an :is() list that ends in a
// type selector after a :not() list), so a failing selector is expanded into
// the plain alternatives of its :is() lists and matched one by one.
function expandIs(selector: string): string[] {
  const start = selector.indexOf(":is(");
  if (start < 0) return [selector];
  const argsStart = start + 4;
  let depth = 1;
  let end = argsStart;
  while (end < selector.length && depth > 0) {
    if (selector[end] === "(") depth += 1;
    else if (selector[end] === ")") depth -= 1;
    end += 1;
  }
  const prefix = selector.slice(0, start);
  const suffix = selector.slice(end);
  return splitTopLevel(selector.slice(argsStart, end - 1)).flatMap((arg) =>
    expandIs(prefix + arg + suffix),
  );
}

function selectorMatches(element: Element, selector: string): boolean {
  try {
    return element.matches(selector);
  } catch {
    return expandIs(selector).some((alternative) =>
      element.matches(alternative),
    );
  }
}

// Winning declared value of a longhand for an element, or null when none.
function cascaded(
  element: Element,
  property: string,
  rules: CssRule[],
): string | null {
  let winner: {
    value: string;
    important: boolean;
    specificity: Specificity;
    order: number;
  } | null = null;
  for (const rule of rules) {
    const declaration = rule.declarations.get(property);
    if (declaration === undefined) continue;
    for (const selector of rule.selectors) {
      if (!selectorMatches(element, selector)) continue;
      const candidate = {
        value: declaration.value,
        important: declaration.important,
        specificity: specificity(selector),
        order: rule.order,
      };
      if (
        winner === null ||
        (candidate.important !== winner.important
          ? candidate.important
          : compareSpecificity(candidate.specificity, winner.specificity) !== 0
            ? compareSpecificity(candidate.specificity, winner.specificity) > 0
            : candidate.order > winner.order)
      ) {
        winner = candidate;
      }
    }
  }
  return winner === null ? null : winner.value;
}

function px(value: string | null, name: string): number {
  if (value === null) return 0;
  const match = /^(-?\d+(?:\.\d+)?)(px)?$/.exec(value.trim());
  if (match === null || (match[2] === undefined && Number(match[1]) !== 0)) {
    throw new Error(`${name} must be a plain px length, got "${value}"`);
  }
  return Number(match[1]);
}

// Rules of equal specificity resolve by load order. The app loads the shared
// presentation sheet first (through the source wizard import) and the worker
// sheet last; the reverse order is evaluated too to prove the overlap result
// does not depend on it.
function sheetOrders(width: number): CssRule[][] {
  const wizard = parseSheet(WIZARD_CSS, width);
  const presentation = parseSheet(PRESENTATION_CSS, width);
  const concat = (...sheets: ParsedSheet[]): CssRule[] => {
    let offset = 0;
    const out: CssRule[] = [];
    for (const sheet of sheets) {
      for (const rule of sheet.rules) {
        out.push({ ...rule, order: rule.order + offset });
      }
      offset += sheet.rules.length;
    }
    return out;
  };
  return [concat(presentation, wizard), concat(wizard, presentation)];
}

const STEP_VALUES = [1, 2, 3, 4] as const;

function mountSteps(): { strip: Element; buttons: Element[] } {
  document.body.innerHTML = `
    <div class="desktop-wizard worker-wizard">
      <ol class="worker-steps wizard-progress">
        ${STEP_VALUES.map(
          (value) => `<li data-current="${value === 2}">
            <button type="button" class="worker-step-target">
              <span class="wizard-step-number">${value}</span><span>Step ${value}</span>
            </button>
          </li>`,
        ).join("")}
      </ol>
    </div>`;
  return {
    strip: document.querySelector(".worker-steps")!,
    buttons: Array.from(document.querySelectorAll(".worker-step-target")),
  };
}

// Every width that sits at, just below or just above a width threshold used by
// the two stylesheets, plus a narrow and a wide extreme.
function breakpointWidths(): number[] {
  const widths = new Set<number>([320, 1440]);
  for (const css of [WIZARD_CSS, PRESENTATION_CSS]) {
    for (const match of stripComments(css).matchAll(
      /\((?:max|min)-width:\s*(\d+)px\)/g,
    )) {
      const threshold = Number(match[1]);
      widths.add(threshold - 1);
      widths.add(threshold);
      widths.add(threshold + 1);
    }
  }
  return [...widths].sort((a, b) => a - b);
}

interface StripGeometry {
  width: number;
  wrap: string;
  columnGap: number;
  rowGap: number;
  inlineReach: number;
  blockReach: number;
  minHeight: number;
  sameRowOverlap: number;
  wrappedRowOverlap: number;
}

// Hit-area model: a button extends past its flex item by the absolute value of
// each negative margin. Two neighbours on the same row overlap by the sum of
// the facing reaches minus the column gap; two wrapped rows by the facing
// vertical reaches minus the row gap. A negative result is clearance.
function stripGeometry(width: number, rules: CssRule[]): StripGeometry {
  const { strip, buttons } = mountSteps();
  const button = buttons[0]!;
  const reach = (property: string) =>
    Math.max(0, -px(cascaded(button, property, rules), property));
  const left = reach("margin-left");
  const right = reach("margin-right");
  const top = reach("margin-top");
  const bottom = reach("margin-bottom");
  const columnGap = px(cascaded(strip, "column-gap", rules), "column-gap");
  const rowGap = px(cascaded(strip, "row-gap", rules), "row-gap");
  return {
    width,
    wrap: cascaded(strip, "flex-wrap", rules) ?? "nowrap",
    columnGap,
    rowGap,
    inlineReach: Math.max(left, right),
    blockReach: Math.max(top, bottom),
    minHeight: px(cascaded(button, "min-height", rules), "min-height"),
    sameRowOverlap: right + left - columnGap,
    wrappedRowOverlap: bottom + top - rowGap,
  };
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("Agent Worker step strip hit areas", () => {
  it("evaluates every breakpoint and keeps the overlap result independent of sheet order", () => {
    const widths = breakpointWidths();
    expect(widths).toContain(760);
    expect(widths).toContain(761);
    for (const width of widths) {
      const [first, second] = sheetOrders(width);
      const { minHeight: _first, ...a } = stripGeometry(width, first!);
      const { minHeight: _second, ...b } = stripGeometry(width, second!);
      expect(a).toEqual(b);
    }
  });

  it("loads the shared presentation sheet before the worker sheet", () => {
    const source = readFileSync("src/agent-worker-wizard.tsx", "utf8");
    const sourceWizard = source.indexOf('from "./agent-worker-source-wizard"');
    const workerCss = source.indexOf('import "./agent-worker-wizard.css"');
    expect(sourceWizard).toBeGreaterThan(-1);
    expect(workerCss).toBeGreaterThan(sourceWizard);
    expect(
      readFileSync("src/agent-worker-source-wizard.tsx", "utf8"),
    ).toContain('import "./wizard-presentation.css"');
  });

  it("never overlaps adjacent step hit areas on a row or between wrapped rows", () => {
    for (const width of breakpointWidths()) {
      sheetOrders(width).forEach((rules, index) => {
        const geometry = stripGeometry(width, rules);
        expect(
          geometry.sameRowOverlap,
          `same-row overlap at ${width}px`,
        ).toBeLessThan(0);
        expect(
          geometry.wrappedRowOverlap,
          `wrapped-row overlap at ${width}px`,
        ).toBeLessThan(0);
        // Index 0 is the real load order; the reverse order only lets the
        // 40px shared control height win a specificity tie, never shrinks it.
        expect(
          geometry.minHeight,
          `hit height at ${width}px`,
        ).toBeGreaterThanOrEqual(MIN_HIT_HEIGHT_PX);
        if (index === 0) expect(geometry.minHeight).toBe(MIN_HIT_HEIGHT_PX);
      });
    }
  });

  it("matches the layout table in the Worker wizard step indicator contract", () => {
    // Columns: width range, wrapping, column gap, row gap, horizontal reach per
    // side, vertical reach per side, same-row overlap, wrapped-row overlap.
    const observed = new Map<string, string>();
    for (const width of breakpointWidths()) {
      const g = stripGeometry(width, sheetOrders(width)[0]!);
      const key = [
        g.wrap,
        `${g.columnGap}px`,
        `${g.rowGap}px`,
        `${g.inlineReach}px`,
        `${g.blockReach}px`,
        `${g.sameRowOverlap}px`,
        `${g.wrappedRowOverlap}px`,
      ].join(" | ");
      observed.set(key, key);
    }
    // One row of the table covers every width: the layout does not change at
    // any stylesheet breakpoint.
    expect([...observed.values()]).toEqual([
      "wrap | 24px | 24px | 8px | 4px | -8px | -16px",
    ]);
  });

  it("keeps the shared wizard-progress gap in force over the worker strip rules", () => {
    // A gap declared on .worker-steps would be silently overridden by the
    // higher-specificity shared rule, so none may be declared in either sheet.
    for (const width of breakpointWidths()) {
      const wizard = parseSheet(WIZARD_CSS, width);
      for (const rule of wizard.rules) {
        if (!rule.selectors.some((s) => /\.worker-steps(?![\w-])/.test(s))) {
          continue;
        }
        if (rule.selectors.some((s) => s.includes("worker-step-target"))) {
          continue;
        }
        expect(rule.declarations.has("row-gap")).toBe(false);
        expect(rule.declarations.has("column-gap")).toBe(false);
      }
    }
  });

  it("has no rule the evaluator skips that could restyle the step strip", () => {
    for (const width of breakpointWidths()) {
      for (const css of [WIZARD_CSS, PRESENTATION_CSS]) {
        for (const selector of parseSheet(css, width).unmodeled) {
          expect(
            /worker-step|wizard-progress|worker-steps/.test(selector),
            `${selector} sits in an at-rule the hit-area guard does not model`,
          ).toBe(false);
        }
      }
    }
  });
});

// Sweep: the same overlap class in every other control strip. A negative
// inline margin or pseudo-element offset is the only way these stylesheets
// extend a control past its layout box, so each use must be a visually hidden
// helper or a documented single control.
const SRC_DIRECTORY = "src";

const NEGATIVE_INLINE_MARGIN_ALLOWLIST = new Map<string, string>([
  [
    ".external-link-error > button",
    "Sole dismiss control at the end of its row; it has no adjacent control.",
  ],
  [
    ".worker-steps button.worker-step-target",
    "Step strip button; covered by the geometry tests above.",
  ],
]);

function stylesheetFiles(): string[] {
  return readdirSync(SRC_DIRECTORY)
    .filter((name) => name.endsWith(".css") && !name.includes(".fixture."))
    .sort();
}

describe("Horizontal hit-area extension across stylesheets", () => {
  it("allows negative inline margins only for the documented controls", () => {
    const seen = new Set<string>();
    for (const file of stylesheetFiles()) {
      const sheet = parseSheet(
        readFileSync(`${SRC_DIRECTORY}/${file}`, "utf8"),
        1024,
      );
      const rules = [
        ...sheet.rules,
        ...parseSheet(readFileSync(`${SRC_DIRECTORY}/${file}`, "utf8"), 320)
          .rules,
      ];
      for (const rule of rules) {
        const negative = ["margin-left", "margin-right"].some((property) => {
          const declaration = rule.declarations.get(property);
          return declaration !== undefined && /^-\d/.test(declaration.value);
        });
        if (!negative) continue;
        // Visually hidden helpers clip themselves to one pixel.
        if (
          rule.declarations.has("clip") ||
          rule.declarations.has("clip-path")
        ) {
          continue;
        }
        for (const selector of rule.selectors) {
          seen.add(selector);
          expect(
            NEGATIVE_INLINE_MARGIN_ALLOWLIST.has(selector),
            `${file}: ${selector} extends inline margins negatively; add a gap-based guard or document it`,
          ).toBe(true);
        }
      }
    }
    expect([...seen].sort()).toEqual(
      [...NEGATIVE_INLINE_MARGIN_ALLOWLIST.keys()].sort(),
    );
  });

  it("never enlarges a hit area with a pseudo-element offset", () => {
    for (const file of stylesheetFiles()) {
      for (const width of [320, 1024]) {
        const { rules } = parseSheet(
          readFileSync(`${SRC_DIRECTORY}/${file}`, "utf8"),
          width,
        );
        for (const rule of rules) {
          if (!rule.selectors.some((s) => /::?(before|after)/.test(s))) {
            continue;
          }
          for (const property of ["left", "right", "inset", "inset-inline"]) {
            const declaration = rule.declarations.get(property);
            expect(
              declaration === undefined || !/(^|\s)-\d/.test(declaration.value),
              `${file}: ${rule.selectors.join(", ")} offsets ${property} negatively`,
            ).toBe(true);
          }
        }
      }
    }
  });

  it("keeps the shared tab strip items edge to edge with no overlap", () => {
    const tabs = parseSheet(
      readFileSync(`${SRC_DIRECTORY}/tab-strip.css`, "utf8"),
      320,
    );
    const strip = tabs.rules.find((rule) =>
      rule.selectors.includes(".tab-strip"),
    )!;
    expect(strip.declarations.get("column-gap")?.value ?? "0").toMatch(/^0/);
    for (const rule of tabs.rules) {
      for (const property of ["margin-left", "margin-right"]) {
        const declaration = rule.declarations.get(property);
        expect(
          declaration === undefined || !declaration.value.startsWith("-"),
        ).toBe(true);
      }
    }
  });
});
