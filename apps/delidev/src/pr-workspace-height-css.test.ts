// SPDX-License-Identifier: Apache-2.0
// jsdom has no layout engine, so this guard reads the committed stylesheets and
// asserts the height contract of the standalone pull request workspace. The
// browser fixture scripts/test-pr-cards-layout.mjs measures the real geometry.
import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const WORKSPACE_CSS = readFileSync("src/pr-workspace.css", "utf8");
const CARDS_CSS = readFileSync("src/pr-list-cards.css", "utf8");
const SHELL_CSS = readFileSync("src/styles.css", "utf8");

enum RuleSelector {
  Shell = ".app",
  VisibleMain = 'main:has(> .page.pull-requests-page[data-list-layout="true"]:not([hidden]))',
  MainSiblings = 'main:has(> .page.pull-requests-page[data-list-layout="true"]:not([hidden])) > :not(.pull-requests-page)',
  PageLayout = '.page.pull-requests-page[data-list-layout="true"]',
  Workspace = ".pr-workspace",
  WideWorkspace = '.pr-workspace[data-wide="true"]',
  Panes = ".pr-workspace-list, .pr-workspace-selected",
}

// Returns the declaration block of the first rule whose selector list matches
// exactly after whitespace normalization, so comments, formatting and unrelated
// rules never satisfy or break an assertion.
function declarations(css: string, selector: RuleSelector): string {
  const stripped = css
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .replace(/\s+/g, " ")
    .replace(/\( /g, "(");
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
    expect(rule).toMatch(/flex\s*:\s*1 1 0px/);
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

  it("makes the page a column that grows into the height left in main", () => {
    const rule = declarations(CARDS_CSS, RuleSelector.PageLayout);
    expect(rule).toMatch(/display\s*:\s*flex/);
    expect(rule).toMatch(/flex-direction\s*:\s*column/);
    expect(rule).toMatch(/flex\s*:\s*1 0 auto/);
    expect(rule).toMatch(/box-sizing\s*:\s*border-box/);
    expect(rule).toMatch(/padding\s*:\s*32px/);
  });

  it("never sizes the page from the full main height", () => {
    // A percentage minimum height would ignore the compact context trigger and
    // the sidebar preference notice that precede the page inside main.
    const rule = declarations(CARDS_CSS, RuleSelector.PageLayout);
    expect(rule).not.toMatch(/(^|\s)(min-|max-)?height\s*:/);
    expect(CARDS_CSS).not.toMatch(/min-height\s*:\s*100%/);
  });

  it("lets main share its height between preceding siblings and the page", () => {
    const main = declarations(CARDS_CSS, RuleSelector.VisibleMain);
    expect(main).toMatch(/display\s*:\s*flex/);
    expect(main).toMatch(/flex-direction\s*:\s*column/);
    expect(main).not.toMatch(/(^|\s)(min-|max-)?height\s*:/);
    const siblings = declarations(CARDS_CSS, RuleSelector.MainSiblings);
    expect(siblings).toMatch(/flex-shrink\s*:\s*0/);
  });

  it("keeps the chain from the app shell to the workspace definite", () => {
    // .app owns the viewport height; main, the page and the workspace then
    // only distribute it, so no container between them has its own height.
    const shell = declarations(SHELL_CSS, RuleSelector.Shell);
    expect(shell).toMatch(/height\s*:\s*100dvh/);
    const chain = [
      declarations(CARDS_CSS, RuleSelector.VisibleMain),
      declarations(CARDS_CSS, RuleSelector.PageLayout),
      declarations(WORKSPACE_CSS, RuleSelector.Workspace),
    ];
    for (const rule of chain) {
      expect(rule).not.toMatch(/(^|[\s;])height\s*:/);
      expect(rule).not.toMatch(/\d+(vh|dvh|svh|lvh)/);
    }
  });
  it("keeps the 32px bottom padding in the compact layout", () => {
    // The desktop contract states one 32px bottom padding for every width, so
    // the compact override may only shrink the top and inline insets.
    const stripped = CARDS_CSS.replace(/\/\*[\s\S]*?\*\//g, "").replace(
      /\s+/g,
      " ",
    );
    const media = stripped.slice(stripped.indexOf("@media (max-width: 759px)"));
    const rule = media.slice(
      media.indexOf(`${RuleSelector.PageLayout} {`),
      media.indexOf("}"),
    );
    const padding = /padding\s*:\s*([^;]+);/.exec(rule)?.[1].trim().split(" ");
    expect(padding).toEqual(["24px", "16px", "32px"]);
    expect(rule).not.toMatch(/padding-bottom/);
  });
});
