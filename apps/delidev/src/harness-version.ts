// SPDX-License-Identifier: Apache-2.0

/**
 * Pinned Claude Code baseline. It mirrors domain.ClaudeProtocolVersion in the Go
 * module and is a minimum SemVer precedence, never an exact version.
 */
export const CLAUDE_BASELINE_VERSION = "2.1.236";
/** Mirrors domain.CodexMinimumVersion; a minimum, never an exact version. */
export const CODEX_BASELINE_VERSION = "0.151.0";
/** Mirrors domain.OpenCodeProtocolVersion; a minimum, never an exact version. */
export const OPENCODE_BASELINE_VERSION = "1.18.32";
/** Mirrors domain.GrokProtocolVersion; a minimum, never an exact version. */
export const GROK_BASELINE_VERSION = "1.0.41";

/** Stable harness identifiers shared with the Go Harness enum. */
export enum HarnessId {
  Codex = "codex",
  Claude = "claude-code",
  OpenCode = "opencode",
  Grok = "grok-build",
}

const installationVersion =
  /^\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]{1,64})?(?:\+[a-zA-Z0-9.-]{1,64})?$/;
const strictSemVer =
  /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+[0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*)?$/;

/** Mirrors domain.ValidInstallationVersion: bounded stable or prerelease SemVer. */
export function validInstallationVersion(value: unknown): value is string {
  return (
    typeof value === "string" &&
    value.length <= 256 &&
    installationVersion.test(value)
  );
}

/**
 * Mirrors golang.org/x/mod/semver.IsValid for a version without the leading
 * "v": no leading zeros in numeric components or numeric prerelease
 * identifiers and no empty prerelease or build identifiers.
 */
export function validSemVer(value: unknown): value is string {
  return typeof value === "string" && strictSemVer.test(value);
}

function parts(value: string) {
  const match = strictSemVer.exec(value);
  if (!match) return undefined;
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    prerelease: match[4] ? match[4].split(".") : [],
  };
}

function comparePrerelease(a: string[], b: string[]): number {
  // A release outranks any prerelease of the same core version.
  if (!a.length || !b.length) return a.length ? -1 : b.length ? 1 : 0;
  for (let i = 0; i < Math.max(a.length, b.length); i += 1) {
    if (a[i] === undefined) return -1;
    if (b[i] === undefined) return 1;
    const left = /^\d+$/.test(a[i]),
      right = /^\d+$/.test(b[i]);
    if (left && right) {
      const delta = Number(a[i]) - Number(b[i]);
      if (delta) return delta < 0 ? -1 : 1;
    } else if (left !== right) {
      return left ? -1 : 1;
    } else if (a[i] !== b[i]) {
      return a[i] < b[i] ? -1 : 1;
    }
  }
  return 0;
}

/**
 * SemVer precedence check mirroring domain.VersionAtLeast. Build metadata is
 * ignored and malformed versions are never admitted. Admission only allows the
 * real native checks to run; it never establishes protocol readiness.
 */
export function versionAtLeast(version: unknown, baseline: string): boolean {
  if (!validInstallationVersion(version)) return false;
  const a = parts(version),
    b = parts(baseline);
  if (!a || !b) return false;
  for (let i = 0; i < 3; i += 1)
    if (a.core[i] !== b.core[i]) return a.core[i] > b.core[i];
  return comparePrerelease(a.prerelease, b.prerelease) >= 0;
}

/** Eligibility rule shared by Claude Runner selection and observations. */
export function claudeVersionAllowed(version: unknown): boolean {
  return versionAtLeast(version, CLAUDE_BASELINE_VERSION);
}

/**
 * Version predicate applied to a Settings installation observation before any
 * protocol state is evaluated. Claude Code and OpenCode report an under-baseline
 * detected version as a version problem; other harnesses keep protocol-only
 * classification.
 */
export function installationVersionAllowed(
  harness: unknown,
): ((version: string) => boolean) | undefined {
  switch (harness) {
    case HarnessId.Claude:
      return claudeVersionAllowed;
    case HarnessId.OpenCode:
      return (version) => harnessVersionAllowed(HarnessId.OpenCode, version);
    default:
      return undefined;
  }
}

/**
 * The single per-harness version rule for renderer validators of retained
 * native records (interactions, usage, subagents): valid SemVer precedence at
 * or above the harness baseline. It mirrors domain.HarnessVersionAllowed for
 * Claude, OpenCode and Grok and applies the same minimum to Codex
 * (domain.CodexMinimumVersion). Never compare a record version for equality
 * with a baseline constant.
 */
export function harnessVersionAllowed(
  harness: unknown,
  version: unknown,
): boolean {
  switch (harness) {
    case HarnessId.Codex:
      return versionAtLeast(version, CODEX_BASELINE_VERSION);
    case HarnessId.Claude:
      return versionAtLeast(version, CLAUDE_BASELINE_VERSION);
    case HarnessId.OpenCode:
      return versionAtLeast(version, OPENCODE_BASELINE_VERSION);
    case HarnessId.Grok:
      return versionAtLeast(version, GROK_BASELINE_VERSION);
    default:
      return false;
  }
}
