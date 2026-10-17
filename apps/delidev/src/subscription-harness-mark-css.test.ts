// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { cascade, rulesFor } from "./css-test-rules";
import { subscriptionCatalog, SubscriptionBrand } from "./subscription-catalog";

it("contains the 24px harness artwork inside the 40px Settings slot without the picker basis or margin", () => {
  const slot = cascade((selector) => selector === ".subscription-mark");
  expect(slot.get("width")).toBe("40px");
  expect(slot.get("height")).toBe("40px");
  expect(slot.get("flex")).toBe("0 0 40px");
  expect(slot.get("align-items")).toBe("center");
  expect(slot.get("justify-content")).toBe("center");

  const base = cascade((selector) => selector === ".worker-harness-mark");
  // The base picker mark stays contained and centered; the override keeps it.
  expect(base.get("background-size")).toBe("contain");
  expect(base.get("background-position")).toBe("center");
  expect(base.get("flex")).toBe("0 0 48px");

  const scoped = cascade(
    (selector) => selector === ".subscription-mark .worker-harness-mark",
  );
  expect(scoped.get("flex")).toBe("0 0 24px");
  expect(scoped.get("width")).toBe("24px");
  expect(scoped.get("height")).toBe("24px");
  expect(scoped.get("margin")).toBe("0");
  // Two classes (0,2,0) outrank the single-class picker mark (0,1,0).
  expect(
    rulesFor(".subscription-mark .worker-harness-mark").every(
      (rule) => rule.selector.split(" ").length === 2,
    ),
  ).toBe(true);
  expect(rulesFor(".subscription-mark img")).toHaveLength(0);
  expect(rulesFor(".subscription-opencode-mark")).toHaveLength(0);
});

it("reuses the canonical explicit-theme and OS-fallback variants without recoloring", () => {
  const variant = (context: string[], selector: string) =>
    rulesFor(selector)
      .find((rule) => rule.context.join("|") === context.join("|"))
      ?.declarations.get("background-image");
  for (const [name, light, dark] of [
    ["grok-build", "grok-light.svg", "grok-dark.svg"],
    ["opencode", "opencode-light.svg", "opencode-dark.svg"],
  ]) {
    expect(variant([], `.worker-harness-mark-${name}`)).toContain(
      `harness-marks/${light}`,
    );
    expect(
      variant([], `:root[data-theme="dark"] .worker-harness-mark-${name}`),
    ).toContain(`harness-marks/${dark}`);
    expect(
      variant(
        ["@media (prefers-color-scheme: dark)"],
        `:root:not([data-theme]) .worker-harness-mark-${name}`,
      ),
    ).toContain(`harness-marks/${dark}`);
  }
  expect(variant([], ".worker-harness-mark-codex")).toContain("codex.webp");
  expect(variant([], ".worker-harness-mark-claude-code")).toContain(
    "claude-code.png",
  );
  // No filter or blend may recolor vendor artwork inside the Settings slot.
  const all = cascade((selector) => selector.includes("worker-harness-mark"));
  expect(all.has("filter")).toBe(false);
  expect(all.has("mix-blend-mode")).toBe(false);
});

it("keeps the rail and popover on the unchanged provider catalog artwork", () => {
  expect(
    Object.fromEntries(
      subscriptionCatalog.map((provider) => [provider.brand, provider.mark]),
    ),
  ).toEqual({
    [SubscriptionBrand.ChatGPT]: "./subscription-marks/openai.svg",
    [SubscriptionBrand.Claude]: "./subscription-marks/claude.svg",
    [SubscriptionBrand.Grok]: "./subscription-marks/grok.svg",
    [SubscriptionBrand.OpenCodeGo]: "./harness-marks/opencode-light.svg",
  });
});
