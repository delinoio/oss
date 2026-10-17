// SPDX-License-Identifier: Apache-2.0
// jsdom does not resolve selector specificity or width caps, so this guard
// reads the committed stylesheets. The generic Settings form rules cap every
// form at 720px; the encrypted Worker-network import form must keep the full
// width its controls had before the form wrapper was added.
import { describe, expect, it } from "vitest";
import { rulesFor } from "./css-test-rules";

const CAP = "max-width";

describe("worker network import form stylesheet", () => {
  const overrides = rulesFor("form.worker-network-import");

  it("overrides the generic 720px form cap with no width limit", () => {
    expect(overrides.length).toBeGreaterThan(0);
    for (const rule of overrides) {
      expect(rule.context).toEqual([]);
      expect(rule.declarations.get(CAP)).toBe("none");
    }
  });

  it("outranks both generic form cap selectors by extending their ancestor chains", () => {
    const selectors = overrides.flatMap((rule) =>
      rule.selector.split(",").map((selector) => selector.trim()),
    );
    // The generic caps are `.settings-content[aria-label] .settings-content-column
    // :is(form, ...)` (class-level argument) and `.settings-task-body :is(form,
    // ...)`. Each override repeats the same ancestors and ends with the more
    // specific `form.worker-network-import`, so specificity is strictly higher.
    expect(selectors).toContain(
      ".settings-content[aria-label] .settings-content-column form.worker-network-import",
    );
    expect(selectors).toContain(
      ".settings-task-body form.worker-network-import",
    );
  });

  it("still has the generic caps this override exists for", () => {
    const generic = rulesFor(".settings-content-column :is(").filter(
      (rule) =>
        rule.selector.includes("form,") &&
        rule.declarations.get(CAP) === "720px",
    );
    expect(generic.length).toBeGreaterThan(0);
  });
});
