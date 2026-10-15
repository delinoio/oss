// SPDX-License-Identifier: Apache-2.0
// The one Markdown scanner for prompt composers. It reads LF text in two fixed
// stages and decides every construct by a single documented precedence order
// (the "Precedence" table of the prompt composer Markdown links contract):
//
//   Stage 1, blocks:   P1 fenced code blocks claim whole lines; the remaining
//                      lines form segments split at blank lines and fences.
//   Stage 2, one left-to-right walk per segment, first claim wins:
//                      P2 link reference definitions (at a line start),
//                      P3 code spans, P4 autolinks and raw HTML tags,
//                      then brackets are recorded for stage 3.
//   Stage 3, brackets: P5 images, P6 inline links, P7 reference links, with
//                      bare URLs (P8) converted only outside every claim.
//
// A construct never crosses a segment boundary, text claimed by an earlier
// precedence is invisible to every later one, and backslash escapes are inert
// inside code. The module is pure and deterministic, has no DOM access and
// never logs prompt text.
import { validHttpUrl } from "./external-links";
import {
  ASCII_PUNCTUATION,
  LineEndings,
  MAX_LINK_URL_LENGTH,
  MAX_PROTECTED_SPAN,
  MAX_REFERENCE_LABEL,
  unescapeMarkdown,
  type MarkdownLink,
  type MarkdownRange,
  type MarkdownScan,
} from "./prompt-markdown-syntax";

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

/** A paragraph-like run of lines; nothing but a fence crosses its edges. */
type Segment = MarkdownRange;

/** Text bounded by the end of its segment, so no parser reads a later block. */
interface Source {
  text: string;
  /** Exclusive end of the segment. */
  end: number;
}
const charAt = (source: Source, index: number): string | undefined =>
  index < source.end ? source.text[index] : undefined;

const isSpaceOrTab = (char: string | undefined) =>
  char === " " || char === "\t";

function skipSpacesAndTabs(source: Source, from: number): number {
  let cursor = from;
  while (isSpaceOrTab(charAt(source, cursor))) cursor++;
  return cursor;
}

/** True when the line starting at `from` is empty or only spaces and tabs. */
function blankLineAt(source: Source, from: number): boolean {
  const cursor = skipSpacesAndTabs(source, from);
  return cursor >= source.end || source.text[cursor] === "\n";
}

// --- Stage 1: blocks ---------------------------------------------------------

/**
 * Fenced code blocks (claimed whole) and the segments between them. A line of
 * only spaces and tabs is blank; a fence opening interrupts a segment.
 */
function splitBlocks(
  text: string,
  budget: Budget,
  claims: MarkdownRange[],
): Segment[] {
  const segments: Segment[] = [];
  let fence: { char: string; length: number; start: number } | undefined,
    segmentStart = -1,
    segmentEnd = -1;
  const flush = () => {
    if (segmentStart >= 0)
      segments.push({ start: segmentStart, end: segmentEnd });
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
        claims.push({ start: fence.start, end: lineEnd });
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
      } else if (/^[ \t]*$/u.test(line)) flush();
      else {
        if (segmentStart < 0) segmentStart = position;
        segmentEnd = lineEnd;
      }
    }
    position = next;
  }
  if (fence) claims.push({ start: fence.start, end: text.length });
  flush();
  return segments;
}

// --- Stage 2: reference definitions -----------------------------------------

/** CommonMark label matching: case-folded with collapsed whitespace. */
function normalizeReferenceLabel(label: string): string {
  return label.trim().replace(/\s+/gu, " ").toLowerCase().toUpperCase();
}

/** End of a quoted or parenthesized title opening at `open`, or undefined. */
function parseDefinitionTitle(
  source: Source,
  open: number,
  budget: Budget,
): number | undefined {
  const opener = charAt(source, open);
  if (opener !== '"' && opener !== "'" && opener !== "(") return undefined;
  const closer = opener === "(" ? ")" : opener,
    limit = Math.min(source.end, open + MAX_PROTECTED_SPAN);
  for (let cursor = open + 1; cursor < limit; cursor++) {
    if (!budget.spend()) return undefined;
    const char = source.text[cursor]!;
    if (char === "\\") cursor++;
    else if (char === closer) return cursor + 1;
    else if (opener === "(" && char === "(") return undefined;
    // A title never spans a blank line.
    else if (char === "\n" && blankLineAt(source, cursor + 1)) return undefined;
  }
  return undefined;
}

/**
 * Parses one complete link reference definition whose `[` is at `bracket` on
 * a line that starts at `lineStart`. The destination and the title may sit on
 * the lines that follow; only blank lines end the definition early. Returns the
 * exclusive end of the definition (its destination, or its title when a valid
 * one follows) and its label.
 */
function parseDefinition(
  source: Source,
  bracket: number,
  budget: Budget,
): { end: number; label: string } | undefined {
  const { text } = source;
  let cursor = bracket + 1,
    meaningful = false;
  const labelLimit = Math.min(source.end, bracket + 2 + MAX_REFERENCE_LABEL);
  for (; cursor < labelLimit; cursor++) {
    if (!budget.spend()) return undefined;
    const char = text[cursor]!;
    if (char === "\\") {
      meaningful = true;
      cursor++;
    } else if (char === "[") return undefined;
    else if (char === "]") break;
    else if (char === "\n") {
      if (blankLineAt(source, cursor + 1)) return undefined;
    } else if (!/\s/u.test(char)) meaningful = true;
  }
  if (
    charAt(source, cursor) !== "]" ||
    !meaningful ||
    charAt(source, cursor + 1) !== ":"
  )
    return undefined;
  const label = text.slice(bracket + 1, cursor);
  if (label.length > MAX_REFERENCE_LABEL) return undefined;
  // At most one line ending separates the colon from the destination.
  cursor = skipSpacesAndTabs(source, cursor + 2);
  if (charAt(source, cursor) === "\n") {
    cursor = skipSpacesAndTabs(source, cursor + 1);
    if (cursor >= source.end || text[cursor] === "\n") return undefined;
  }
  let destEnd: number;
  const limit = Math.min(source.end, cursor + MAX_PROTECTED_SPAN);
  if (charAt(source, cursor) === "<") {
    let scan = cursor + 1;
    for (; scan < limit; scan++) {
      if (!budget.spend()) return undefined;
      const char = text[scan]!;
      if (char === "\\") {
        if (charAt(source, scan + 1) === "\n") return undefined;
        scan++;
      } else if (char === ">" || char === "\n" || char === "<") break;
    }
    if (charAt(source, scan) !== ">") return undefined;
    destEnd = scan + 1;
  } else {
    let scan = cursor,
      depth = 0;
    for (; scan < limit; scan++) {
      if (!budget.spend()) return undefined;
      const char = text[scan]!;
      if (char === "\\" && !/\s/u.test(charAt(source, scan + 1) ?? " ")) scan++;
      else if (/[\s\u0000-\u001f\u007f]/u.test(char)) break;
      else if (char === "(") depth++;
      else if (char === ")") {
        if (depth === 0) break;
        depth--;
      }
    }
    if (scan === cursor || depth !== 0 || scan - cursor >= MAX_PROTECTED_SPAN)
      return undefined;
    destEnd = scan;
  }
  // A title follows after whitespace: on the same line, or on the next line.
  // The definition must otherwise end at the end of a line.
  const afterDestination = skipSpacesAndTabs(source, destEnd);
  const destinationEndsLine =
    afterDestination >= source.end || text[afterDestination] === "\n";
  let titleOpen: number | undefined;
  if (destinationEndsLine) {
    if (afterDestination < source.end) {
      const next = skipSpacesAndTabs(source, afterDestination + 1);
      if (next < source.end && text[next] !== "\n") titleOpen = next;
    }
  } else if (afterDestination > destEnd) titleOpen = afterDestination;
  else return undefined;
  if (titleOpen !== undefined) {
    const titleEnd = parseDefinitionTitle(source, titleOpen, budget);
    if (titleEnd !== undefined && blankLineAt(source, titleEnd))
      return { end: titleEnd, label };
  }
  // A malformed title on the destination's line voids the definition; one on a
  // later line is a separate paragraph and leaves the destination complete.
  return destinationEndsLine ? { end: destEnd, label } : undefined;
}

// A definition the strict parser rejects keeps single-line protection of its
// first destination token (and still defines its label).
const LOOSE_DEFINITION = /[ ]{0,3}\[([^\]\n]+)\]:[ \t]*\S+/uy;

// --- Stage 2: code spans, autolinks and raw HTML ------------------------------

/** Maximal backtick runs of one segment, grouped by length. */
function backtickRuns(
  text: string,
  segment: Segment,
  budget: Budget,
): Map<number, number[]> {
  const runs = new Map<number, number[]>();
  for (let index = segment.start; index < segment.end; index++) {
    if (!budget.spend()) break;
    if (text[index] !== "`") continue;
    let end = index + 1;
    while (end < segment.end && text[end] === "`") end++;
    const starts = runs.get(end - index);
    if (starts) starts.push(index);
    else runs.set(end - index, [index]);
    index = end - 1;
  }
  return runs;
}

/** First run start at or after `from` in an ascending list, if any. */
function firstAtOrAfter(
  starts: readonly number[] | undefined,
  from: number,
): number | undefined {
  if (!starts) return undefined;
  let low = 0,
    high = starts.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (starts[mid]! < from) low = mid + 1;
    else high = mid;
  }
  return starts[low];
}

// Raw HTML open and closing tags keep their attribute values verbatim.
const HTML_TAG =
  /^<\/?[A-Za-z][A-Za-z0-9-]*(?:\s+[A-Za-z_:][\w:.-]*(?:\s*=\s*(?:[^\s"'=<>`]+|'[^']*'|"[^"]*"))?)*\s*\/?>/u;
const AUTOLINK = /^<[A-Za-z][A-Za-z0-9+.-]{1,31}:[^\s<>]*>/u;
const MAX_TAG_LENGTH = 2100;

// --- Stage 3: inline destinations ----------------------------------------------

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
  source: Source,
  from: number,
  budget: Budget,
): ParsedDestination | undefined {
  const { text } = source;
  let cursor = from;
  const skipSpace = () => {
    while (
      cursor < source.end &&
      (text[cursor] === " " || text[cursor] === "\t" || text[cursor] === "\n")
    ) {
      if (!budget.spend()) return false;
      cursor++;
    }
    return true;
  };
  const limit = Math.min(source.end, from + MAX_PROTECTED_SPAN);
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
    if (charAt(source, cursor) !== ">") return undefined;
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
    destEnd = Math.min(cursor, source.end);
    cursor = destEnd;
  }
  if (!skipSpace()) return undefined;
  const untouched = cursor === destEnd;
  const opener = charAt(source, cursor);
  if (opener === '"' || opener === "'" || opener === "(") {
    // A title is scanned up to the same limit as a definition title.
    const closer = opener === "(" ? ")" : opener,
      titleLimit = Math.min(source.end, cursor + MAX_PROTECTED_SPAN);
    cursor++;
    while (cursor < titleLimit && text[cursor] !== closer) {
      if (!budget.spend()) return undefined;
      if (text[cursor] === "\\") cursor++;
      cursor++;
    }
    if (charAt(source, cursor) !== closer) return undefined;
    cursor++;
    if (!skipSpace()) return undefined;
  }
  if (charAt(source, cursor) !== ")") return undefined;
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

/** First element of an ascending list at or after `start`. */
function firstFrom(
  sorted: readonly number[],
  start: number,
): number | undefined {
  let low = 0,
    high = sorted.length;
  while (low < high) {
    const mid = (low + high) >> 1;
    if (sorted[mid]! < start) low = mid + 1;
    else high = mid;
  }
  return sorted[low];
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

/** One bracket pair found outside every claim. */
interface BracketPair {
  open: number;
  close: number;
  /** An unescaped `!` directly precedes the opening bracket. */
  image: boolean;
  /** Exclusive end of the segment that holds the pair. */
  segmentEnd: number;
}

/**
 * Finds valid inline HTTP(S) links plus every range where bare URLs must stay
 * untouched (any inline, reference or shortcut link or image, code spans,
 * fenced code, autolinks, raw HTML tags and reference definitions). Every
 * line-ending form (LF, CRLF, lone CR) scans alike: the text is read through
 * its LF-normalized form and all offsets are mapped back to the exact text.
 * Work is linear in the text length with a fixed budget; an exhausted budget
 * reports `complete: false`.
 */
export function scanMarkdown(text: string): MarkdownScan {
  if (!text.includes("\r")) return scanNormalized(text);
  const endings = new LineEndings(text),
    scan = scanNormalized(endings.text),
    exact = (offset: number) => endings.toExact(offset);
  return {
    complete: scan.complete,
    protectedRanges: scan.protectedRanges.map((range) => ({
      start: exact(range.start),
      end: exact(range.end),
    })),
    links: scan.links.map((link) => ({
      ...link,
      start: exact(link.start),
      end: exact(link.end),
      labelStart: exact(link.labelStart),
      labelEnd: exact(link.labelEnd),
      destStart: exact(link.destStart),
      destEnd: exact(link.destEnd),
      labelOffsets: link.labelOffsets.map(exact),
    })),
  };
}

function scanNormalized(text: string): MarkdownScan {
  const budget = new Budget(text.length),
    claims: MarkdownRange[] = [],
    links: MarkdownLink[] = [],
    segments = splitBlocks(text, budget, claims);
  const finish = (): MarkdownScan => ({
    links,
    protectedRanges: mergeRanges(claims),
    complete: !budget.exhausted,
  });
  if (!text.includes("[") && !text.includes("<") && !text.includes("`"))
    return finish();

  // --- Stage 2: one left-to-right walk per segment, first claim wins. --------
  const hasBacktick = text.includes("`"),
    definitions = new Set<string>(),
    pairAt = new Map<number, BracketPair>(),
    opens: number[] = [],
    // Offsets of every `]` followed by `(` that does not close an image.
    linkMarkers: number[] = [];
  for (const segment of segments) {
    if (budget.exhausted) break;
    const source: Source = { text, end: segment.end },
      runs = hasBacktick
        ? backtickRuns(text, segment, budget)
        : new Map<number, number[]>(),
      stack: { open: number; image: boolean }[] = [];
    let lastBang = -2,
      index = segment.start;
    while (index < segment.end) {
      if (!budget.spend()) break;
      // P2: a definition claims whole lines from a line start.
      if (index === 0 || text[index - 1] === "\n") {
        const bracket = skipSpacesAndTabs(source, index);
        if (text[bracket] === "[" && bracket - index <= 3) {
          const parsed = parseDefinition(source, bracket, budget);
          if (budget.exhausted) break;
          if (parsed) {
            claims.push({ start: index, end: parsed.end });
            definitions.add(normalizeReferenceLabel(parsed.label));
            index = parsed.end;
            continue;
          }
          LOOSE_DEFINITION.lastIndex = index;
          const loose = LOOSE_DEFINITION.exec(text);
          if (loose && index + loose[0].length <= segment.end) {
            claims.push({ start: index, end: index + loose[0].length });
            definitions.add(normalizeReferenceLabel(loose[1]!));
            index += loose[0].length;
            continue;
          }
        }
      }
      const char = text[index]!;
      if (char === "\\") {
        // Outside code a backslash makes the next character inert.
        index += 2;
      } else if (char === "`") {
        // P3: a span closes at the next run of exactly the opening length;
        // backslashes are literal inside it. An unmatched run is literal.
        let runEnd = index + 1;
        while (runEnd < segment.end && text[runEnd] === "`") runEnd++;
        const closer = firstAtOrAfter(runs.get(runEnd - index), runEnd);
        if (closer !== undefined) {
          claims.push({ start: index, end: closer + (runEnd - index) });
          index = closer + (runEnd - index);
        } else index = runEnd;
      } else if (char === "<") {
        // P4: autolinks and raw HTML tags.
        const head = text.slice(
            index,
            Math.min(segment.end, index + MAX_TAG_LENGTH),
          ),
          tag = AUTOLINK.exec(head) ?? HTML_TAG.exec(head);
        if (tag) {
          claims.push({ start: index, end: index + tag[0].length });
          index += tag[0].length;
        } else index++;
      } else {
        if (char === "!") lastBang = index;
        else if (char === "[") {
          stack.push({ open: index, image: lastBang === index - 1 });
          opens.push(index);
        } else if (char === "]") {
          const top = stack.pop();
          if (top) {
            const pair: BracketPair = {
              open: top.open,
              close: index,
              image: top.image,
              segmentEnd: segment.end,
            };
            pairAt.set(top.open, pair);
          }
          if (text[index + 1] === "(" && !top?.image) linkMarkers.push(index);
        }
        index++;
      }
    }
  }
  if (budget.exhausted) return finish();

  // --- Stage 3: resolve bracket pairs in source order. -----------------------
  // True when a bracket opens strictly between `from` and `to`.
  const opensBetween = (from: number, to: number) => {
    const next = firstFrom(opens, from + 1);
    return next !== undefined && next < to;
  };
  // End of a full, collapsed or shortcut reference whose text brackets span
  // `[open, close]`, when its label resolves to a definition. A label holds no
  // bracket, so only the innermost, pairwise disjoint pairs are ever sliced.
  const referenceEnd = (pair: BracketPair): number | undefined => {
    const { open, close } = pair;
    if (!budget.spend(4)) return undefined;
    const ownLabel = (): string | undefined => {
      if (
        close - open - 1 > MAX_REFERENCE_LABEL ||
        opensBetween(open, close) ||
        !budget.spend(close - open)
      )
        return undefined;
      return text.slice(open + 1, close);
    };
    if (text[close + 1] === "[") {
      const second = pairAt.get(close + 1);
      if (
        second !== undefined &&
        second.close - close - 2 <= MAX_REFERENCE_LABEL &&
        !opensBetween(close + 1, second.close) &&
        budget.spend(second.close - close)
      ) {
        const given = text.slice(close + 2, second.close),
          label = /\S/u.test(given) ? given : ownLabel();
        if (
          label !== undefined &&
          definitions.has(normalizeReferenceLabel(label))
        )
          return second.close + 1;
      }
    }
    const own = ownLabel();
    return own !== undefined && definitions.has(normalizeReferenceLabel(own))
      ? close + 1
      : undefined;
  };
  let consumed = 0;
  for (const open of opens) {
    if (open < consumed) continue;
    const pair = pairAt.get(open);
    if (pair === undefined) continue;
    const { close, image, segmentEnd } = pair,
      source: Source = { text, end: segmentEnd },
      start = image ? open - 1 : open;
    if (text[close + 1] === "(") {
      if (!budget.spend(2)) break;
      const parsed = parseDestination(source, close + 2, budget);
      if (budget.exhausted) break;
      if (parsed) {
        // P5: an image is consumed whole, so nothing inside its description
        // (links, images, URLs) is detected. P6: only a link label may not
        // hold another link, and an image inside a label is not a link.
        if (!image) {
          const marker = firstFrom(linkMarkers, open + 1);
          if (marker !== undefined && marker < close - 1) continue;
        }
        claims.push({ start, end: parsed.end });
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
        continue;
      }
    }
    // P7: a reference, collapsed or shortcut form whose label resolves to a
    // definition protects its whole range, image bang included. A reference
    // image is consumed like an inline image; the text of a reference link
    // may still hold an inline link, so scanning continues inside it.
    if (definitions.size === 0) continue;
    const end = referenceEnd(pair);
    if (end !== undefined) {
      claims.push({ start, end });
      if (image) consumed = end;
    }
    if (budget.exhausted) break;
  }
  return finish();
}
