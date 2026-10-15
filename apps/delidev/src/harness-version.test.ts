// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  CLAUDE_BASELINE_VERSION,
  HarnessId,
  claudeVersionAllowed,
  harnessVersionAllowed,
  validInstallationVersion,
  validSemVer,
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

it("validates full SemVer the way golang.org/x/mod/semver does", () => {
  for (const version of ["0.0.0", "1.0.46", "1.0.0-0", "1.0.0-rc.1+build.01"])
    expect(validSemVer(version), version).toBe(true);
  for (const version of [
    "01.0.0",
    "1.0.0-01",
    "1.0.0-",
    "1.0.0-a..b",
    "1.0.0+",
    "1.0.0+a..b",
    "1.0",
    "v1.0.0",
    "",
    undefined,
  ])
    expect(validSemVer(version), String(version)).toBe(false);
});

it("applies one minimum-version rule per harness to retained native records", () => {
  const cases: [HarnessId, string, string][] = [
    [HarnessId.Codex, "0.151.0", "0.150.9"],
    [HarnessId.Claude, "2.1.236", "2.1.235"],
    [HarnessId.OpenCode, "1.18.32", "1.18.31"],
    [HarnessId.Grok, "1.0.41", "1.0.40"],
  ];
  for (const [harness, baseline, lower] of cases) {
    const [major, minor, patch] = baseline.split(".").map(Number);
    for (const newer of [
      baseline,
      `${major}.${minor}.${patch + 1}`,
      `${major}.${minor + 1}.0`,
      `${major + 1}.0.0-rc.1`,
      `${baseline}+build.3`,
    ])
      expect(harnessVersionAllowed(harness, newer), `${harness} ${newer}`).toBe(
        true,
      );
    for (const rejected of [
      lower,
      `${baseline}-rc.1`,
      "unknown",
      "",
      undefined,
    ])
      expect(
        harnessVersionAllowed(harness, rejected),
        `${harness} ${String(rejected)}`,
      ).toBe(false);
  }
  expect(harnessVersionAllowed("not-a-harness", "9.9.9")).toBe(false);
  expect(harnessVersionAllowed(undefined, "9.9.9")).toBe(false);
});
