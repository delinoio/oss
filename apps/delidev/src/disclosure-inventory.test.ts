// SPDX-License-Identifier: Apache-2.0
import { readdirSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, it } from "vitest";

it("keeps raw product details limited to the shared anchored action menu and one native-view exception", () => {
  const directory = resolve(process.cwd(), "src") + "/";
  // Persistent disclosures use disclosure.tsx; row action menus use
  // details-action-menu.tsx, whose actions are an anchored top-layer popup. The
  // Session Browser overflow stays an in-flow details because its neighbor is a
  // native view that top-layer web content cannot cover (see the dropdown
  // inventory in the desktop contract).
  const owners = new Set(["disclosure.tsx", "details-action-menu.tsx"]);
  const excluded = new Map([["session-browser.tsx", "browser-overflow"]]);
  const remaining: string[] = [];
  for (const file of readdirSync(directory)) {
    if (
      !file.endsWith(".tsx") ||
      /\.(test|fixture)\./.test(file) ||
      owners.has(file)
    )
      continue;
    const source = readFileSync(directory + file, "utf8");
    for (const match of source.matchAll(/<details\b/g)) {
      const start = source.slice(match.index, match.index + 100);
      if (
        !excluded.has(file) ||
        !start.includes(`className="${excluded.get(file)}"`)
      )
        remaining.push(file);
    }
  }
  expect(remaining).toEqual([]);
});
