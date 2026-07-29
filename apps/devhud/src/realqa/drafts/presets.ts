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
  if (rules.length > MAX_REALQA_PROCESS_URL_RULES) return false;
  return rules.every((rule) => {
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
      /\{\d{3,}(?:,\d*)?\}/u.test(pattern)
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
