// SPDX-License-Identifier: Apache-2.0
// Pure Markdown link model for prompt composers. The canonical draft is always
// Markdown source text; the editable textarea shows a display projection in
// which valid inline HTTP(S) links collapse to their labels. Every function in
// this module is deterministic, has no DOM access and never logs prompt text.
import { findUrlTokens, validHttpUrl } from "./external-links";

/** Longest destination accepted as a link, matching the external-link limit. */
export const MAX_LINK_URL_LENGTH = 2048;
/** Links beyond this count stay ordinary visible source (bounded overlay). */
export const MAX_COLLAPSED_LINKS = 256;
/** Longest title or angle-destination scanned while protecting link syntax. */
const MAX_PROTECTED_SPAN = 4096;

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

const ASCII_PUNCTUATION = /[!-/:-@[-`{-~]/u;

// --- Escaping --------------------------------------------------------------

export function escapeLinkLabel(text: string): string {
  return text.replace(/[\\[\]]/gu, "\\$&");
}

export function escapeLinkDestination(text: string): string {
  return text.replace(/[\\()]/gu, "\\$&");
}

export function unescapeMarkdown(text: string): string {
  return text.replace(/\\([!-/:-@[-`{-~])/gu, "$1");
}

/** Builds `[URL](URL)` with escaping that resolves back to the same URL. */
export function markdownLinkFor(url: string): string {
  return `[${escapeLinkLabel(url)}](${escapeLinkDestination(url)})`;
}

// --- Scanning --------------------------------------------------------------

class Budget {
  remaining: number;
  constructor(length: number) {
    this.remaining = length * 24 + 20_000;
  }
  spend(units = 1): boolean {
    this.remaining -= units;
    return this.remaining >= 0;
  }
  get exhausted(): boolean {
    return this.remaining < 0;
  }
}

function inlineCodeRanges(
  text: string,
  start: number,
  end: number,
  into: MarkdownRange[],
  budget: Budget,
) {
  const runs: MarkdownRange[] = [];
  for (let index = start; index < end; index++) {
    if (!budget.spend()) return;
    const char = text[index];
    if (char === "\\") {
      index++;
      continue;
    }
    if (char !== "`") continue;
    const runStart = index;
    while (index + 1 < end && text[index + 1] === "`") index++;
    runs.push({ start: runStart, end: index + 1 });
  }
  // A span opens at a run and closes at the next run of exactly equal length.
  const nextSame: (number | undefined)[] = new Array(runs.length),
    seen = new Map<number, number>();
  for (let order = runs.length - 1; order >= 0; order--) {
    const length = runs[order]!.end - runs[order]!.start;
    nextSame[order] = seen.get(length);
    seen.set(length, order);
  }
  for (let order = 0; order < runs.length; order++) {
    const closer = nextSame[order];
    if (closer === undefined) continue;
    into.push({ start: runs[order]!.start, end: runs[closer]!.end });
    // Runs enclosed by the span are literal code characters.
    order = closer;
  }
}

/** Fenced code blocks and inline code spans, as sorted disjoint ranges. */
function codeRanges(text: string, budget: Budget): MarkdownRange[] {
  const ranges: MarkdownRange[] = [];
  let fence: { char: string; length: number; start: number } | undefined,
    segmentStart = -1,
    segmentEnd = -1;
  const flush = () => {
    if (segmentStart >= 0)
      inlineCodeRanges(text, segmentStart, segmentEnd, ranges, budget);
    segmentStart = -1;
  };
  let position = 0;
  while (position <= text.length) {
    if (!budget.spend(8)) break;
    let lineEnd = text.indexOf("\n", position);
    const next = lineEnd < 0 ? text.length + 1 : lineEnd + 1;
    if (lineEnd < 0) lineEnd = text.length;
    const line = text.slice(position, lineEnd);
    if (fence) {
      const close = /^ {0,3}(`{3,}|~{3,})[ \t]*$/u.exec(line);
      if (
        close &&
        close[1]![0] === fence.char &&
        close[1]!.length >= fence.length
      ) {
        ranges.push({ start: fence.start, end: lineEnd });
        fence = undefined;
      }
    } else {
      const open = /^ {0,3}(`{3,}|~{3,})(.*)$/u.exec(line);
      if (open && !(open[1]![0] === "`" && open[2]!.includes("`"))) {
        flush();
        fence = {
          char: open[1]![0]!,
          length: open[1]!.length,
          start: position,
        };
      } else if (/^\s*$/u.test(line)) flush();
      else {
        if (segmentStart < 0) segmentStart = position;
        segmentEnd = lineEnd;
      }
    }
    position = next;
  }
  if (fence) ranges.push({ start: fence.start, end: text.length });
  flush();
  return ranges.sort((a, b) => a.start - b.start);
}

function escapedAt(text: string, index: number): boolean {
  let count = 0;
  for (let cursor = index - 1; cursor >= 0 && text[cursor] === "\\"; cursor--)
    count++;
  return count % 2 === 1;
}

interface ParsedDestination {
  /** Raw non-whitespace destination run, when no angle brackets are used. */
  destStart: number;
  destEnd: number;
  /** True when the destination is immediately followed by the closing `)`. */
  simple: boolean;
  /** Offset just after the closing `)`. */
  end: number;
}

function parseDestination(
  text: string,
  from: number,
  budget: Budget,
): ParsedDestination | undefined {
  let cursor = from;
  const skipSpace = () => {
    while (cursor < text.length && /[ \t\n]/u.test(text[cursor]!)) {
      if (!budget.spend()) return false;
      cursor++;
    }
    return true;
  };
  const limit = Math.min(text.length, from + MAX_PROTECTED_SPAN);
  if (!skipSpace()) return undefined;
  const destStart = cursor,
    plain = text[cursor] !== "<";
  let destEnd: number;
  if (!plain) {
    cursor++;
    while (cursor < limit && text[cursor] !== ">") {
      if (!budget.spend() || text[cursor] === "\n" || text[cursor] === "<")
        return undefined;
      cursor += text[cursor] === "\\" ? 2 : 1;
    }
    if (text[cursor] !== ">") return undefined;
    cursor++;
    destEnd = cursor;
  } else {
    let depth = 0;
    while (cursor < limit) {
      if (!budget.spend()) return undefined;
      const char = text[cursor]!;
      if (char === "\\") {
        cursor += 2;
        continue;
      }
      if (/\s/u.test(char)) break;
      if (char === "(") depth++;
      else if (char === ")") {
        if (depth === 0) break;
        depth--;
      }
      cursor++;
    }
    destEnd = cursor;
  }
  if (!skipSpace()) return undefined;
  const untouched = cursor === destEnd;
  const opener = text[cursor];
  if (opener === '"' || opener === "'" || opener === "(") {
    const closer = opener === "(" ? ")" : opener,
      titleLimit = Math.min(text.length, cursor + 512);
    cursor++;
    while (cursor < titleLimit && text[cursor] !== closer) {
      if (!budget.spend()) return undefined;
      if (text[cursor] === "\\") cursor++;
      cursor++;
    }
    if (text[cursor] !== closer) return undefined;
    cursor++;
    if (!skipSpace()) return undefined;
  }
  if (text[cursor] !== ")") return undefined;
  return {
    destStart,
    destEnd,
    simple: plain && destStart === from && untouched && destEnd > destStart,
    end: cursor + 1,
  };
}

function unescapeLabel(
  text: string,
  start: number,
  end: number,
): { label: string; offsets: number[] } {
  let label = "";
  const offsets: number[] = [];
  for (let index = start; index < end; index++) {
    const char = text[index]!;
    if (
      char === "\\" &&
      index + 1 < end &&
      ASCII_PUNCTUATION.test(text[index + 1]!)
    ) {
      offsets.push(index);
      label += text[index + 1];
      index++;
    } else {
      offsets.push(index);
      label += char;
    }
  }
  offsets.push(end);
  return { label, offsets };
}

/**
 * True when an unescaped, non-code `](` marker lies inside `[start, end)` with
 * its `(` also inside the range. `markers` holds the ascending offsets of every
 * such `]` found by the single bracket pass, so each query costs one binary
 * search instead of a rescan of the label.
 */
function labelHasNestedLink(
  markers: readonly number[],
  start: number,
  end: number,
): boolean {
  let low = 0,
    high = markers.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (markers[mid]! < start) low = mid + 1;
    else high = mid;
  }
  const first = markers[low];
  return first !== undefined && first < end - 1;
}

function mergeRanges(ranges: MarkdownRange[]): MarkdownRange[] {
  const sorted = [...ranges].sort((a, b) => a.start - b.start),
    merged: MarkdownRange[] = [];
  for (const range of sorted) {
    const last = merged[merged.length - 1];
    if (last && range.start <= last.end)
      last.end = Math.max(last.end, range.end);
    else merged.push({ ...range });
  }
  return merged;
}

/**
 * Finds valid inline HTTP(S) links plus every range where bare URLs must stay
 * untouched (any inline link or image, code spans, fenced code, autolinks and
 * reference definitions). Work is linear in the text length with a fixed
 * budget; an exhausted budget reports `complete: false`.
 */
export function scanMarkdown(text: string): MarkdownScan {
  const budget = new Budget(text.length),
    code = /[`~]/u.test(text) ? codeRanges(text, budget) : [],
    protectedRanges: MarkdownRange[] = [...code],
    links: MarkdownLink[] = [];
  if (!text.includes("[") && !text.includes("<"))
    return {
      links,
      protectedRanges: mergeRanges(protectedRanges),
      complete: !budget.exhausted,
    };
  const inCode = new Uint8Array(text.length);
  for (const range of code) inCode.fill(1, range.start, range.end);
  // Linear bracket matching outside code; escapes never open or close.
  const opens: number[] = [],
    closeOf = new Map<number, number>(),
    stack: number[] = [],
    // Offsets of every `]` that is directly followed by `(` (ascending).
    markers: number[] = [];
  for (let index = 0; index < text.length; index++) {
    if (!budget.spend()) break;
    if (inCode[index]) continue;
    const char = text[index];
    if (char === "\\") {
      index++;
      continue;
    }
    if (char === "[") {
      stack.push(index);
      opens.push(index);
    } else if (char === "]") {
      if (text[index + 1] === "(") markers.push(index);
      if (stack.length) closeOf.set(stack.pop()!, index);
    } else if (
      // An escaped `<` was skipped above, so reaching it means it is real.
      char === "<" &&
      /^<[A-Za-z][A-Za-z0-9+.-]{1,31}:[^\s<>]*>/u.test(
        text.slice(index, index + 2100),
      )
    ) {
      const close = text.indexOf(">", index);
      protectedRanges.push({ start: index, end: close + 1 });
      index = close;
    }
  }
  const definition = /^ {0,3}\[[^\]\n]+\]:[ \t]*\S+/gmu;
  for (const match of text.matchAll(definition)) {
    if (!budget.spend(4)) break;
    protectedRanges.push({
      start: match.index!,
      end: match.index! + match[0].length,
    });
  }
  let consumed = 0;
  for (const open of opens) {
    if (open < consumed) continue;
    const close = closeOf.get(open);
    if (close === undefined || text[close + 1] !== "(") continue;
    if (!budget.spend(2)) break;
    const parsed = parseDestination(text, close + 2, budget);
    if (budget.exhausted) break;
    if (!parsed) continue;
    const image =
        open > 0 && text[open - 1] === "!" && !escapedAt(text, open - 1),
      start = image ? open - 1 : open;
    // An image description is flattened text, so it may hold links; the whole
    // image is protected before any nested-label rejection applies. Only a
    // link label may not hold another link.
    if (!image && labelHasNestedLink(markers, open + 1, close)) continue;
    protectedRanges.push({ start, end: parsed.end });
    consumed = parsed.end;
    if (image || !parsed.simple) continue;
    const raw = text.slice(parsed.destStart, parsed.destEnd);
    const url = unescapeMarkdown(raw),
      { label, offsets } = unescapeLabel(text, open + 1, close);
    if (
      url.length > MAX_LINK_URL_LENGTH ||
      !validHttpUrl(url) ||
      /[\u0000- \u007f]/u.test(url) ||
      !/\S/u.test(label) ||
      label.includes("\n")
    )
      continue;
    links.push({
      start,
      end: parsed.end,
      labelStart: open + 1,
      labelEnd: close,
      destStart: parsed.destStart,
      destEnd: parsed.destEnd,
      label,
      labelOffsets: offsets,
      url,
    });
  }
  return {
    links,
    protectedRanges: mergeRanges(protectedRanges),
    complete: !budget.exhausted,
  };
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

// A URL glued to word characters, an escape or an image marker, or placed where
// link text or a link destination begins, is not a standalone bare URL.
function urlStartsStandalone(text: string, start: number): boolean {
  if (start === 0) return true;
  const previous = text[start - 1]!;
  if (/[A-Za-z0-9\\![]/u.test(previous)) return false;
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
  displayStart: number;
  label: string;
  /** Absolute source offset of each label unit, plus the label end. */
  offsets: number[];
}

/**
 * Display text in which collapsed links show only their labels, with
 * monotonic offset maps in both directions. Offsets are UTF-16 code units.
 */
export class MarkdownProjection {
  readonly text: string;
  readonly regions: readonly ProjectionRegion[];
  private readonly hiddenBefore: number[];
  constructor(text: string, regions: ProjectionRegion[]) {
    this.text = text;
    this.regions = regions;
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
    if (index < 0) return offset;
    const region = this.regions[index]!;
    if (offset >= region.srcEnd) return offset - this.hiddenBefore[index + 1]!;
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
  toSource(offset: number): number {
    let low = 0,
      high = this.regions.length;
    while (low < high) {
      const mid = (low + high) >> 1;
      if (
        this.regions[mid]!.displayStart + this.regions[mid]!.label.length <=
        offset
      )
        low = mid + 1;
      else high = mid;
    }
    // `low` is the first region whose display end is after the offset.
    const region = this.regions[low];
    if (region && offset > region.displayStart)
      return region.offsets[offset - region.displayStart]!;
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
   * the same collapsed set. Returns undefined when the edit overlaps a region.
   */
  shifted(
    displayStart: number,
    displayEnd: number,
    inserted: number,
    sourceDelta: number,
    text: string,
  ): MarkdownProjection | undefined {
    const displayDelta = inserted - (displayEnd - displayStart),
      regions: ProjectionRegion[] = [];
    for (const region of this.regions) {
      const end = region.displayStart + region.label.length;
      if (end <= displayStart) regions.push(region);
      else if (region.displayStart >= displayEnd)
        regions.push({
          srcStart: region.srcStart + sourceDelta,
          srcEnd: region.srcEnd + sourceDelta,
          displayStart: region.displayStart + displayDelta,
          label: region.label,
          offsets: region.offsets.map((offset) => offset + sourceDelta),
        });
      else return undefined;
    }
    return new MarkdownProjection(text, regions);
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
  const regions: ProjectionRegion[] = [];
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
      displayStart: text.length,
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
