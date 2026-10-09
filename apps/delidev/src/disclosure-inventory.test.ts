// SPDX-License-Identifier: Apache-2.0
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

it("keeps raw product details limited to the two specialized action menus", () => {
  const directory = resolve(process.cwd(), "src") + "/";
  const excluded = new Map([["saved-connections.tsx", "connections-row-menu"], ["session-browser.tsx", "browser-overflow"]]);
  const remaining: string[] = [];
  for (const file of readdirSync(directory)) {
    if (!file.endsWith(".tsx") || /\.(test|fixture)\./.test(file) || file === "disclosure.tsx") continue;
    const source = readFileSync(directory + file, "utf8");
    for (const match of source.matchAll(/<details\b/g)) {
      const start = source.slice(match.index, match.index + 100);
      if (!excluded.has(file) || !start.includes(`className="${excluded.get(file)}"`)) remaining.push(file);
    }
  }
  expect(remaining).toEqual([]);
});
