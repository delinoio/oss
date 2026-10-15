// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const css = readFileSync(
  join(process.cwd(), "src", "styles.css"),
  "utf8",
).replace(/\/\*[\s\S]*?\*\//g, "");
function block(selector: string, container = css) {
  const start = container.indexOf(`${selector} {`);
  expect(start, selector).toBeGreaterThanOrEqual(0);
  return container.slice(
    container.indexOf("{", start) + 1,
    container.indexOf("}", start),
  );
}

it("defines danger tokens for light, explicit dark and system dark", () => {
  expect(block(":root")).toContain("--danger-text: #b42332;");
  expect(block(":root")).toContain("--danger-background: #fff3f4;");
  const system = css.slice(css.indexOf("@media (prefers-color-scheme: dark)"));
  expect(block(':root:not([data-theme="light"])', system)).toContain(
    "--danger-text: #ffb6bf;",
  );
  expect(block(':root:not([data-theme="light"])', system)).toContain(
    "--danger-background: #392127;",
  );
  expect(block(':root[data-theme="dark"]')).toContain(
    "--danger-text: #ffb6bf;",
  );
  expect(block(':root[data-theme="dark"]')).toContain(
    "--danger-background: #392127;",
  );
  // An explicit light choice keeps the light tokens from :root.
  expect(block(':root[data-theme="light"]')).not.toContain("--danger");
});
it("styles only the semantic marker with a transparent outlined button and a pale hover", () => {
  const base = block(':root button[data-action-tone="danger"]');
  expect(base).toContain("color: var(--danger-text);");
  expect(base).toContain("border-color: var(--danger-text);");
  expect(base).toContain("background: transparent;");
  expect(
    block(':root button[data-action-tone="danger"]:hover:not(:disabled)'),
  ).toContain("background: var(--danger-background);");
  // Geometry, attenuation and focus stay with the shared button rules.
  for (const forbidden of ["min-height", "padding", "opacity", "outline"])
    expect(base).not.toContain(forbidden);
  expect(css).not.toMatch(/data-action-tone="danger"\][^{]*(svg|aria-label)/);
});
it("keeps the shared button geometry, disabled attenuation and focus outline", () => {
  expect(block("button")).toContain("border-radius: 6px;");
  expect(css).toMatch(
    /button,\s*input,\s*select,\s*summary \{\s*min-height: 48px;/,
  );
  expect(block("button:disabled")).toContain("opacity: 0.55;");
  expect(css).toContain("outline: 3px solid var(--focus);");
});
