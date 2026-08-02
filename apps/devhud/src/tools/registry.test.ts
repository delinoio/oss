import { describe, expect, it } from "vitest";

import {
  defineTool,
  filterTools,
  productionTools,
  ToolCapability,
  ToolOperatingSystem,
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
  supportedOperatingSystems: new Set([ToolOperatingSystem.Ubuntu]),
  requiredCapabilities: new Set([ToolCapability.Diagnostics]),
  EntryPoint: FixtureEntryPoint,
});

const realQaFixture = defineTool({
  toolId: "fixture-realqa",
  name: "Fixture RealQA",
  description: "A test-only capture and composer tool.",
  searchKeywords: ["fixture", "capture"],
  supportedPlatforms: new Set([ToolPlatform.Desktop]),
  supportedOperatingSystems: new Set([ToolOperatingSystem.Ubuntu]),
  requiredCapabilities: new Set([
    ToolCapability.RealQaCapture,
    ToolCapability.RealQaComposer,
  ]),
  EntryPoint: FixtureEntryPoint,
});

describe("internal tool registry", () => {
  it("registers RealQA only for supported desktop operating systems", () => {
    expect(productionTools.map((tool) => tool.toolId)).toEqual(["realqa"]);
    for (const operatingSystem of Object.values(ToolOperatingSystem)) {
      expect(
        filterTools(productionTools, {
          platform: ToolPlatform.Desktop,
          operatingSystem,
          grantedCapabilities: new Set([ToolCapability.WindowControl]),
        }).map((tool) => tool.toolId),
      ).toEqual(["realqa"]);
    }
    expect(
      filterTools(productionTools, {
        platform: ToolPlatform.Desktop,
        operatingSystem: null,
        grantedCapabilities: new Set([ToolCapability.WindowControl]),
      }),
    ).toEqual([]);
    for (const platform of [ToolPlatform.Ios, ToolPlatform.Android]) {
      expect(
        filterTools(productionTools, {
          platform,
          operatingSystem: null,
          grantedCapabilities: new Set([ToolCapability.WindowControl]),
        }),
      ).toEqual([]);
    }
  });

  it("filters fixture definitions by platform and granted capabilities", () => {
    expect(
      filterTools([desktopFixture], {
        platform: ToolPlatform.Desktop,
        operatingSystem: ToolOperatingSystem.Ubuntu,
        grantedCapabilities: new Set([ToolCapability.Diagnostics]),
      }),
    ).toEqual([desktopFixture]);
    expect(
      filterTools([desktopFixture], {
        platform: ToolPlatform.Ios,
        operatingSystem: null,
        grantedCapabilities: new Set([ToolCapability.Diagnostics]),
      }),
    ).toEqual([]);
    expect(
      filterTools([desktopFixture], {
        platform: ToolPlatform.Desktop,
        operatingSystem: ToolOperatingSystem.Ubuntu,
        grantedCapabilities: new Set(),
      }),
    ).toEqual([]);
  });

  it("rejects an invalid tool identifier", () => {
    expect(() =>
      defineTool({ ...desktopFixture, toolId: "Fixture Diagnostics" }),
    ).toThrow("lowercase kebab-case");
  });

  it("requires both disjoint RealQA window capabilities", () => {
    for (const grantedCapabilities of [
      new Set<ToolCapability>(),
      new Set([ToolCapability.RealQaCapture]),
      new Set([ToolCapability.RealQaComposer]),
    ]) {
      expect(
        filterTools([realQaFixture], {
          platform: ToolPlatform.Desktop,
          operatingSystem: ToolOperatingSystem.Ubuntu,
          grantedCapabilities,
        }),
      ).toEqual([]);
    }
    expect(
      filterTools([realQaFixture], {
        platform: ToolPlatform.Desktop,
        operatingSystem: ToolOperatingSystem.Ubuntu,
        grantedCapabilities: new Set([
          ToolCapability.RealQaCapture,
          ToolCapability.RealQaComposer,
        ]),
      }),
    ).toEqual([realQaFixture]);
  });
});
