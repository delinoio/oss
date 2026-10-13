// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { render } from "@testing-library/react";
import { expect, it } from "vitest";
import { revealTabItem } from "./tab-strip";

const read = (name: string) => readFileSync(`src/${name}`, "utf8");
const strip = read("tab-strip.css");
const withoutComments = (css: string) => css.replace(/\/\*[\s\S]*?\*\//g, "");
/** Returns the declaration block of the first rule with exactly this selector list. */
function block(css: string, selector: string) {
  for (const match of withoutComments(css).matchAll(/([^{}]+)\{([^{}]*)\}/g))
    if (
      match[1]
        .split(",")
        .map((value) => value.trim().replace(/\s+/g, " "))
        .includes(selector)
    )
      return match[2];
  throw new Error(`Missing rule: ${selector}`);
}
/** Splits a selector list at top-level commas only. */
function splitSelectors(list: string) {
  const result: string[] = [];
  let depth = 0,
    start = 0;
  for (let index = 0; index < list.length; index += 1) {
    const character = list[index];
    if (character === "(" || character === "[") depth += 1;
    else if (character === ")" || character === "]") depth -= 1;
    else if (character === "," && depth === 0) {
      result.push(list.slice(start, index).trim());
      start = index + 1;
    }
  }
  result.push(list.slice(start).trim());
  return result;
}
const surfaces = [
  ["project-edit-tabs", "project-edit-tabs.tsx", "project-edit-tabs.css"],
  ["usage", "usage.tsx", "usage.css"],
  ["backups", "backups.tsx", "backups.css"],
  ["session tab bar", "session-tab-bar.tsx", "session.css"],
  ["terminal selector", "session-terminals.tsx", "terminal-dock.css"],
] as const;

it("defines flat unboxed items with the approved geometry and one neutral baseline", () => {
  const item = block(strip, ".tab-strip .tab-item");
  expect(item).toContain("min-height: 40px");
  expect(item).toContain("border-bottom: 2px solid transparent");
  expect(item).toContain("border-radius: 0");
  expect(item).toContain("background: transparent");
  expect(item).toContain("font-size: 14px");
  expect(item).toContain("line-height: 20px");
  expect(item).toContain("color: var(--tab-muted, var(--muted))");
  expect(item).not.toMatch(/border-right|border-left|box-shadow: [^n]/);
  expect(block(strip, ".tab-strip button.tab-item")).toContain(
    "padding: 8px 16px",
  );
  expect(block(strip, ".tab-strip .tab-label")).toContain("padding: 8px 16px");
  const container = block(strip, ".tab-strip");
  expect(container).toContain("gap: 0");
  expect(container).toContain("flex-wrap: nowrap");
  expect(container).toContain("overflow-x: auto");
  expect(container).toContain("overflow-y: hidden");
  expect(container).toContain("max-width: 100%");
  expect(container).toContain(
    "box-shadow: inset 0 -1px 0 var(--tab-baseline, var(--border))",
  );
  expect(block(strip, ".tab-strip .tab-item.is-selected")).toContain(
    "border-bottom-color: var(--tab-selected-border, var(--selected-border))",
  );
  expect(block(strip, ".tab-strip .tab-item.is-selected")).toContain(
    "color: var(--tab-selected-text, var(--selected-text))",
  );
  expect(block(strip, ".tab-strip .tab-item-dynamic")).toContain(
    "max-width: 320px",
  );
});

it("keeps labels from overlapping the always-visible 40px close target", () => {
  const label = block(strip, ".tab-strip .tab-label");
  expect(label).toContain("min-width: 0");
  expect(label).toContain("overflow: hidden");
  expect(label).toContain("text-overflow: ellipsis");
  expect(label).toContain("white-space: nowrap");
  const close = block(strip, ".tab-strip .tab-close");
  expect(close).toContain("flex: none");
  expect(close).toContain("width: 40px");
  expect(close).toContain("height: 40px");
  expect(close).not.toMatch(/opacity|visibility|display: none/);
});

it("draws a focus indicator independent of selection, inside the scrolling strip", () => {
  for (const selector of [
    ".tab-strip .tab-label:focus-visible",
    ".tab-strip .tab-close:focus-visible",
  ]) {
    const focus = block(strip, selector);
    expect(focus).toContain(
      "outline: 2px solid var(--tab-focus, var(--focus))",
    );
    expect(focus).toContain("outline-offset: -2px");
  }
});

it.each(surfaces)(
  "%s imports the shared stylesheet and drops its private tab geometry",
  (_name, tsx, css) => {
    expect(read(tsx)).toContain('import "./tab-strip.css"');
    expect(read(tsx)).toContain("tab-strip");
    const own = withoutComments(read(css));
    expect(own).not.toMatch(
      /(?:usage-tabs|backups-tabs|project-edit-tablist|session-tab|terminal-tabs)[^{]*\{[^}]*(?:border-bottom: 3px|border-radius: (?:8px 8px|4px 4px)|border-right)/,
    );
    expect(own).not.toContain(".browser-pages");
  },
);

it("excludes shared tab controls from broad form-button rules via zero-specificity guards", () => {
  for (const name of ["settings-task.css", "settings-presentation.css"]) {
    const css = withoutComments(read(name));
    for (const match of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
      const [, selector, declarations] = match;
      if (
        /(border-color: var\(--control-border\)|border-radius: 8px|white-space: normal)/.test(
          declarations,
        ) &&
        /\bbutton\b/.test(selector) &&
        !/\.settings-task-footer|\.primary|\.paired-device-revoke/.test(
          selector,
        )
      )
        expect(selector, `${name}: ${selector.trim()}`).toContain(
          "button:where(:not(.tab-item, .tab-label, .tab-close))",
        );
    }
  }
});

it("broad Settings control selectors do not match shared tab controls but still match ordinary buttons", () => {
  const css = withoutComments(read("settings-task.css"));
  const selectors = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)]
    .filter(
      ([, selector, declarations]) =>
        /border-color: var\(--control-border\)|border-radius: 8px/.test(
          declarations,
        ) && selector.includes(".settings-task-body"),
    )
    .flatMap(([, selector]) => splitSelectors(selector))
    .flatMap((selector) => {
      // jsdom's selector engine cannot parse :is() lists holding nested
      // :where(:not()) arguments, so test each button argument as a plain compound.
      const match = /^(.*?)\s*:is\(([\s\S]*)\)$/.exec(selector);
      return match
        ? splitSelectors(match[2]).map((argument) =>
            `${match[1]} ${argument}`.replace(/\s+/g, " "),
          )
        : [selector.replace(/\s+/g, " ")];
    })
    .filter((selector) => selector.includes("button:where"));
  expect(selectors.length).toBeGreaterThanOrEqual(2);
  const view = render(
    <div className="settings-task-body">
      <div className="tab-strip">
        <button className="tab-item tab-label" id="item">
          Execution
        </button>
        <div className="tab-item">
          <button className="tab-label" id="label">
            Browser
          </button>
          <button className="tab-close" id="close">
            ×
          </button>
        </div>
      </div>
      <button id="ordinary">Save</button>
    </div>,
  );
  for (const id of ["item", "label", "close"])
    for (const selector of selectors)
      expect(
        view.container.querySelector(`#${id}`)!.matches(selector),
        `#${id} vs ${selector}`,
      ).toBe(false);
  for (const selector of selectors)
    expect(
      view.container.querySelector("#ordinary")!.matches(selector),
      `ordinary vs ${selector}`,
    ).toBe(true);
});

it("reveals the focused or selected item without throwing where scrolling is unavailable", () => {
  const calls: unknown[] = [];
  const element = document.createElement("button");
  expect(() => revealTabItem(element)).not.toThrow();
  expect(() => revealTabItem(null)).not.toThrow();
  element.scrollIntoView = (options) => void calls.push(options);
  revealTabItem(element);
  expect(calls).toEqual([{ block: "nearest", inline: "nearest" }]);
});
