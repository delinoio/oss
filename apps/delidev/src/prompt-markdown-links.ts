// SPDX-License-Identifier: Apache-2.0
import { findUrlTokens, validHttpUrl } from "./external-links";

/**
 * Pure Markdown link model for prompt composers. The canonical draft is always
 * Markdown text; a layout derives the collapsed display text that the single
 * textarea shows and the offset maps between both coordinate systems.
 */

export interface TextRange {
  start: number;
  end: number;
}

/** A valid inline HTTP(S) Markdown link `[label](url)` in canonical text. */
export interface MarkdownLink extends TextRange {
  labelStart: number;
  labelEnd: number;
  url: string;
}

export interface MarkdownScan {
  links: MarkdownLink[];
  /** Sorted, disjoint ranges that bare URL conversion must never touch. */
  protectedRanges: TextRange[];
}

/** Prompts larger than this are never scanned; the composer limit is 256 KiB. */
const MAX_SCAN_LENGTH = 1_000_000;
const MAX_URL_LENGTH = 2048;
const FENCE_OPEN = /^ {0,3}(`{3,}|~{3,})[^\n]*(?:\n|$)/y;
const AUTOLINK = /<[A-Za-z][A-Za-z0-9+.-]*:[^\s<>]*>/y;
const ESCAPABLE = /\\([!-/:-@[-`{-~])/gu;

interface BracketShape extends TextRange {
  link?: MarkdownLink;
}

function parseBracketShape(
  text: string,
  open: number,
): BracketShape | undefined {
  let index = open + 1;
  for (;;) {
    const char = text[index];
    if (char === undefined || char === "\n" || char === "[") return;
    if (char === "\\") {
      index += 2;
      continue;
    }
    if (char === "]") break;
    index += 1;
  }
  const labelEnd = index;
  if (text[labelEnd + 1] !== "(") return;
  let depth = 0,
    cursor = labelEnd + 2;
  for (;;) {
    const char = text[cursor];
    if (char === undefined || char === "\n") return;
    if (char === "\\") {
      cursor += 2;
      continue;
    }
    if (char === "(") depth += 1;
    else if (char === ")") {
      if (depth === 0) break;
      depth -= 1;
    }
    cursor += 1;
  }
  const end = cursor + 1;
  const raw = text.slice(labelEnd + 2, cursor);
  const shape: BracketShape = { start: open, end };
  if (labelEnd > open + 1 && raw !== "" && !/[\s<>]/u.test(raw)) {
    const url = raw.replace(ESCAPABLE, "$1");
    if (url.length <= MAX_URL_LENGTH && validHttpUrl(url))
      shape.link = {
        start: open,
        end,
        labelStart: open + 1,
        labelEnd,
        url,
      };
  }
  return shape;
}

/**
 * Finds valid inline HTTP(S) links and every region where a bare URL must stay
 * untouched: link-shaped syntax (valid or not), images, inline code, fenced
 * code and autolinks.
 */
export function scanMarkdown(text: string): MarkdownScan {
  const links: MarkdownLink[] = [],
    protectedRanges: TextRange[] = [];
  if (text.length > MAX_SCAN_LENGTH) return { links, protectedRanges };
  const length = text.length;
  let index = 0;
  while (index < length) {
    const char = text[index];
    if (char === "\\") {
      index += 2;
      continue;
    }
    if (index === 0 || text[index - 1] === "\n") {
      FENCE_OPEN.lastIndex = index;
      const fence = FENCE_OPEN.exec(text);
      if (fence) {
        const marker = fence[1]!;
        let line = index + fence[0].length;
        let end = length;
        while (line < length) {
          const next = text.indexOf("\n", line);
          const stop = next === -1 ? length : next;
          const body = text.slice(line, stop);
          const close = /^ {0,3}(`{3,}|~{3,}) *$/u.exec(body);
          if (
            close &&
            close[1]![0] === marker[0] &&
            close[1]!.length >= marker.length
          ) {
            end = stop;
            break;
          }
          line = stop + 1;
        }
        protectedRanges.push({ start: index, end });
        index = end;
        continue;
      }
    }
    if (char === "`") {
      let run = index;
      while (text[run] === "`") run += 1;
      const size = run - index;
      let search = run;
      let closed = -1;
      while (search < length) {
        const found = text.indexOf("`", search);
        if (found === -1) break;
        let stop = found;
        while (text[stop] === "`") stop += 1;
        if (stop - found === size) {
          closed = stop;
          break;
        }
        search = stop;
      }
      if (closed !== -1) {
        protectedRanges.push({ start: index, end: closed });
        index = closed;
      } else index = run;
      continue;
    }
    if (char === "!" && text[index + 1] === "[") {
      const shape = parseBracketShape(text, index + 1);
      if (shape) {
        protectedRanges.push({ start: index, end: shape.end });
        index = shape.end;
        continue;
      }
    }
    if (char === "[") {
      const shape = parseBracketShape(text, index);
      if (shape) {
        protectedRanges.push({ start: shape.start, end: shape.end });
        if (shape.link) links.push(shape.link);
        index = shape.end;
        continue;
      }
    }
    if (char === "<") {
      AUTOLINK.lastIndex = index;
      const match = AUTOLINK.exec(text);
      if (match) {
        protectedRanges.push({ start: index, end: index + match[0].length });
        index += match[0].length;
        continue;
      }
    }
    index += 1;
  }
  return { links, protectedRanges };
}

/** Escapes link label delimiters; the label keeps the URL characters. */
export function escapeLinkLabel(url: string): string {
  return url.replace(/[\\[\]]/gu, "\\$&");
}

/** Escapes destination delimiters so the parsed destination equals the URL. */
export function escapeLinkDestination(url: string): string {
  return url.replace(/[\\()]/gu, "\\$&");
}

export function markdownLinkFor(url: string): string {
  return `[${escapeLinkLabel(url)}](${escapeLinkDestination(url)})`;
}

export interface UrlConversion {
  text: string;
  /** Number of URLs that were converted. */
  count: number;
  /** Maps an offset of the pre-conversion text to the converted text. */
  map: (offset: number) => number;
}

/**
 * Converts bare HTTP(S) URLs whose complete token lies inside `[from, to)` of
 * `text` to Markdown links. Detection reuses `findUrlTokens`, so the length,
 * credential and punctuation rules are the activatable-link rules. Tokens that
 * continue past `to`, sit inside existing links, images or code, or follow a
 * word character are left unchanged.
 */
export function convertBareUrls(
  text: string,
  from: number,
  to: number,
): UrlConversion | undefined {
  if (from >= to || text.length > MAX_SCAN_LENGTH) return;
  if (!/https?:\/\//iu.test(text.slice(from, to))) return;
  let windowEnd = to;
  while (windowEnd < text.length && !/\s/u.test(text[windowEnd]!))
    windowEnd += 1;
  const tokens = findUrlTokens(text.slice(from, windowEnd))
    .map((token) => ({
      start: token.start + from,
      end: token.end + from,
      url: token.url,
    }))
    .filter((token) => token.end <= to);
  if (!tokens.length) return;
  const { protectedRanges } = scanMarkdown(text);
  // Tokens and protected ranges are both sorted and disjoint, so one shared
  // cursor keeps the overlap test linear for large pastes.
  let rangeIndex = 0;
  const accepted = tokens.filter((token) => {
    while (
      rangeIndex < protectedRanges.length &&
      protectedRanges[rangeIndex]!.end <= token.start
    )
      rangeIndex += 1;
    const range = protectedRanges[rangeIndex];
    if (range && range.start < token.end) return false;
    const before = token.start === 0 ? "" : text[token.start - 1]!;
    return !/[\p{L}\p{N}_<]/u.test(before);
  });
  if (!accepted.length) return;
  let result = "",
    cursor = 0;
  const shifts: { at: number; delta: number }[] = [];
  for (const token of accepted) {
    const link = markdownLinkFor(token.url);
    result += text.slice(cursor, token.start) + link;
    cursor = token.end;
    shifts.push({
      at: token.end,
      delta: link.length - (token.end - token.start),
    });
  }
  result += text.slice(cursor);
  return {
    text: result,
    count: accepted.length,
    map: (offset) => {
      let delta = 0;
      for (const shift of shifts) if (shift.at <= offset) delta += shift.delta;
      return offset + delta;
    },
  };
}

/**
 * Converts the URL that a single typed delimiter at `delimiterAt` terminates.
 * `text` already contains the delimiter. Only the whitespace-delimited word
 * that ends at the delimiter is considered.
 */
export function convertTypedUrl(
  text: string,
  delimiterAt: number,
): UrlConversion | undefined {
  let from = delimiterAt;
  while (from > 0 && !/\s/u.test(text[from - 1]!)) from -= 1;
  return convertBareUrls(text, from, delimiterAt);
}

/** Collapsed link with its display coordinates. */
export interface CollapsedLink extends MarkdownLink {
  displayStart: number;
  displayEnd: number;
}

export interface PromptLayout {
  canonical: string;
  display: string;
  links: MarkdownLink[];
  collapsed: CollapsedLink[];
  toDisplay: (offset: number) => number;
  toCanonical: (offset: number) => number;
  /** Collapsed link whose display range strictly contains the offset. */
  collapsedContaining: (displayOffset: number) => CollapsedLink | undefined;
}

export function buildLayout(
  canonical: string,
  revealed: readonly number[] = [],
): PromptLayout {
  const links = scanMarkdown(canonical).links;
  const open = new Set(revealed);
  const collapsed: CollapsedLink[] = [];
  let display = "",
    cursor = 0;
  for (const link of links) {
    if (open.has(link.start)) continue;
    display += canonical.slice(cursor, link.start);
    const displayStart = display.length;
    display += canonical.slice(link.labelStart, link.labelEnd);
    collapsed.push({ ...link, displayStart, displayEnd: display.length });
    cursor = link.end;
  }
  display += canonical.slice(cursor);
  const hiddenOf = (link: MarkdownLink) =>
    link.end - link.start - (link.labelEnd - link.labelStart);
  return {
    canonical,
    display,
    links,
    collapsed,
    toDisplay: (offset) => {
      let hidden = 0;
      for (const link of collapsed) {
        if (offset <= link.start) break;
        if (offset >= link.end) {
          hidden += hiddenOf(link);
          continue;
        }
        if (offset <= link.labelStart) return link.displayStart;
        if (offset >= link.labelEnd) return link.displayEnd;
        return link.displayStart + (offset - link.labelStart);
      }
      return offset - hidden;
    },
    toCanonical: (offset) => {
      let hidden = 0;
      for (const link of collapsed) {
        if (offset <= link.displayStart) break;
        if (offset >= link.displayEnd) {
          hidden += hiddenOf(link);
          continue;
        }
        return link.labelStart + (offset - link.displayStart);
      }
      return offset + hidden;
    },
    collapsedContaining: (offset) =>
      collapsed.find(
        (link) => offset > link.displayStart && offset < link.displayEnd,
      ),
  };
}

export interface DisplayEdit {
  /** Replaced range of the previous display text. */
  start: number;
  end: number;
  /** Replacement text taken from the new display text. */
  inserted: string;
}

/**
 * Diffs two display strings. The caret that follows the edit bounds the
 * unchanged suffix, so repeated characters resolve to the typed position.
 */
export function diffDisplay(
  before: string,
  after: string,
  caret: number,
): DisplayEdit {
  const limit = Math.min(before.length, after.length);
  let suffix = 0;
  const maxSuffix = Math.min(limit, Math.max(0, after.length - caret));
  while (
    suffix < maxSuffix &&
    before[before.length - 1 - suffix] === after[after.length - 1 - suffix]
  )
    suffix += 1;
  let prefix = 0;
  while (prefix < limit - suffix && before[prefix] === after[prefix])
    prefix += 1;
  return {
    start: prefix,
    end: before.length - suffix,
    inserted: after.slice(prefix, after.length - suffix),
  };
}

/**
 * Maps a display range to the canonical range an edit replaces. An edit that
 * stays inside one collapsed label edits that label. An edit that crosses a
 * link boundary covers the whole link, so no half link is ever left behind.
 */
export function canonicalRangeForDisplay(
  layout: PromptLayout,
  start: number,
  end: number,
): TextRange {
  const first = layout.collapsedContaining(start),
    last = layout.collapsedContaining(end);
  if (first && first === last)
    return {
      start: layout.toCanonical(start),
      end: layout.toCanonical(end),
    };
  return {
    start: first ? first.start : layout.toCanonical(start),
    end: last ? last.end : layout.toCanonical(end),
  };
}

/**
 * Link starts that the selection reveals: a caret strictly inside a link, or a
 * non-empty selection that overlaps it. Touching an edge does not reveal.
 */
export function revealedStarts(
  links: readonly MarkdownLink[],
  start: number,
  end: number,
): number[] {
  return links
    .filter((link) =>
      start === end
        ? link.start < start && start < link.end
        : start < link.end && end > link.start,
    )
    .map((link) => link.start);
}

/** The link whose source range contains a collapsed caret, edges included. */
export function linkAtCaret(
  links: readonly MarkdownLink[],
  caret: number,
): MarkdownLink | undefined {
  return links.find((link) => link.start <= caret && caret <= link.end);
}

export function sameStarts(
  a: readonly number[],
  b: readonly number[],
): boolean {
  return a.length === b.length && a.every((value, index) => value === b[index]);
}

/** Moves retained link starts across one canonical replacement. */
export function shiftStarts(
  starts: readonly number[],
  replaced: TextRange,
  insertedLength: number,
): number[] {
  const delta = insertedLength - (replaced.end - replaced.start);
  return starts.flatMap((start) =>
    start >= replaced.end
      ? [start + delta]
      : start < replaced.start
        ? [start]
        : [],
  );
}
