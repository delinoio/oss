// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

// These source-policy checks do not establish packaged trackpad behavior.
const source = (file) => readFileSync(new URL(`../src/${file}`, import.meta.url), "utf8");
function declarations(file, selector) {
  const rules = [...source(file).replace(/\/\*[\s\S]*?\*\//g, "").matchAll(/([^{}]+)\{([^{}]*)\}/g)];
  const rule = rules.find(([, selectors]) => selectors.split(",").some((item) => item.trim() === selector));
  assert(rule, `Missing owner rule: ${selector}`);
  return Object.fromEntries(rule[2].split(";").filter((item) => item.includes(":")).map((item) => {
    const colon = item.indexOf(":");
    return [item.slice(0, colon).trim(), item.slice(colon + 1).trim()];
  }));
}

for (const [file, selector, overflow] of [
  ["styles.css", ".sidebar-list", "overflow-y"],
  ["styles.css", ".sidebar-footer", "overflow"],
  ["styles.css", ".sidebar-server-management", "overflow-y"],
  ["styles.css", ".sidebar-session-tooltip", "overflow"],
  ["styles.css", ".sidebar-session-menu", "overflow"],
  ["usage.css", ".usage-sidebar .usage-filter-scroll", "overflow-y"],
  ["subscription-rail.css", ".subscription-rail-scroll", "overflow-y"],
  ["subscription-rail.css", ".subscription-rail-popover", "overflow"],
]) {
  test(`${selector} suppresses both boundary axes and retains internal scrolling`, () => {
    const rule = declarations(file, selector);
    assert.equal(rule["overscroll-behavior"], "none");
    assert.equal(rule[overflow], "auto");
  });
}

test("picker boundaries follow sidebar DOM ownership in panes and compact drawers", () => {
  assert.equal(declarations("scroll-picker.css", ".scroll-picker-popup")["overscroll-behavior"], "contain");
  for (const owner of [".sidebar-pane", ".sidebar-surface-content"]) {
    assert.equal(declarations("scroll-picker.css", `${owner} .scroll-picker-popup`)["overscroll-behavior"], "none");
  }
  assert(!source("scroll-picker.tsx").includes("createPortal"), "Top-layer picker must retain its DOM owner");
  assert.match(source("sidebar.tsx"), /className=\{`sidebar-pane\$\{/);
  assert.match(source("sidebar.tsx"), /sidebar-pane-dialog\$\{compact && drawerOpen/);
});

test("sidebar boundaries leave main content and task/dialog policies independent", () => {
  for (const [file, selector] of [
    ["styles.css", "main"],
    ["styles.css", ".inbox-list-scroll"],
    ["styles.css", ".inbox-detail-pane"],
    ["session.css", ".session-workspace .transcript"],
    ["settings-task.css", ".settings-task-body"],
    ["usage.css", ".usage-page .usage-table"],
    ["usage.css", ".usage-page .usage-detail > .usage-table"],
  ]) {
    const rule = declarations(file, selector);
    assert.notEqual(rule["overscroll-behavior"], "none", selector);
    assert.notEqual(rule["overscroll-behavior-y"], "none", selector);
  }
});
