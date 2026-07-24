import { describe, expect, it } from "vitest";

import {
  defineTool,
  filterTools,
  productionTools,
  ToolCapability,
  ToolPlatform,
} from "./registry";

function FixtureEntryPoint() {
  return null;
}

const desktopFixture = defineTool({
  toolId: "fixture-diagnostics",
  name: "Fixture diagnostics",
  description: "A test-only desktop tool.",
  searchKeywords: ["fixture", "diagnostics"],
  supportedPlatforms: new Set([ToolPlatform.Desktop]),
  requiredCapabilities: new Set([ToolCapability.Diagnostics]),
  EntryPoint: FixtureEntryPoint,
});

describe("internal tool registry", () => {
  it("keeps production registration empty", () => {
    expect(productionTools).toEqual([]);
  });

  it("filters fixture definitions by platform and granted capabilities", () => {
    expect(
      filterTools([desktopFixture], {
        platform: ToolPlatform.Desktop,
        grantedCapabilities: new Set([ToolCapability.Diagnostics]),
      }),
    ).toEqual([desktopFixture]);
    expect(
      filterTools([desktopFixture], {
        platform: ToolPlatform.Ios,
        grantedCapabilities: new Set([ToolCapability.Diagnostics]),
      }),
    ).toEqual([]);
    expect(
      filterTools([desktopFixture], {
        platform: ToolPlatform.Desktop,
        grantedCapabilities: new Set(),
      }),
    ).toEqual([]);
  });

  it("rejects an invalid tool identifier", () => {
    expect(() =>
      defineTool({ ...desktopFixture, toolId: "Fixture Diagnostics" }),
    ).toThrow("lowercase kebab-case");
  });
});
