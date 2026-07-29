import type { StructuredShortcut } from "../../persistence/contracts";
import { sanitizeCapturedUrl, type CapturedUrlResult } from "./url";

export const MAX_REALQA_PROCESS_URL_RULES = 64;
export const MAX_REALQA_SAFE_PATTERN_BYTES = 512;
export const MAX_REALQA_EXPANDED_URL_BYTES = 8_192;

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
 * nested/ambiguous repetition that causes catastrophic backtracking. Reject
 * unclosed groups here before translation can accidentally close them.
 */
function hasUnsafeRepeatedOrUnbalancedGroup(pattern: string): boolean {
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
      const flagDirective = pattern
        .slice(index)
        .match(/^\(\?[imsU]*(?:-[imsU]*)?\)/u);
      if (flagDirective !== null) {
        index += flagDirective[0].length - 1;
        continue;
      }
      groups.push({ containsAlternation: false, containsRepetition: false });
      const groupPrefix = pattern.slice(index).match(/^\(\?[imsU]*(?:-[imsU]*)?:/u);
      if (groupPrefix !== null) {
        index += groupPrefix[0].length - 1;
      } else if (pattern.startsWith("?:", index + 1)) {
        index += 2;
      }
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
  return groups.length > 0;
}

interface RegexFlags {
  readonly i: boolean;
  readonly m: boolean;
  readonly s: boolean;
  readonly U: boolean;
}

const defaultRegexFlags: RegexFlags = {
  i: false,
  m: false,
  s: false,
  U: false,
};

function updateRegexFlags(
  current: RegexFlags,
  enabled: string,
  disabled: string,
): RegexFlags {
  const next: Record<keyof RegexFlags, boolean> = { ...current };
  for (const flag of enabled) next[flag as keyof RegexFlags] = true;
  for (const flag of disabled) next[flag as keyof RegexFlags] = false;
  return next;
}

function wrapRegexModifiers(
  current: RegexFlags,
  next: RegexFlags,
  content: string,
): string {
  const enabled = ["i", "m", "s"].filter(
    (flag) =>
      !current[flag as keyof RegexFlags] &&
      next[flag as keyof RegexFlags],
  );
  const disabled = ["i", "m", "s"].filter(
    (flag) =>
      current[flag as keyof RegexFlags] &&
      !next[flag as keyof RegexFlags],
  );
  if (enabled.length === 0 && disabled.length === 0) return `(?:${content})`;
  const modifier = `${enabled.join("")}${
    disabled.length === 0 ? "" : `-${disabled.join("")}`
  }`;
  return `(?${modifier}:${content})`;
}

/**
 * Rust/Go inline flags can change for the rest of a group and include `U`.
 * JavaScript supports only scoped i/m/s modifier groups, so preserve those
 * scopes explicitly and implement ungreedy mode by reversing quantifier greed.
 */
function translateTitlePattern(pattern: string): string {
  function translateSequence(
    start: number,
    flags: RegexFlags,
    closesGroup: boolean,
  ): { readonly output: string; readonly nextIndex: number } {
    let output = "";
    let index = start;
    while (index < pattern.length) {
      const token = pattern[index] ?? "";
      if (token === ")" && closesGroup) {
        return { output, nextIndex: index + 1 };
      }
      if (token === "[") {
        const classStart = index;
        index += 1;
        while (index < pattern.length) {
          if (pattern[index] === "\\") {
            index += 2;
          } else if (pattern[index] === "]") {
            index += 1;
            break;
          } else {
            index += 1;
          }
        }
        output += pattern.slice(classStart, index);
        continue;
      }
      if (token === "\\") {
        const escaped = pattern[index + 1];
        output +=
          escaped === "A"
            ? "(?<![\\s\\S])"
            : escaped === "z"
              ? "(?![\\s\\S])"
              : pattern.slice(index, index + 2);
        index += 2;
        continue;
      }
      if (token === "(") {
        const inlineFlags = pattern
          .slice(index)
          .match(/^\(\?([imsU]*)(?:-([imsU]*))?([:)])/u);
        if (inlineFlags !== null) {
          const nextFlags = updateRegexFlags(
            flags,
            inlineFlags[1] ?? "",
            inlineFlags[2] ?? "",
          );
          index += inlineFlags[0].length;
          if (inlineFlags[3] === ":") {
            const inner = translateSequence(index, nextFlags, true);
            output += wrapRegexModifiers(flags, nextFlags, inner.output);
            index = inner.nextIndex;
            continue;
          }
          const remainder = translateSequence(index, nextFlags, closesGroup);
          output += wrapRegexModifiers(flags, nextFlags, remainder.output);
          return { output, nextIndex: remainder.nextIndex };
        }
        const nonCapturing = pattern.startsWith("(?:", index);
        index += nonCapturing ? 3 : 1;
        const inner = translateSequence(index, flags, true);
        output += `${nonCapturing ? "(?:" : "("}${inner.output})`;
        index = inner.nextIndex;
        continue;
      }
      const quantifierLength = repetitionLength(pattern, index);
      if (quantifierLength > 0) {
        const quantifier = pattern.slice(index, index + quantifierLength);
        const lazy = pattern[index + quantifierLength] === "?";
        output += flags.U ? `${quantifier}${lazy ? "" : "?"}` : quantifier;
        if (!flags.U && lazy) output += "?";
        index += quantifierLength + (lazy ? 1 : 0);
        continue;
      }
      output += token;
      index += 1;
    }
    return { output, nextIndex: index };
  }

  return translateSequence(0, defaultRegexFlags, false).output;
}

function compileTitlePattern(pattern: string): RegExp | null {
  try {
    return new RegExp(translateTitlePattern(pattern), "u");
  } catch {
    return null;
  }
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
      hasUnsafeRepeatedOrUnbalancedGroup(pattern)
    ) {
      return false;
    }
    return compileTitlePattern(pattern) !== null;
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
    let match: RegExpExecArray | null;
    if (rule.safeWindowTitlePattern === "") {
      match = [windowTitle] as unknown as RegExpExecArray;
    } else {
      const expression = compileTitlePattern(rule.safeWindowTitlePattern);
      if (expression === null) continue;
      match = expression.exec(windowTitle);
    }
    if (match === null) continue;
    const expanded = expandTemplate(rule.urlTemplate, match);
    if (new TextEncoder().encode(expanded).length > MAX_REALQA_EXPANDED_URL_BYTES) {
      continue;
    }
    const result = sanitizeCapturedUrl(expanded);
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
