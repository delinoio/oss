// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  CLAUDE_BASELINE_VERSION,
  claudeVersionAllowed,
  validInstallationVersion,
  versionAtLeast,
} from "./harness-version";

it("admits SemVer precedence at or above the Claude baseline", () => {
  for (const version of [
    "2.1.236",
    "2.1.237",
    "2.2.0",
    "10.0.0",
    "3.0.0-rc.1",
    "2.1.237-beta.2",
    "2.1.236+build.5",
  ])
    expect(claudeVersionAllowed(version), version).toBe(true);
  for (const version of [
    "2.1.235",
    "1.99.99",
    "2.1.236-rc.1",
    "2.1",
    "v2.1.237",
    "02.1.237",
    "",
    "not-a-version",
    undefined,
    2.1,
  ])
    expect(claudeVersionAllowed(version), String(version)).toBe(false);
  expect(CLAUDE_BASELINE_VERSION).toBe("2.1.236");
});

it("orders prerelease identifiers by SemVer precedence and ignores build metadata", () => {
  expect(versionAtLeast("1.0.0-alpha.1", "1.0.0-alpha")).toBe(true);
  expect(versionAtLeast("1.0.0-alpha", "1.0.0-alpha.1")).toBe(false);
  expect(versionAtLeast("1.0.0-beta", "1.0.0-alpha.9")).toBe(true);
  expect(versionAtLeast("1.0.0-alpha.beta", "1.0.0-alpha.1")).toBe(true);
  expect(versionAtLeast("1.0.0-2", "1.0.0-11")).toBe(false);
  expect(versionAtLeast("1.0.0", "1.0.0-rc.1")).toBe(true);
  expect(versionAtLeast("1.0.0-rc.1", "1.0.0")).toBe(false);
  expect(versionAtLeast("1.0.0+a", "1.0.0+b")).toBe(true);
  expect(versionAtLeast("1.0.0", "bogus")).toBe(false);
});

it("accepts the same bounded detected-version shape as the Go side", () => {
  expect(validInstallationVersion("2.1.237-rc.1+build.7")).toBe(true);
  expect(validInstallationVersion("2.1")).toBe(false);
  expect(validInstallationVersion(`1.0.0-${"a".repeat(65)}`)).toBe(false);
});
