// SPDX-License-Identifier: Apache-2.0
// Pure Markdown link model for prompt composers. The canonical draft is always
// Markdown source text; the editable textarea shows a display projection in
// which valid inline HTTP(S) links collapse to their labels. Every function in
// this module is deterministic, has no DOM access and never logs prompt text.
// Syntax detection lives in `prompt-markdown-scan.ts` (one scanner, one
// documented precedence order); this module converts, projects and edits.
import { findUrlTokens } from "./external-links";
import {
  LineEndings,
  MAX_COLLAPSED_LINKS,
  type MarkdownLink,
  type MarkdownRange,
  type MarkdownScan,
} from "./prompt-markdown-syntax";
import { scanMarkdown } from "./prompt-markdown-scan";

export {
  LineEndings,
  MAX_COLLAPSED_LINKS,
  MAX_LINK_URL_LENGTH,
  normalizeLineEndings,
  unescapeMarkdown,
  type MarkdownLink,
  type MarkdownRange,
  type MarkdownScan,
} from "./prompt-markdown-syntax";
export { scanMarkdown };

/**
 * Joins `prefix` with `rest` for an edit. A lone CR left before an inserted or
 * newly adjacent LF would silently fuse into one CRLF pair, which a textarea
 * shows as a single line ending; writing the CR as LF keeps the exact text and
 * the displayed text the same length apart from the intended edit.
 */
export function joinKeepingLineEndings(prefix: string, rest: string): string {
  return prefix.endsWith("\r") && rest.startsWith("\n")
    ? `${prefix.slice(0, -1)}\n${rest}`
    : prefix + rest;
}

// --- Escaping --------------------------------------------------------------

export function escapeLinkLabel(text: string): string {
  return text.replace(/[\\[\]]/gu, "\\$&");
}

export function escapeLinkDestination(text: string): string {
  return text.replace(/[\\()]/gu, "\\$&");
}

/** Builds `[URL](URL)` with escaping that resolves back to the same URL. */
export function markdownLinkFor(url: string): string {
  return `[${escapeLinkLabel(url)}](${escapeLinkDestination(url)})`;
}

// --- Conversion ------------------------------------------------------------

function overlapsProtected(
  ranges: readonly MarkdownRange[],
  start: number,
  end: number,
): boolean {
  let low = 0,
    high = ranges.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (ranges[mid]!.end <= start) low = mid + 1;
    else high = mid;
  }
  const hit = ranges[low];
  return hit !== undefined && hit.start < end;
}

// A URL glued to a preceding letter, digit (in any script), combining mark,
// escape or image marker, or placed where link text or a link destination
// begins, is not a standalone bare URL. The preceding code point is read as a
// whole so an astral letter (a surrogate pair) is classified as one character.
const WORD_CHARACTER = /[\p{L}\p{N}\p{M}]/u;
function urlStartsStandalone(text: string, start: number): boolean {
  if (start === 0) return true;
  const previous = text[start - 1]!;
  if (previous === "\\" || previous === "!" || previous === "[") return false;
  const pair = start >= 2 ? text.codePointAt(start - 2) : undefined;
  const glued =
    pair !== undefined && pair > 0xffff ? String.fromCodePoint(pair) : previous;
  if (WORD_CHARACTER.test(glued)) return false;
  return !(previous === "(" && text[start - 2] === "]");
}

export interface UrlConversion {
  text: string;
  /** Number of UTF-16 units added by conversion (zero when unchanged). */
  delta: number;
  changed: boolean;
}

/**
 * Converts the bare HTTP(S) URLs that lie wholly inside `[from, to)` to
 * `[URL](URL)`. Tokens come from `findUrlTokens`, so every external-link limit
 * and punctuation rule applies. A URL that continues outside the range, is
 * glued to preceding word characters or lies in protected syntax stays
 * unchanged. The result is idempotent: converted text is protected link syntax.
 */
export function convertBareUrls(
  text: string,
  from: number,
  to: number,
  knownScan?: MarkdownScan,
): UrlConversion {
  const unchanged = { text, delta: 0, changed: false };
  if (from >= to) return unchanged;
  let wordStart = from;
  while (wordStart > 0 && !/\s/u.test(text[wordStart - 1]!)) wordStart--;
  let wordEnd = to;
  while (wordEnd < text.length && !/\s/u.test(text[wordEnd]!)) wordEnd++;
  const candidates = findUrlTokens(text.slice(wordStart, wordEnd))
    .map((token) => ({
      start: token.start + wordStart,
      end: token.end + wordStart,
      url: token.url,
    }))
    .filter(
      (token) =>
        token.start >= from &&
        token.end <= to &&
        urlStartsStandalone(text, token.start),
    );
  if (!candidates.length) return unchanged;
  const scan = knownScan ?? scanMarkdown(text);
  if (!scan.complete) return unchanged;
  const tokens = candidates.filter(
    (token) => !overlapsProtected(scan.protectedRanges, token.start, token.end),
  );
  if (!tokens.length) return unchanged;
  let result = "",
    cursor = 0;
  for (const token of tokens) {
    result += text.slice(cursor, token.start) + markdownLinkFor(token.url);
    cursor = token.end;
  }
  result += text.slice(cursor);
  return { text: result, delta: result.length - text.length, changed: true };
}

/**
 * Converts the URL word that a just-typed space or newline terminated. The
 * delimiter at `delimiter` stays in the output untouched.
 */
export function convertTerminatedUrl(
  text: string,
  delimiter: number,
): UrlConversion {
  let start = delimiter;
  while (start > 0 && !/\s/u.test(text[start - 1]!)) start--;
  return convertBareUrls(text, start, delimiter);
}

/**
 * Converts the URL word before every space or newline in `[from, to)`. Later
 * delimiters are handled first so earlier offsets stay valid.
 */
export function convertDelimitedWords(
  text: string,
  from: number,
  to: number,
): UrlConversion {
  let result = text;
  for (let index = Math.min(to, text.length) - 1; index >= from; index--) {
    if (!isUrlDelimiter(result[index])) continue;
    result = convertTerminatedUrl(result, index).text;
  }
  return {
    text: result,
    delta: result.length - text.length,
    changed: result !== text,
  };
}

export function isUrlDelimiter(char: string | undefined): boolean {
  return char === " " || char === "\n";
}

// --- Projection ------------------------------------------------------------

interface ProjectionRegion {
  srcStart: number;
  srcEnd: number;
  /** Start in the displayed (line-ending normalized) text. */
  displayStart: number;
  /** Start in the projected text before line endings are normalized. */
  rawStart: number;
  label: string;
  /** Absolute source offset of each label unit, plus the label end. */
  offsets: number[];
}

/**
 * Display text in which collapsed links show only their labels, with
 * monotonic offset maps in both directions. Offsets are UTF-16 code units.
 * The display text writes every line ending as LF, exactly as a textarea
 * reports its value, so a draft that retains CRLF or lone CR endings still
 * equals the native field and every offset maps back to the exact source.
 * Collapsed links never contain a line ending.
 */
export class MarkdownProjection {
  readonly text: string;
  readonly regions: readonly ProjectionRegion[];
  private readonly raw: string;
  private readonly endings: LineEndings;
  private readonly hiddenBefore: number[];
  /**
   * `raw` is the projected text before line-ending normalization; each region
   * gives its `rawStart` there (`displayStart` is derived from it).
   */
  constructor(
    raw: string,
    regions: readonly Omit<ProjectionRegion, "displayStart">[],
  ) {
    this.raw = raw;
    this.endings = new LineEndings(raw);
    this.text = this.endings.text;
    this.regions = regions.map((region) => ({
      ...region,
      displayStart: this.endings.toNormalized(region.rawStart),
    }));
    // hiddenBefore[i] is the number of hidden units before region i; the last
    // entry is the total.
    this.hiddenBefore = [0];
    let hidden = 0;
    for (const region of regions) {
      hidden += region.srcEnd - region.srcStart - region.label.length;
      this.hiddenBefore.push(hidden);
    }
  }
  get collapsed(): boolean {
    return this.regions.length > 0;
  }
  /** Source offset -> display offset. Offsets inside hidden syntax clamp. */
  toDisplay(offset: number): number {
    let low = 0,
      high = this.regions.length;
    while (low < high) {
      const mid = (low + high) >> 1;
      if (this.regions[mid]!.srcStart < offset) low = mid + 1;
      else high = mid;
    }
    const index = low - 1;
    if (index < 0) return this.endings.toNormalized(offset);
    const region = this.regions[index]!;
    if (offset >= region.srcEnd)
      return this.endings.toNormalized(offset - this.hiddenBefore[index + 1]!);
    // Strictly inside: count label units that start before the offset.
    let lo = 0,
      hi = region.label.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (region.offsets[mid]! < offset) lo = mid + 1;
      else hi = mid;
    }
    return region.displayStart + lo;
  }
  /** Display offset -> source offset. Region edges map to the link edges. */
  toSource(displayOffset: number): number {
    // A display offset on a normalized line ending maps to the start of the
    // exact ending (before a CR of a CRLF pair).
    const offset = this.endings.toExact(displayOffset);
    let low = 0,
      high = this.regions.length;
    while (low < high) {
      const mid = (low + high) >> 1;
      if (
        this.regions[mid]!.rawStart + this.regions[mid]!.label.length <=
        offset
      )
        low = mid + 1;
      else high = mid;
    }
    // `low` is the first region whose display end is after the offset.
    const region = this.regions[low];
    if (region && offset > region.rawStart)
      return region.offsets[offset - region.rawStart]!;
    return offset + this.hiddenBefore[low]!;
  }
  /**
   * Source range for a display range being replaced. A range inside one
   * collapsed label maps through the label units (so a label edit stays a
   * label edit); a range covering a whole label replaces the whole link.
   */
  toSourceRange(displayStart: number, displayEnd: number): [number, number] {
    if (displayStart < displayEnd) {
      for (const region of this.regions) {
        const end = region.displayStart + region.label.length;
        if (displayStart >= region.displayStart && displayEnd <= end) {
          if (displayStart === region.displayStart && displayEnd === end)
            return [region.srcStart, region.srcEnd];
          return [
            region.offsets[displayStart - region.displayStart]!,
            region.offsets[displayEnd - region.displayStart]!,
          ];
        }
      }
    }
    return [this.toSource(displayStart), this.toSource(displayEnd)];
  }
  /** Region strictly containing the display offset, if any. */
  regionAt(displayOffset: number) {
    return this.regions.find(
      (region) =>
        displayOffset > region.displayStart &&
        displayOffset < region.displayStart + region.label.length,
    );
  }
  /**
   * Applies a display edit that lies outside every collapsed region, keeping
   * the same collapsed set. `inserted` is the text now between the display
   * offsets (written with LF endings) and `sourceDelta` its change in source
   * length. Returns undefined when the edit overlaps a region.
   */
  shifted(
    displayStart: number,
    displayEnd: number,
    inserted: string,
    sourceDelta: number,
  ): MarkdownProjection | undefined {
    const rawFrom = this.endings.toExact(displayStart),
      rawTo = this.endings.toExact(displayEnd),
      rawDelta = inserted.length - (rawTo - rawFrom),
      regions: Omit<ProjectionRegion, "displayStart">[] = [];
    const raw = joinKeepingLineEndings(
      this.raw.slice(0, rawFrom),
      inserted + this.raw.slice(rawTo),
    );
    for (const region of this.regions) {
      const end = region.displayStart + region.label.length;
      if (end <= displayStart) regions.push(region);
      else if (region.displayStart >= displayEnd)
        regions.push({
          srcStart: region.srcStart + sourceDelta,
          srcEnd: region.srcEnd + sourceDelta,
          rawStart: region.rawStart + rawDelta,
          label: region.label,
          offsets: region.offsets.map((offset) => offset + sourceDelta),
        });
      else return undefined;
    }
    return new MarkdownProjection(raw, regions);
  }
}

/**
 * Builds the projection of `source`. `collapse(link)` decides which valid links
 * collapse; at most MAX_COLLAPSED_LINKS links ever collapse.
 */
export function projectMarkdown(
  source: string,
  links: readonly MarkdownLink[],
  collapse: (link: MarkdownLink) => boolean,
): MarkdownProjection {
  const regions: Omit<ProjectionRegion, "displayStart">[] = [];
  let text = "",
    cursor = 0,
    collapsedCount = 0;
  for (const link of links) {
    if (collapsedCount >= MAX_COLLAPSED_LINKS) break;
    if (link.start < cursor || !collapse(link)) continue;
    collapsedCount++;
    text += source.slice(cursor, link.start);
    regions.push({
      srcStart: link.start,
      srcEnd: link.end,
      rawStart: text.length,
      label: link.label,
      offsets: link.labelOffsets,
    });
    text += link.label;
    cursor = link.end;
  }
  text += source.slice(cursor);
  return new MarkdownProjection(text, regions);
}

export interface LinkSelection {
  start: number;
  end: number;
}

/**
 * A link reveals its whole source when the caret is strictly inside it or a
 * non-empty selection overlaps it. Link edges alone never reveal.
 */
export function selectionRevealsLink(
  link: MarkdownRange,
  selection: LinkSelection,
): boolean {
  return selection.start === selection.end
    ? selection.start > link.start && selection.start < link.end
    : selection.start < link.end && selection.end > link.start;
}

// --- Display edits ---------------------------------------------------------

export interface CanonicalEdit {
  /** Canonical range replaced by `inserted`. */
  start: number;
  end: number;
  inserted: string;
  /** Display range replaced and the inserted length (display coordinates). */
  displayStart: number;
  displayEnd: number;
  /** Maps a caret in the new display text to the new canonical text. */
  caret: (displayCaret: number) => number;
}

/**
 * Translates a native textarea edit (old display text to new display text)
 * into a canonical edit. `caretHint` is the caret after the edit and picks the
 * unambiguous edit when repeated characters make the textual diff ambiguous.
 */
export function canonicalEditFromDisplay(
  projection: MarkdownProjection,
  nextDisplay: string,
  caretHint: number,
): CanonicalEdit {
  const previous = projection.text;
  let prefix = 0;
  const maxPrefix = Math.min(previous.length, nextDisplay.length);
  const hinted = Math.min(Math.max(caretHint, 0), nextDisplay.length);
  // The caret ends at the inserted text, so the suffix after it is unchanged.
  const tail = nextDisplay.length - hinted;
  let suffix = 0;
  if (
    tail <= previous.length &&
    previous.slice(previous.length - tail) === nextDisplay.slice(hinted)
  ) {
    suffix = tail;
    const limit = Math.min(
      previous.length - suffix,
      nextDisplay.length - suffix,
    );
    while (prefix < limit && previous[prefix] === nextDisplay[prefix]) prefix++;
  } else {
    while (prefix < maxPrefix && previous[prefix] === nextDisplay[prefix])
      prefix++;
    while (
      suffix < previous.length - prefix &&
      suffix < nextDisplay.length - prefix &&
      previous[previous.length - 1 - suffix] ===
        nextDisplay[nextDisplay.length - 1 - suffix]
    )
      suffix++;
  }
  const displayStart = prefix,
    displayEnd = previous.length - suffix,
    inserted = nextDisplay.slice(prefix, nextDisplay.length - suffix),
    [start, end] = projection.toSourceRange(displayStart, displayEnd),
    sourceDelta = inserted.length - (end - start),
    displayDelta = inserted.length - (displayEnd - displayStart);
  return {
    start,
    end,
    inserted,
    displayStart,
    displayEnd,
    caret: (caret) => {
      if (caret <= displayStart) return projection.toSource(caret);
      if (caret <= displayStart + inserted.length)
        return start + caret - displayStart;
      return projection.toSource(caret - displayDelta) + sourceDelta;
    },
  };
}
