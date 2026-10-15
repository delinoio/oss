// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const css = (name: string) =>
  readFileSync(join(process.cwd(), "src", name), "utf8").replace(
    /\/\*[\s\S]*?\*\//g,
    "",
  );

it("sizes the sidebar harness mark as a fixed 14px square slot that outranks the 48px picker mark", () => {
  const styles = css("styles.css");
  const rule = /\.worker-harness-mark\.sidebar-harness-mark\s*\{([^}]*)\}/.exec(
    styles,
  );
  expect(rule).not.toBeNull();
  const body = rule![1].replace(/\s+/g, " ");
  expect(body).toContain("flex: 0 0 14px");
  expect(body).toContain("width: 14px");
  expect(body).toContain("height: 14px");
  expect(body).toContain("margin: 0");
  // The base mark keeps contained proportions; the override must not drop them.
  expect(css("harness-mark.css")).toContain("background-size: contain");
  // Two classes (0,2,0) beat the single-class base picker mark (0,1,0).
  expect(css("harness-mark.css")).toMatch(/\.worker-harness-mark\s*\{/);
});
