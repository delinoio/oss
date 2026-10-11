// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { PresentationLabels } from "./product-presentation";
import { en } from "./localization";
it("preserves original name bytes and connection-owned numbering", () => {
  const first = "019c0000-0000-7000-8000-000000000001",
    second = "019c0000-0000-7000-8000-000000000002";
  const owner = new PresentationLabels();
  const c = en;
  owner.observe(first, "Same");
  owner.observe(second, "Same");
  expect(owner.name(first, "Same", c.project, c)).toBe(`Same · ${c.project} 1`);
  expect(owner.name(second, "Same", c.project, c)).toBe(
    `Same · ${c.project} 2`,
  );
  expect(owner.name(first, first, c.project, c)).toBe(first);
  expect(owner.name(first, "", c.project, c)).toBe(
    `${c.project} 1 · ${c.nameUnavailable}`,
  );
  expect(owner.identity(second, c.project)).toBe(`${c.project} 2`);
});
