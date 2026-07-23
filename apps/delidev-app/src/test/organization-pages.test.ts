import {
  OrganizationRole,
  type Team,
} from "@delinoio/delibase-connect";
import { describe, expect, it } from "vitest";

import {
  canCreateChildTeam,
  canManageOrganization,
  canUseTeamAsParent,
  formatOptionalUsdMicros,
  formatUsageCost,
  formatUsageUnits,
  getEditableOverageLimit,
  parseUsdMicros,
} from "../pages/OrganizationPages";

function team(
  id: string,
  parentId?: string,
  depth = parentId ? 1 : 0,
): Team {
  return {
    $typeName: "delibase.v1.Team",
    createdAt: undefined,
    depth,
    name: id,
    organizationId: { $typeName: "delibase.v1.UuidV7", value: "org" },
    parentTeamId: parentId
      ? { $typeName: "delibase.v1.UuidV7", value: parentId }
      : undefined,
    protectedGeneral: false,
    teamId: { $typeName: "delibase.v1.UuidV7", value: id },
    updatedAt: undefined,
  };
}

describe("organization billing inputs", () => {
  it("converts exact USD input to signed 64-bit micro-units", () => {
    expect(parseUsdMicros("0")).toBe(0n);
    expect(parseUsdMicros("12.345678")).toBe(12_345_678n);
    expect(parseUsdMicros("9223372036854.775807")).toBe(
      9_223_372_036_854_775_807n,
    );
  });

  it("rejects negative, over-precise, and overflowing limits", () => {
    expect(parseUsdMicros("-1")).toBeUndefined();
    expect(parseUsdMicros("1.0000001")).toBeUndefined();
    expect(parseUsdMicros("9223372036854.775808")).toBeUndefined();
  });

  it("limits organization management to owners and admins", () => {
    expect(canManageOrganization(OrganizationRole.OWNER)).toBe(true);
    expect(canManageOrganization(OrganizationRole.ADMIN)).toBe(true);
    expect(canManageOrganization(OrganizationRole.MEMBER)).toBe(false);
    expect(canManageOrganization(OrganizationRole.UNSPECIFIED)).toBe(false);
  });

  it("distinguishes missing usage wrappers from explicit zero values", () => {
    expect(formatUsageUnits(undefined)).toBe("Unavailable");
    expect(formatUsageCost(undefined)).toBe("Unavailable");
    expect(formatUsageUnits(0n)).toBe("0");
    expect(formatUsageCost(0n)).toBe("$0.0000");
  });

  it("does not edit a configured overage limit when its wrapper is missing", () => {
    expect(getEditableOverageLimit(true, undefined)).toBeUndefined();
    expect(getEditableOverageLimit(true, 0n)).toBe(0n);
    expect(getEditableOverageLimit(false, undefined)).toBe(0n);
  });

  it("distinguishes missing billing balances from explicit zero values", () => {
    expect(formatOptionalUsdMicros(undefined)).toBe("Unavailable");
    expect(formatOptionalUsdMicros(0n)).toBe("$0.0000");
  });
});

describe("team hierarchy controls", () => {
  it("excludes level-five teams from create-team parent choices", () => {
    expect(canCreateChildTeam(team("level-four", "parent", 3))).toBe(true);
    expect(canCreateChildTeam(team("level-five", "parent", 4))).toBe(false);
  });

  it("excludes the current team and descendants from move targets", () => {
    const parent = team("parent");
    const child = team("child", "parent");
    const grandchild = team("grandchild", "child");
    const sibling = team("sibling");
    const teams = [parent, child, grandchild, sibling];

    expect(canUseTeamAsParent(parent, parent, teams)).toBe(false);
    expect(canUseTeamAsParent(parent, grandchild, teams)).toBe(false);
    expect(canUseTeamAsParent(parent, sibling, teams)).toBe(true);
  });

  it("excludes move targets that would make the subtree too deep", () => {
    const root = team("root");
    const moving = team("moving");
    const child = team("child", "moving", 1);
    const grandchild = team("grandchild", "child", 2);
    const levelThree = team("level-three", "root", 2);
    const levelFour = team("level-four", "level-three", 3);
    const teams = [root, moving, child, grandchild, levelThree, levelFour];

    expect(canUseTeamAsParent(moving, root, teams)).toBe(true);
    expect(canUseTeamAsParent(moving, levelThree, teams)).toBe(false);
    expect(canUseTeamAsParent(moving, levelFour, teams)).toBe(false);
  });
});
