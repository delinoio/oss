// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, it } from "vitest";

const css = readFileSync(
  join(process.cwd(), "src/claude-usage-tracking.css"),
  "utf8",
);

it("uses semantic tokens, 8px corners and a narrow-width action stack", () => {
  expect(css).not.toMatch(/#[\da-fA-F]{3,8}\b|rgba?\(|:\s*(?:white|black)\b/);
  expect(css).toMatch(/\.claude-usage \{[^}]*border-radius: 8px;/);
  expect(css).toMatch(/\.claude-usage-code input \{[^}]*min-height: 40px;/);
  // The section answers to its own width, so 640px, 320px and 200% zoom reflow
  // without a viewport breakpoint.
  expect(css).toMatch(/container-type: inline-size/);
  expect(css).toMatch(
    /@container \(width < 360px\) \{[^}]*\.claude-usage-actions \{[^}]*flex-direction: column;/s,
  );
  expect(css).toMatch(/overflow-wrap: anywhere/);
});
