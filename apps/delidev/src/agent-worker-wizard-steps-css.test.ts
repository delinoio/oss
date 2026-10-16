// SPDX-License-Identifier: Apache-2.0
// jsdom does not evaluate media queries, so this guard loads the committed
// stylesheets, unwraps every max-width media block (the narrow-viewport case)
// and asserts the cascaded display of each Agent Worker step label.
import { readFileSync } from "node:fs";
import { afterEach, describe, expect, it } from "vitest";

const WIZARD_CSS = readFileSync("src/agent-worker-wizard.css", "utf8");
const PRESENTATION_CSS = readFileSync("src/wizard-presentation.css", "utf8");

enum ViewportKind {
  Wide = "wide",
  Narrow = "narrow",
}

const STEP_VALUES = [1, 2, 3, 4] as const;

// Flattens at-rules for a narrow or wide viewport: max-width media blocks are
// unwrapped for narrow viewports and dropped for wide ones. Other at-rules
// (such as @container, which the jsdom CSS parser rejects) are removed because
// they never target the step label display.
function flattenMedia(css: string, viewport: ViewportKind): string {
  let out = "";
  let i = 0;
  while (i < css.length) {
    const at = css.indexOf("@", i);
    if (at < 0) {
      out += css.slice(i);
      break;
    }
    out += css.slice(i, at);
    const open = css.indexOf("{", at);
    const query = css.slice(at, open);
    let depth = 1;
    let j = open + 1;
    while (j < css.length && depth > 0) {
      if (css[j] === "{") depth += 1;
      else if (css[j] === "}") depth -= 1;
      j += 1;
    }
    const maxWidth = /^@media[^{]*max-width:\s*(\d+)px/.exec(query);
    if (
      viewport === ViewportKind.Narrow &&
      maxWidth !== null &&
      Number(maxWidth[1]) >= 320
    ) {
      out += css.slice(open + 1, j - 1);
    }
    i = j;
  }
  return out;
}

// Mirrors the wizard markup: the current and visited steps (up to `visited`)
// are buttons, later steps are plain spans.
function mountSteps(current: number, visited = current): HTMLElement[] {
  document.body.innerHTML = `
    <div class="desktop-wizard worker-wizard">
      <ol class="worker-steps wizard-progress">
        ${STEP_VALUES.map((value) => {
          const inner = `<span class="wizard-step-number" aria-hidden="true">${value}</span>
            <span>Step label ${value}</span>`;
          return `<li data-current="${value === current}" data-completed="${value < current}">
            ${
              value <= visited
                ? `<button type="button" class="worker-step-target"${value === current ? ' aria-current="step"' : ""}>${inner}</button>`
                : `<span class="worker-step-target">${inner}</span>`
            }
          </li>`;
        }).join("")}
      </ol>
    </div>`;
  return Array.from(
    document.querySelectorAll<HTMLElement>(
      ".worker-steps .worker-step-target > span:last-child",
    ),
  );
}

function installStyles(viewport: ViewportKind) {
  const style = document.createElement("style");
  style.id = "worker-steps-test-style";
  style.textContent =
    flattenMedia(PRESENTATION_CSS, viewport) +
    flattenMedia(WIZARD_CSS, viewport);
  document.head.appendChild(style);
  // jsdom silently ignores stylesheets it cannot parse, which would make the
  // computed-style assertions vacuous.
  expect(style.sheet).not.toBeNull();
  expect(style.sheet!.cssRules.length).toBeGreaterThan(10);
}

afterEach(() => {
  document.getElementById("worker-steps-test-style")?.remove();
  document.body.innerHTML = "";
});

describe("Agent Worker step labels", () => {
  it("keeps the narrow-viewport media block loaded by the test", () => {
    const narrow = flattenMedia(WIZARD_CSS, ViewportKind.Narrow);
    expect(narrow).toContain(".worker-steps");
    expect(flattenMedia(WIZARD_CSS, ViewportKind.Wide)).not.toContain(
      ".worker-footer {\n    flex-wrap",
    );
  });

  it.each([ViewportKind.Wide, ViewportKind.Narrow])(
    "shows all four labels for every active step (%s viewport)",
    (viewport) => {
      installStyles(viewport);
      for (const current of STEP_VALUES) {
        const labels = mountSteps(current);
        expect(labels).toHaveLength(4);
        for (const label of labels) {
          expect(getComputedStyle(label).display).not.toBe("none");
        }
      }
    },
  );

  it("never hides a wizard-progress label span in any stylesheet rule", () => {
    for (const css of [WIZARD_CSS, PRESENTATION_CSS]) {
      for (const viewport of [ViewportKind.Wide, ViewportKind.Narrow]) {
        const flat = flattenMedia(css, viewport);
        for (const match of flat.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
          const selector = match[1]!;
          const declarations = match[2]!;
          if (!/worker-steps|wizard-progress/.test(selector)) continue;
          if (/wizard-step-number|span:first-child/.test(selector)) continue;
          expect(
            /display:\s*none/.test(declarations),
            `${selector.trim()} must not hide step labels`,
          ).toBe(false);
        }
      }
    }
  });

  it("keeps the 28px number circle geometry in the wide viewport", () => {
    expect(WIZARD_CSS).toMatch(
      /\.worker-steps \.wizard-step-number \{[^}]*width: 28px;[^}]*height: 28px;/,
    );
    expect(PRESENTATION_CSS).toContain("font-size: 14px");
  });

  it.each([ViewportKind.Wide, ViewportKind.Narrow])(
    "keeps number and label together in one flex target (%s viewport)",
    (viewport) => {
      installStyles(viewport);
      mountSteps(2, 3);
      for (const target of document.querySelectorAll<HTMLElement>(
        ".worker-step-target",
      )) {
        expect(getComputedStyle(target).display).toBe("flex");
        expect(
          getComputedStyle(target.querySelector(".wizard-step-number")!).width,
        ).toBe("28px");
      }
    },
  );

  it.each([ViewportKind.Wide, ViewportKind.Narrow])(
    "keeps every label atomic: nowrap, never broken inside a word (%s viewport)",
    (viewport) => {
      installStyles(viewport);
      mountSteps(2, 3);
      for (const label of document.querySelectorAll<HTMLElement>(
        ".worker-step-target > span:last-child",
      )) {
        const style = getComputedStyle(label);
        expect(style.whiteSpace).toBe("nowrap");
        expect(style.overflowWrap).toBe("normal");
        expect(style.wordBreak).toBe("normal");
      }
    },
  );

  it("sizes the padded step button to its content below every host width cap", () => {
    // Each host caps controls with max-width: 100% at a different selector
    // specificity (shared wizard, Settings dialog, Settings page). The cap
    // excludes the 16px of padding the negative margins give back, which
    // squeezed the label until it broke inside words. min-width always beats
    // max-width, so it holds in every host without a specificity race. Real
    // line geometry is covered by scripts/test-worker-steps-layout.mjs.
    expect(WIZARD_CSS).toMatch(
      /\.worker-steps button\.worker-step-target \{[^}]*min-width: max-content;/,
    );
    for (const host of [
      "src/wizard-presentation.css",
      "src/settings-task.css",
      "src/settings-presentation.css",
    ]) {
      expect(readFileSync(host, "utf8"), host).toContain("max-width: 100%");
    }
    // The fix must not be an !important override or a shared-control change.
    expect(WIZARD_CSS).not.toMatch(/!important/);
    expect(PRESENTATION_CSS).not.toContain("max-content");
  });

  it("scopes the hover surface and focus ring to enabled step buttons", () => {
    const flat = flattenMedia(WIZARD_CSS, ViewportKind.Narrow);
    expect(flat).toMatch(
      /\.worker-steps button\.worker-step-target:hover:not\(:disabled\) \{[^}]*background: var\(--selected-background\)/,
    );
    expect(flat).toMatch(
      /\.worker-steps button\.worker-step-target:focus-visible \{[^}]*outline: 2px solid var\(--focus\)/,
    );
    // Unvisited steps are spans, so no rule gives them hover or focus styling.
    expect(flat).not.toMatch(/span\.worker-step-target:(hover|focus)/);
    // The shared wizard-progress styles used by other wizards stay untouched.
    expect(PRESENTATION_CSS).not.toContain("worker-step-target");
  });

  it("styles the current step through the data attribute on the item", () => {
    expect(WIZARD_CSS).toContain('.worker-steps li[data-current="true"]');
    expect(WIZARD_CSS).toContain(
      '.worker-steps li[data-current="true"] .wizard-step-number',
    );
  });
});
