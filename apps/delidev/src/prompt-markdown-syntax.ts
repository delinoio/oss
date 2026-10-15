// SPDX-License-Identifier: Apache-2.0
// Shared vocabulary of the prompt Markdown model: limits, scan result types,
// line-ending maps and escape resolution. It has no dependencies so the scanner
// and the link model can both import it.

/** Longest destination accepted as a link, matching the external-link limit. */
export const MAX_LINK_URL_LENGTH = 2048;
/** Links beyond this count stay ordinary visible source (bounded overlay). */
export const MAX_COLLAPSED_LINKS = 256;
/**
 * Longest title, angle-destination or plain destination scanned while
 * protecting link syntax. Inline link titles, reference-definition titles and
 * destinations all use this one limit.
 */
export const MAX_PROTECTED_SPAN = 4096;
/** Longest label a reference (or definition) may carry. */
export const MAX_REFERENCE_LABEL = 999;

export interface MarkdownRange {
  start: number;
  end: number;
}

/** One valid inline HTTP(S) Markdown link `[label](destination)`. */
export interface MarkdownLink extends MarkdownRange {
  /** Offset just after `[`. */
  labelStart: number;
  /** Offset of `]`. */
  labelEnd: number;
  /** Offset just after `(`. */
  destStart: number;
  /** Offset of the closing `)`. */
  destEnd: number;
  /** Unescaped label text that a collapsed link displays. */
  label: string;
  /**
   * Source offset of each UTF-16 unit of `label`; the final entry equals
   * `labelEnd` so the array is `label.length + 1` long and strictly increasing.
   */
  labelOffsets: number[];
  /** The resolved destination (backslash escapes removed). */
  url: string;
}

export interface MarkdownScan {
  links: MarkdownLink[];
  /** Sorted, merged ranges where bare URLs must never be converted. */
  protectedRanges: MarkdownRange[];
  /** False when the work budget ran out; callers must not convert then. */
  complete: boolean;
}

export const ASCII_PUNCTUATION = /[!-/:-@[-`{-~]/u;

// --- Line endings ----------------------------------------------------------

/** Replaces every CRLF and lone CR with LF, the form a textarea reports. */
export function normalizeLineEndings(text: string): string {
  return text.includes("\r") ? text.replace(/\r\n?/gu, "\n") : text;
}

function countBelow(sorted: readonly number[], limit: number): number {
  let low = 0,
    high = sorted.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (sorted[mid]! < limit) low = mid + 1;
    else high = mid;
  }
  return low;
}

/**
 * Offset map between exact text and its line-ending-normalized form. A
 * textarea reports its value with every CRLF and lone CR as one LF, so a draft
 * that retains other endings can only be compared and addressed through this
 * map. A lone CR becomes one LF unit (offsets unchanged); a CRLF pair becomes
 * one LF unit. An offset between the CR and the LF of a pair maps to the start
 * of the normalized line ending.
 */
export class LineEndings {
  /** The text with every line ending written as LF. */
  readonly text: string;
  /** Exact offset of the CR of each CRLF pair, ascending. */
  private readonly pairs: number[] = [];
  /** Normalized offset of the LF that stands for each CRLF pair, ascending. */
  private readonly normalizedPairs: number[] = [];
  constructor(exact: string) {
    this.text = normalizeLineEndings(exact);
    if (!exact.includes("\r")) return;
    for (let index = 0; index < exact.length - 1; index++) {
      if (exact[index] === "\r" && exact[index + 1] === "\n") {
        this.normalizedPairs.push(index - this.pairs.length);
        this.pairs.push(index);
        index++;
      }
    }
  }
  /** True when the exact and normalized offsets are identical. */
  get uniform(): boolean {
    return this.pairs.length === 0;
  }
  /** Exact offset -> normalized offset. */
  toNormalized(offset: number): number {
    return offset - countBelow(this.pairs, offset);
  }
  /** Normalized offset -> exact offset (the start of a mapped line ending). */
  toExact(offset: number): number {
    return offset + countBelow(this.normalizedPairs, offset);
  }
}

export function unescapeMarkdown(text: string): string {
  return text.replace(/\\([!-/:-@[-`{-~])/gu, "$1");
}
