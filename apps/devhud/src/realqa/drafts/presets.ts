import type { StructuredShortcut } from "../../persistence/contracts";
import { sanitizeCapturedUrl, type CapturedUrlResult } from "./url";

export const MAX_REALQA_PROCESS_URL_RULES = 64;
export const MAX_REALQA_SAFE_PATTERN_BYTES = 512;

export interface RealQaProcessUrlRule {
  readonly ruleId: string;
  readonly exactProcessName: string;
  readonly safeWindowTitlePattern: string;
  readonly urlTemplate: string;
  readonly enabled: boolean;
}

export interface SynchronizedRealQaDestination {
  readonly destinationId: string;
  readonly provider: "github";
  readonly installationId: string;
  readonly repository: {
    readonly repositoryId: string;
    readonly owner: string;
    readonly name: string;
  };
}

export interface SynchronizedRealQaRepositoryDefinition {
  readonly definitionId: string;
  readonly kind: "issue-form" | "markdown-template";
  readonly name: string;
  readonly relativePath: string;
  readonly revision: string;
}

export interface SynchronizedRealQaPreset {
  readonly presetId: string;
  readonly revision: number;
  readonly destination: SynchronizedRealQaDestination;
  readonly repositoryDefinition: SynchronizedRealQaRepositoryDefinition | null;
  readonly processUrlRules: readonly RealQaProcessUrlRule[];
  readonly shortcut: {
    readonly shortcutId: string;
    readonly accelerator: StructuredShortcut;
    readonly active: boolean;
  } | null;
}

/** Device-owned values are never accepted from preset synchronization. */
export interface RealQaDeviceState {
  readonly capturePermission: "unknown" | "prompt-required" | "granted" | "denied";
  readonly shortcutRegistrations: Readonly<Record<string, "active" | "inactive">>;
  readonly extensionPairing: "unpaired" | "paired" | "locked";
}

const forbiddenPatternFragments = [
  "(?=",
  "(?!",
  "(?<=",
  "(?<!",
  "(?P<",
  "(?<",
  String.raw`\k`,
  String.raw`\g`,
  String.raw`\C`,
  String.raw`\Q`,
  String.raw`\E`,
  "&&",
  "--",
  "~~",
  "(?x",
  "(?x:",
];

interface PatternGroupState {
  containsAlternation: boolean;
  containsRepetition: boolean;
}

function repetitionLength(pattern: string, index: number): number {
  const token = pattern[index];
  if (token === "*" || token === "+" || token === "?") return 1;
  if (token !== "{") return 0;
  return pattern.slice(index).match(/^\{\d+(?:,\d*)?\}/u)?.[0].length ?? 0;
}

/**
 * JavaScript RegExp uses a backtracking engine. Reject repeated groups whose
 * contents can repeat or branch, so synchronized input cannot introduce the
 * nested/ambiguous repetition that causes catastrophic backtracking.
 */
function hasUnsafeRepeatedGroup(pattern: string): boolean {
  const groups: PatternGroupState[] = [];
  let inCharacterClass = false;
  for (let index = 0; index < pattern.length; index += 1) {
    const token = pattern[index];
    if (token === "\\") {
      index += 1;
      continue;
    }
    if (token === "[") {
      inCharacterClass = true;
      continue;
    }
    if (token === "]" && inCharacterClass) {
      inCharacterClass = false;
      continue;
    }
    if (inCharacterClass) continue;
    if (token === "(") {
      groups.push({ containsAlternation: false, containsRepetition: false });
      if (pattern.startsWith("?:", index + 1)) index += 2;
      continue;
    }
    if (token === "|") {
      const current = groups.at(-1);
      if (current !== undefined) current.containsAlternation = true;
      continue;
    }
    if (token === ")") {
      const group = groups.pop();
      if (group === undefined) continue;
      const repeatedBy = repetitionLength(pattern, index + 1);
      if (
        repeatedBy > 0 &&
        (group.containsAlternation || group.containsRepetition)
      ) {
        return true;
      }
      const parent = groups.at(-1);
      if (parent !== undefined) {
        parent.containsAlternation ||= group.containsAlternation;
        parent.containsRepetition ||= group.containsRepetition || repeatedBy > 0;
      }
      index += repeatedBy;
      continue;
    }
    const repeatedBy = repetitionLength(pattern, index);
    if (repeatedBy > 0) {
      const current = groups.at(-1);
      if (current !== undefined) current.containsRepetition = true;
      index += repeatedBy - 1;
    }
  }
  return false;
}

function validTemplate(template: string): boolean {
  if (template.length === 0 || new TextEncoder().encode(template).length > 2_048) {
    return false;
  }
  const probe = template.replace(/\$(?:\{[0-9]+\}|[0-9]+)/gu, "x");
  const sanitized = sanitizeCapturedUrl(probe);
  return sanitized.ok;
}

export function validateRealQaProcessUrlRules(
  rules: readonly RealQaProcessUrlRule[],
): boolean {
  const enabledRules = rules.filter((rule) => rule.enabled);
  if (enabledRules.length > MAX_REALQA_PROCESS_URL_RULES) return false;
  return enabledRules.every((rule) => {
    if (
      rule.exactProcessName.length === 0 ||
      new TextEncoder().encode(rule.exactProcessName).length > 255 ||
      !validTemplate(rule.urlTemplate)
    ) {
      return false;
    }
    const pattern = rule.safeWindowTitlePattern;
    if (pattern === "") return true;
    if (
      new TextEncoder().encode(pattern).length > MAX_REALQA_SAFE_PATTERN_BYTES ||
      forbiddenPatternFragments.some((fragment) => pattern.includes(fragment)) ||
      /\\[0-9]/u.test(pattern) ||
      /\{\d{3,}(?:,\d*)?\}/u.test(pattern) ||
      hasUnsafeRepeatedGroup(pattern)
    ) {
      return false;
    }
    try {
      void new RegExp(pattern, "u");
      return true;
    } catch {
      return false;
    }
  });
}

function expandTemplate(template: string, match: RegExpExecArray): string {
  return template.replace(/\$(?:\{([0-9]+)\}|([0-9]+))/gu, (_whole, braced, plain) => {
    const index = Number(braced ?? plain);
    return match[index] ?? "";
  });
}

/**
 * Evaluates declaration order exactly. A malformed expansion is skipped so a
 * later safe fallback may match; no match deliberately returns a blank URL.
 */
export function inferDesktopUrl(
  rules: readonly RealQaProcessUrlRule[],
  processName: string,
  windowTitle: string,
): CapturedUrlResult | null {
  if (!validateRealQaProcessUrlRules(rules)) {
    return { ok: false, reason: "invalid-url" };
  }
  for (const rule of rules) {
    if (!rule.enabled || rule.exactProcessName !== processName) continue;
    const match =
      rule.safeWindowTitlePattern === ""
        ? ([windowTitle] as unknown as RegExpExecArray)
        : new RegExp(rule.safeWindowTitlePattern, "u").exec(windowTitle);
    if (match === null) continue;
    const result = sanitizeCapturedUrl(expandTemplate(rule.urlTemplate, match));
    if (result.ok) return result;
  }
  return null;
}

export function inferChromeUrl(activeTabUrl: string): CapturedUrlResult {
  return sanitizeCapturedUrl(activeTabUrl);
}

export function applySynchronizedPreset(
  currentDeviceState: RealQaDeviceState,
  preset: SynchronizedRealQaPreset,
): {
  readonly preset: SynchronizedRealQaPreset;
  readonly deviceState: RealQaDeviceState;
} {
  if (!validateRealQaProcessUrlRules(preset.processUrlRules)) {
    throw new Error("RealQA preset contains invalid process/title URL rules.");
  }
  return { preset, deviceState: currentDeviceState };
}
