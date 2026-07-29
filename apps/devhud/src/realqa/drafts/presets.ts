import type { StructuredShortcut } from "../../persistence/contracts";
import { sanitizeCapturedUrl, type CapturedUrlResult } from "./url";

export const MAX_REALQA_PROCESS_URL_RULES = 64;
export const MAX_REALQA_SAFE_PATTERN_BYTES = 512;
export const MAX_REALQA_EXPANDED_URL_BYTES = 8_192;
const MAX_REALQA_COMPILED_PATTERN_INSTRUCTIONS = 2_048;

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
  "(?x",
  "(?x:",
];

interface PatternGroupState {
  containsAlternation: boolean;
  containsRepetition: boolean;
}

const posixCharacterClassRanges = {
  alnum: [
    [0x30, 0x39],
    [0x41, 0x5a],
    [0x61, 0x7a],
  ],
  alpha: [
    [0x41, 0x5a],
    [0x61, 0x7a],
  ],
  ascii: [[0x00, 0x7f]],
  blank: [
    [0x09, 0x09],
    [0x20, 0x20],
  ],
  cntrl: [
    [0x00, 0x1f],
    [0x7f, 0x7f],
  ],
  digit: [[0x30, 0x39]],
  graph: [[0x21, 0x7e]],
  lower: [[0x61, 0x7a]],
  print: [[0x20, 0x7e]],
  punct: [
    [0x21, 0x2f],
    [0x3a, 0x40],
    [0x5b, 0x60],
    [0x7b, 0x7e],
  ],
  space: [
    [0x09, 0x0d],
    [0x20, 0x20],
  ],
  upper: [[0x41, 0x5a]],
  word: [
    [0x30, 0x39],
    [0x41, 0x5a],
    [0x5f, 0x5f],
    [0x61, 0x7a],
  ],
  xdigit: [
    [0x30, 0x39],
    [0x41, 0x46],
    [0x61, 0x66],
  ],
} as const;

type CharacterRange = readonly [number, number];

const goPerlWhitespaceRanges: readonly CharacterRange[] = [
  [0x09, 0x0a],
  [0x0c, 0x0d],
  [0x20, 0x20],
];

function complementCharacterRanges(
  ranges: readonly CharacterRange[],
): readonly CharacterRange[] {
  const complement: CharacterRange[] = [];
  let start = 0;
  for (const [rangeStart, rangeEnd] of ranges) {
    if (start < rangeStart) complement.push([start, rangeStart - 1]);
    start = rangeEnd + 1;
  }
  if (start <= 0x10ffff) complement.push([start, 0x10ffff]);
  return complement;
}

function escapeCharacterClassCodePoint(codePoint: number): string {
  return codePoint <= 0xff
    ? String.raw`\x${codePoint.toString(16).padStart(2, "0")}`
    : String.raw`\u{${codePoint.toString(16)}}`;
}

function renderCharacterRanges(ranges: readonly CharacterRange[]): string {
  return ranges
    .map(([start, end]) => {
      const first = escapeCharacterClassCodePoint(start);
      return start === end
        ? first
        : `${first}-${escapeCharacterClassCodePoint(end)}`;
    })
    .join("");
}

function characterRangesContain(
  ranges: readonly CharacterRange[],
  codePoint: number,
): boolean {
  return ranges.some(([start, end]) => codePoint >= start && codePoint <= end);
}

/**
 * JavaScript /iu adds Unicode simple-fold equivalents to ASCII ranges. Close
 * the excluded set before complementing it so negated POSIX classes keep Go's
 * case-insensitive semantics when rendered as a positive JavaScript range.
 */
function closeAsciiRangesUnderUnicodeCaseFolding(
  ranges: readonly CharacterRange[],
): readonly CharacterRange[] {
  const expanded: CharacterRange[] = [...ranges];
  for (let upper = 0x41; upper <= 0x5a; upper += 1) {
    const lower = upper + 0x20;
    if (
      characterRangesContain(ranges, upper) ||
      characterRangesContain(ranges, lower)
    ) {
      expanded.push([upper, upper], [lower, lower]);
    }
  }
  if (
    characterRangesContain(ranges, 0x4b) ||
    characterRangesContain(ranges, 0x6b)
  ) {
    expanded.push([0x212a, 0x212a]);
  }
  if (
    characterRangesContain(ranges, 0x53) ||
    characterRangesContain(ranges, 0x73)
  ) {
    expanded.push([0x017f, 0x017f]);
  }
  const merged: CharacterRange[] = [];
  for (const [start, end] of expanded.toSorted(
    ([left], [right]) => left - right,
  )) {
    const previous = merged.at(-1);
    if (previous !== undefined && start <= previous[1] + 1) {
      merged[merged.length - 1] = [previous[0], Math.max(previous[1], end)];
    } else {
      merged.push([start, end]);
    }
  }
  return merged;
}

function translatePosixCharacterClass(
  pattern: string,
  index: number,
  caseInsensitive = false,
): { readonly output: string; readonly nextIndex: number } | null {
  const posixClass = pattern
    .slice(index)
    .match(
      /^\[:(\^?)(alnum|alpha|ascii|blank|cntrl|digit|graph|lower|print|punct|space|upper|word|xdigit):\]/u,
    );
  if (posixClass === null) return null;
  const ranges =
    posixCharacterClassRanges[
      posixClass[2] as keyof typeof posixCharacterClassRanges
    ];
  const negated = posixClass[1] === "^";
  return {
    output: renderCharacterRanges(
      negated
        ? complementCharacterRanges(
            caseInsensitive
              ? closeAsciiRangesUnderUnicodeCaseFolding(ranges)
              : ranges,
          )
        : ranges,
    ),
    nextIndex: index + posixClass[0].length,
  };
}

function repetitionLength(pattern: string, index: number): number {
  const token = pattern[index];
  if (token === "*" || token === "+" || token === "?") return 1;
  if (token !== "{") return 0;
  return pattern.slice(index).match(/^\{\d+(?:,\d*)?\}/u)?.[0].length ?? 0;
}

function translateUnicodeClassEscape(
  pattern: string,
  index: number,
  caseInsensitive: boolean,
  insideCharacterClass: boolean,
): { readonly output: string; readonly nextIndex: number } | null {
  const unicodeClass = pattern
    .slice(index)
    .match(/^\\([pP])(?:\{(\^?)([A-Za-z_]+)\}|([A-Za-z]))/u);
  if (unicodeClass === null) return null;
  const name = unicodeClass[3] ?? unicodeClass[4] ?? "";
  const classKind =
    unicodeClass[2] === "^"
      ? unicodeClass[1] === "p"
        ? "P"
        : "p"
      : unicodeClass[1];
  const positive = `\\p{${name}}`;
  let property = name;
  try {
    new RegExp(positive, "u");
  } catch {
    property = `Script=${name}`;
  }
  const direct = `\\${classKind}{${property}}`;
  return {
    output:
      classKind === "P" && caseInsensitive && !insideCharacterClass
        ? `[^\\p{${property}}]`
        : direct,
    nextIndex: index + unicodeClass[0].length,
  };
}

function translateGoCharacterEscape(
  pattern: string,
  index: number,
): { readonly output: string; readonly nextIndex: number } | null {
  if (pattern.startsWith(String.raw`\a`, index)) {
    return { output: String.raw`\x07`, nextIndex: index + 2 };
  }
  const bracedHex = pattern.slice(index).match(/^\\x\{([0-9A-Fa-f]+)\}/u);
  if (bracedHex !== null) {
    return {
      output: `\\u{${bracedHex[1]}}`,
      nextIndex: index + bracedHex[0].length,
    };
  }
  if (pattern[index + 1] === "u" || pattern[index + 1] === "c") {
    throw new Error("JavaScript-only escape");
  }
  return null;
}

function translatePerlWhitespaceClassEscape(
  pattern: string,
  index: number,
  insideCharacterClass: boolean,
): { readonly output: string; readonly nextIndex: number } | null {
  const shorthand = pattern.slice(index, index + 2);
  if (shorthand !== String.raw`\s` && shorthand !== String.raw`\S`) {
    return null;
  }
  const ranges =
    shorthand === String.raw`\s`
      ? goPerlWhitespaceRanges
      : complementCharacterRanges(goPerlWhitespaceRanges);
  const rendered = renderCharacterRanges(ranges);
  return {
    output: insideCharacterClass ? rendered : `[${rendered}]`,
    nextIndex: index + 2,
  };
}

function hasUnsupportedCharacterClassSetAlgebra(pattern: string): boolean {
  let inCharacterClass = false;
  for (let index = 0; index < pattern.length; index += 1) {
    const token = pattern[index];
    if (token === "\\") {
      index += 1;
      continue;
    }
    if (token === "[") {
      if (inCharacterClass) {
        const posixClass = translatePosixCharacterClass(pattern, index);
        if (posixClass !== null) {
          index = posixClass.nextIndex - 1;
          continue;
        }
      }
      inCharacterClass = true;
      continue;
    }
    if (token === "]" && inCharacterClass) {
      inCharacterClass = false;
      continue;
    }
    if (
      inCharacterClass &&
      ["&&", "--", "~~"].some((operator) =>
        pattern.startsWith(operator, index),
      )
    ) {
      return true;
    }
  }
  return false;
}

function hasOversizedBoundedRepetition(pattern: string): boolean {
  let inCharacterClass = false;
  for (let index = 0; index < pattern.length; index += 1) {
    const token = pattern[index];
    if (token === "\\") {
      index += 1;
      continue;
    }
    if (token === "[") {
      if (inCharacterClass) {
        const posixClass = translatePosixCharacterClass(pattern, index);
        if (posixClass !== null) {
          index = posixClass.nextIndex - 1;
          continue;
        }
      }
      inCharacterClass = true;
      continue;
    }
    if (token === "]" && inCharacterClass) {
      inCharacterClass = false;
      continue;
    }
    if (inCharacterClass || token !== "{") continue;
    const repetition = pattern
      .slice(index)
      .match(/^\{(\d+)(?:,(\d*))?\}/u);
    if (repetition === null) continue;
    const minimum = Number(repetition[1]);
    const maximum =
      repetition[2] === undefined || repetition[2] === ""
        ? null
        : Number(repetition[2]);
    if (minimum > 100 || (maximum !== null && maximum > 100)) {
      return true;
    }
    index += repetition[0].length - 1;
  }
  return false;
}

interface PatternWork {
  readonly instructions: number;
  readonly nullable: boolean;
}

/**
 * Applies Go regexp/syntax's post-Simplify instruction costs so synchronized
 * patterns cannot exceed the server's compiled-program budget.
 */
function compiledPatternInstructions(pattern: string): number | null {
  let index = 0;
  const bounded = (value: number): number =>
    Math.min(value, MAX_REALQA_COMPILED_PATTERN_INSTRUCTIONS + 1);

  function quantify(work: PatternWork): PatternWork {
    const repetition = pattern
      .slice(index)
      .match(/^([*+?]|\{(\d+)(?:,(\d*))?\})(\??)/u);
    if (repetition === null) return work;
    index += repetition[0].length;
    const token = repetition[1] ?? "";
    if (token === "?") {
      return {
        instructions: bounded(work.instructions + 1),
        nullable: true,
      };
    }
    if (token === "*") {
      return {
        instructions: bounded(work.instructions + (work.nullable ? 2 : 1)),
        nullable: true,
      };
    }
    if (token === "+") {
      return {
        instructions: bounded(work.instructions + 1),
        nullable: work.nullable,
      };
    }
    const minimum = Number(repetition[2]);
    const maximum =
      repetition[3] === undefined
        ? minimum
        : repetition[3] === ""
          ? null
          : Number(repetition[3]);
    if (minimum === 0 && maximum === 0) {
      return { instructions: 1, nullable: true };
    }
    if (maximum === null) {
      if (minimum === 0) {
        return {
          instructions: bounded(work.instructions + (work.nullable ? 2 : 1)),
          nullable: true,
        };
      }
      return {
        instructions: bounded(work.instructions * minimum + 1),
        nullable: work.nullable,
      };
    }
    return {
      instructions: bounded(
        work.instructions * maximum + Math.max(0, maximum - minimum),
      ),
      nullable: minimum === 0 || work.nullable,
    };
  }

  function sequence(closesGroup: boolean): PatternWork | null {
    let instructions = 0;
    let nullable = true;
    let hasAtom = false;
    while (index < pattern.length) {
      const token = pattern[index] ?? "";
      if (token === "|" || (token === ")" && closesGroup)) break;
      const flagDirective = pattern
        .slice(index)
        .match(/^\(\?[imsU]*(?:-[imsU]*)?\)/u);
      if (flagDirective !== null) {
        index += flagDirective[0].length;
        continue;
      }

      let atom: PatternWork;
      if (token === "(") {
        const groupPrefix = pattern
          .slice(index)
          .match(/^\(\?[imsU]*(?:-[imsU]*)?:/u);
        const capturing = groupPrefix === null;
        index += groupPrefix?.[0].length ?? 1;
        const inner = alternation(true);
        if (inner === null || pattern[index] !== ")") return null;
        index += 1;
        atom = {
          instructions: bounded(inner.instructions + (capturing ? 2 : 0)),
          nullable: inner.nullable,
        };
      } else if (token === "[") {
        index += 1;
        let closed = false;
        while (index < pattern.length) {
          const posixClass = translatePosixCharacterClass(pattern, index);
          if (posixClass !== null) {
            index = posixClass.nextIndex;
          } else if (pattern[index] === "\\") {
            index += 2;
          } else if (pattern[index] === "]") {
            index += 1;
            closed = true;
            break;
          } else {
            index += 1;
          }
        }
        if (!closed) return null;
        atom = { instructions: 1, nullable: false };
      } else if (token === "\\") {
        const unicodeClass = pattern
          .slice(index)
          .match(/^\\[pP](?:\{\^?[A-Za-z_]+\}|[A-Za-z])/u);
        index += unicodeClass?.[0].length ?? 2;
        atom = {
          instructions: 1,
          nullable: ["A", "z", "b", "B"].includes(pattern[index - 1] ?? ""),
        };
      } else if (token === ")") {
        return null;
      } else {
        index += (pattern.codePointAt(index) ?? 0) > 0xffff ? 2 : 1;
        atom = {
          instructions: 1,
          nullable: token === "^" || token === "$",
        };
      }

      const quantified = quantify(atom);
      instructions = bounded(instructions + quantified.instructions);
      nullable &&= quantified.nullable;
      hasAtom = true;
    }
    return {
      instructions: hasAtom ? instructions : 1,
      nullable,
    };
  }

  function alternation(closesGroup: boolean): PatternWork | null {
    let work = sequence(closesGroup);
    if (work === null) return null;
    while (pattern[index] === "|") {
      index += 1;
      const branch = sequence(closesGroup);
      if (branch === null) return null;
      work = {
        instructions: bounded(work.instructions + branch.instructions + 1),
        nullable: work.nullable || branch.nullable,
      };
    }
    return work;
  }

  const work = alternation(false);
  return work === null || index !== pattern.length
    ? null
    : bounded(work.instructions + 2);
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
    if (inCharacterClass) {
      const posixClass = translatePosixCharacterClass(pattern, index);
      if (posixClass !== null) {
        index = posixClass.nextIndex - 1;
        continue;
      }
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

function translateAsciiWordBoundary(
  escaped: "b" | "B",
  flags: RegexFlags,
): string {
  const word = `[${renderCharacterRanges(posixCharacterClassRanges.word)}]`;
  const assertion =
    escaped === "b"
      ? `(?:(?<!${word})(?=${word})|(?<=${word})(?!${word}))`
      : `(?:(?<=${word})(?=${word})|(?<!${word})(?!${word}))`;
  return wrapRegexModifiers(flags, { ...flags, i: false }, assertion);
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
        output += token;
        index += 1;
        while (index < pattern.length) {
          const posixClass = translatePosixCharacterClass(
            pattern,
            index,
            flags.i,
          );
          if (posixClass !== null) {
            output += posixClass.output;
            index = posixClass.nextIndex;
          } else if (pattern[index] === "\\") {
            const whitespaceClass = translatePerlWhitespaceClassEscape(
              pattern,
              index,
              true,
            );
            const translatedClass =
              whitespaceClass ??
              translateUnicodeClassEscape(pattern, index, flags.i, true) ??
              translateGoCharacterEscape(pattern, index);
            if (translatedClass !== null) {
              output += translatedClass.output;
              index = translatedClass.nextIndex;
            } else {
              output += pattern.slice(index, index + 2);
              index += 2;
            }
          } else if (pattern[index] === "]") {
            output += "]";
            index += 1;
            break;
          } else {
            output += pattern[index];
            index += 1;
          }
        }
        continue;
      }
      if (token === "\\") {
        const whitespaceClass = translatePerlWhitespaceClassEscape(
          pattern,
          index,
          false,
        );
        const translatedClass =
          whitespaceClass ??
          translateUnicodeClassEscape(pattern, index, flags.i, false) ??
          translateGoCharacterEscape(pattern, index);
        if (translatedClass !== null) {
          output += translatedClass.output;
          index = translatedClass.nextIndex;
          continue;
        }
        const escaped = pattern[index + 1];
        output +=
          escaped === "A"
            ? "(?<![\\s\\S])"
            : escaped === "z"
              ? "(?![\\s\\S])"
              : escaped === "b" || escaped === "B"
                ? translateAsciiWordBoundary(escaped, flags)
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
      output +=
        token === "."
          ? flags.s
            ? token
            : "[^\\n]"
          : token === "{" || token === "}"
            ? `\\${token}`
            : token;
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
  if (/%(?![0-9A-Fa-f]{2})/u.test(probe)) return false;
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
      hasUnsupportedCharacterClassSetAlgebra(pattern) ||
      /\\[0-9]/u.test(pattern) ||
      hasOversizedBoundedRepetition(pattern) ||
      hasUnsafeRepeatedOrUnbalancedGroup(pattern) ||
      (compiledPatternInstructions(pattern) ?? Infinity) >
        MAX_REALQA_COMPILED_PATTERN_INSTRUCTIONS
    ) {
      return false;
    }
    return compileTitlePattern(pattern) !== null;
  });
}

function expandTemplate(template: string, match: RegExpExecArray): string {
  let output = "";
  let cursor = 0;
  while (cursor < template.length) {
    const dollar = template.indexOf("$", cursor);
    if (dollar < 0) {
      output += template.slice(cursor);
      break;
    }
    output += template.slice(cursor, dollar);
    if (template[dollar + 1] === "$") {
      output += "$";
      cursor = dollar + 2;
      continue;
    }
    const remainder = template.slice(dollar + 1);
    const braced = remainder.startsWith("{");
    const nameMatch = remainder
      .slice(braced ? 1 : 0)
      .match(/^[\p{L}\p{N}_]+/u);
    const name = nameMatch?.[0];
    const closingBrace = dollar + 2 + (name?.length ?? 0);
    if (
      name === undefined ||
      (braced && template[closingBrace] !== "}")
    ) {
      output += "$";
      cursor = dollar + 1;
      continue;
    }
    const numeric =
      /^[0-9]+$/u.test(name) &&
      (name.length === 1 || !name.startsWith("0")) &&
      Number(name) < 100_000_000;
    output += numeric ? (match[Number(name)] ?? "") : "";
    cursor = braced ? closingBrace + 1 : dollar + 1 + name.length;
  }
  return output;
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
    let match: RegExpExecArray | null = null;
    let expanded = rule.urlTemplate;
    if (rule.safeWindowTitlePattern === "") {
      // Go only expands capture references when a title expression exists.
    } else {
      const expression = compileTitlePattern(rule.safeWindowTitlePattern);
      if (expression === null) continue;
      match = expression.exec(windowTitle);
      if (match !== null) {
        expanded = expandTemplate(rule.urlTemplate, match);
      }
    }
    if (rule.safeWindowTitlePattern !== "" && match === null) continue;
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
