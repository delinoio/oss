// SPDX-License-Identifier: Apache-2.0
// jsdom evaluates the committed stylesheet cascade, so this guard injects
// pr-workspace.css and asserts that the compact author layout applies only to
// the list metadata while commit author lines keep their wrapping flex layout.
import { readFileSync } from "node:fs";
import { afterEach, beforeEach, expect, it } from "vitest";

const CSS = readFileSync("src/pr-workspace.css", "utf8");

let style: HTMLStyleElement;

beforeEach(() => {
  style = document.createElement("style");
  style.textContent = CSS;
  document.head.append(style);
});

afterEach(() => {
  style.remove();
  document.body.replaceChildren();
});

it("keeps the compact author layout inside the list metadata only", () => {
  document.body.innerHTML = `
    <span class="pr-row-meta"><span class="pr-row-author" id="list"></span></span>
    <article class="pr-commit"><p class="pr-row-author" id="commit"></p></article>
  `;
  const list = getComputedStyle(document.getElementById("list")!);
  expect(list.display).toBe("inline-flex");
  expect(list.flexWrap).toBe("nowrap");
  expect(list.gap).toBe("4px");
});

it("keeps wrapping flex author and committer lines in the commit detail", () => {
  document.body.innerHTML = `
    <article class="pr-commit"><p class="pr-row-author" id="commit"></p></article>
  `;
  const commit = getComputedStyle(document.getElementById("commit")!);
  expect(commit.display).toBe("flex");
  expect(commit.flexWrap).toBe("wrap");
  expect(commit.gap).toBe("8px");
  expect(commit.fontSize).toBe("13px");
});

it("scopes every compact author rule to the list metadata", () => {
  const rules = CSS.replace(/\/\*[\s\S]*?\*\//g, "");
  const selectors = [...rules.matchAll(/([^{}]+)\{[^}]*\}/g)]
    .flatMap((match) => match[1].split(","))
    .map((selector) => selector.trim())
    .filter((selector) => selector.includes("pr-row-author"));
  expect(selectors).toEqual([".pr-row-author", ".pr-row-meta .pr-row-author"]);
});
