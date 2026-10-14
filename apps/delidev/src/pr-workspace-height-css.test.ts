// SPDX-License-Identifier: Apache-2.0
// jsdom has no layout engine, so this guard reads the committed stylesheets and
// asserts the height contract of the standalone pull request workspace. The
// browser fixture scripts/test-pr-cards-layout.mjs measures the real geometry.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const WORKSPACE_CSS = readFileSync("src/pr-workspace.css", "utf8");
const CARDS_CSS = readFileSync("src/pr-list-cards.css", "utf8");

enum RuleSelector {
  PageLayout = '.page.pull-requests-page[data-list-layout="true"]',
  Workspace = ".pr-workspace",
  WideWorkspace = '.pr-workspace[data-wide="true"]',
  Panes = ".pr-workspace-list,\n.pr-workspace-selected",
}

// Returns the declaration block of the first rule whose selector list matches
// exactly, so comments and unrelated rules never satisfy an assertion.
function declarations(css: string, selector: RuleSelector): string {
  const stripped = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const start = stripped.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`rule not found: ${selector}`);
  const open = stripped.indexOf("{", start);
  return stripped.slice(open + 1, stripped.indexOf("}", open));
}

describe("pull request workspace height contract", () => {
  it("removes the viewport-relative height cap from the workspace", () => {
    const rule = declarations(WORKSPACE_CSS, RuleSelector.Workspace);
    expect(rule).not.toMatch(/(^|\s)height\s*:/);
    expect(WORKSPACE_CSS).not.toMatch(/75vh|1000px/);
  });

  it("grows the workspace into the remaining page height with a 320px floor", () => {
    const rule = declarations(WORKSPACE_CSS, RuleSelector.Workspace);
    expect(rule).toMatch(/flex\s*:\s*1 1 auto/);
    expect(rule).toMatch(/min-height\s*:\s*320px/);
    expect(rule).toMatch(/grid-template-rows\s*:\s*minmax\(0,\s*1fr\)/);
  });

  it("keeps the 40/60 split in the wide workspace", () => {
    const rule = declarations(WORKSPACE_CSS, RuleSelector.WideWorkspace);
    expect(rule).toMatch(
      /grid-template-columns\s*:\s*minmax\(0,\s*40fr\)\s*minmax\(0,\s*60fr\)/,
    );
  });

  it("keeps independent pane overflow without percentage heights", () => {
    const rule = declarations(WORKSPACE_CSS, RuleSelector.Panes);
    expect(rule).toMatch(/overflow\s*:\s*auto/);
    expect(rule).toMatch(/overscroll-behavior\s*:\s*contain/);
    expect(rule).toMatch(/scrollbar-gutter\s*:\s*stable/);
    expect(rule).toMatch(/min-height\s*:\s*0/);
    expect(rule).not.toMatch(/(^|\s)height\s*:/);
  });

  it("makes the page a column that fills main and keeps its 32px padding", () => {
    const rule = declarations(CARDS_CSS, RuleSelector.PageLayout);
    expect(rule).toMatch(/display\s*:\s*flex/);
    expect(rule).toMatch(/flex-direction\s*:\s*column/);
    expect(rule).toMatch(/min-height\s*:\s*100%/);
    expect(rule).toMatch(/box-sizing\s*:\s*border-box/);
    expect(rule).toMatch(/padding\s*:\s*32px/);
  });
});
