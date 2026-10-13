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

function mountSteps(current: number): HTMLElement[] {
  document.body.innerHTML = `
    <div class="desktop-wizard worker-wizard">
      <ol class="worker-steps wizard-progress">
        ${STEP_VALUES.map(
          (
            value,
          ) => `<li${value === current ? ' aria-current="step"' : ""} data-completed="${value < current}">
            <span class="wizard-step-number" aria-hidden="true">${value}</span>
            <span>Step label ${value}</span>
          </li>`,
        ).join("")}
      </ol>
    </div>`;
  return Array.from(
    document.querySelectorAll<HTMLElement>(
      ".worker-steps li > span:last-child",
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
      /\.worker-steps li > span:first-child \{[^}]*width: 28px;[^}]*height: 28px;/,
    );
    expect(PRESENTATION_CSS).toContain("font-size: 14px");
  });
});
