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
const ESCAPED_CHAR = /^[!-/:-@[-`{-~]$/u;

interface BracketShape extends TextRange {
  link?: MarkdownLink;
}

/** Longest destination scanned after `](`; keeps unterminated shapes bounded. */
const MAX_DESTINATION_SCAN = 4 * MAX_URL_LENGTH;

/**
 * Pairs every unescaped `[` with its matching `]` on the same line in one
 * linear pass, so nested labels such as `[see [docs]](url)` resolve in O(1) per
 * opener. `-1` marks an unmatched opener.
 */
function matchBrackets(text: string): Int32Array {
  const closes = new Int32Array(text.length).fill(-1);
  const stack: number[] = [];
  for (let index = 0; index < text.length; index += 1) {
    const char = text[index];
    if (char === "\\") index += 1;
    else if (char === "\n") stack.length = 0;
    else if (char === "[") stack.push(index);
    else if (char === "]") {
      const open = stack.pop();
      if (open !== undefined) closes[open] = index;
    }
  }
  return closes;
}

function hasUnescapedBracket(text: string, from: number, to: number): boolean {
  for (let index = from; index < to; index += 1) {
    const char = text[index];
    if (char === "\\") index += 1;
    else if (char === "[" || char === "]") return true;
  }
  return false;
}

function parseBracketShape(
  text: string,
  open: number,
  closes: Int32Array,
): BracketShape | undefined {
  const labelEnd = closes[open]!;
  if (labelEnd === -1 || text[labelEnd + 1] !== "(") return;
  let depth = 0,
    cursor = labelEnd + 2;
  const limit = Math.min(text.length, cursor + MAX_DESTINATION_SCAN);
  for (;;) {
    if (cursor >= limit) {
      // Too long to be a URL destination: protect the scanned prefix and never
      // rescan it, so adversarial input stays linear. It is never a link.
      return cursor >= text.length ? undefined : { start: open, end: cursor };
    }
    const char = text[cursor];
    if (char === "\n") return;
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
  // Only a label without nested brackets is a collapsible link. A nested label
  // is still link-shaped syntax, so its destination stays protected as source.
  if (
    labelEnd > open + 1 &&
    raw !== "" &&
    !/[\s<>]/u.test(raw) &&
    !hasUnescapedBracket(text, open + 1, labelEnd)
  ) {
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
  const closes = matchBrackets(text);
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
      const shape = parseBracketShape(text, index + 1, closes);
      if (shape) {
        protectedRanges.push({ start: index, end: shape.end });
        index = shape.end;
        continue;
      }
    }
    if (char === "[") {
      const shape = parseBracketShape(text, index, closes);
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
  /**
   * Canonical offset, relative to `labelStart`, of every displayed label
   * character plus the label end. Absent when the label has no escapes.
   */
  labelOffsets?: number[];
}

export interface PromptLayout {
  canonical: string;
  display: string;
  links: MarkdownLink[];
  collapsed: CollapsedLink[];
  /**
   * True when the parsed links exceeded the collapse budget, so the display is
   * the complete canonical source and nothing is collapsed.
   */
  collapseSuppressed: boolean;
  toDisplay: (offset: number) => number;
  toCanonical: (offset: number) => number;
  /** Collapsed link whose display range strictly contains the offset. */
  collapsedContaining: (displayOffset: number) => CollapsedLink | undefined;
}

/** Decodes label escapes; `offsets[i]` is the canonical start of display char i. */
function decodeLabel(label: string): { text: string; offsets?: number[] } {
  if (!label.includes("\\")) return { text: label };
  let text = "";
  const offsets: number[] = [];
  for (let index = 0; index < label.length;) {
    offsets.push(index);
    if (label[index] === "\\" && ESCAPED_CHAR.test(label[index + 1] ?? "")) {
      text += label[index + 1];
      index += 2;
      continue;
    }
    text += label[index];
    index += 1;
  }
  offsets.push(label.length);
  return { text, offsets };
}

/** Display index of a canonical offset inside a label; mid-escape rounds down. */
function labelDisplayIndex(link: CollapsedLink, relative: number): number {
  const offsets = link.labelOffsets;
  if (!offsets) return relative;
  let low = 0,
    high = offsets.length - 1;
  while (low < high) {
    const middle = (low + high + 1) >> 1;
    if (offsets[middle]! <= relative) low = middle;
    else high = middle - 1;
  }
  return low;
}

/**
 * Collapse state held fixed while an IME composition owns the textarea. The
 * display text must not change under the IME, so the collapse decision is
 * taken once, before composition, and not re-derived from the changing draft.
 */
export interface FrozenCollapse {
  /** True when the whole draft was plain source before composition. */
  suppressed: boolean;
  /**
   * Starts of the links that were collapsed before composition, shifted across
   * composed edits. A link that is not listed (for example one completed while
   * composing) stays plain source.
   */
  collapsibleStarts: readonly number[];
}

/**
 * Builds the display layout. `collapseLimit` is the most links the presentation
 * may decorate. When the draft parses more links than that, the whole draft
 * stays plain source (display equals canonical, nothing collapsed) instead of
 * collapsing an arbitrary prefix; `links` still reports every parsed link.
 * A `frozen` state replaces that decision entirely, so neither the link count
 * nor the limit can change what is collapsed until the freeze is lifted.
 */
export function buildLayout(
  canonical: string,
  revealed: readonly number[] = [],
  collapseLimit = Number.POSITIVE_INFINITY,
  frozen?: FrozenCollapse,
): PromptLayout {
  const links = scanMarkdown(canonical).links;
  const collapseSuppressed = frozen
    ? frozen.suppressed
    : links.length > collapseLimit;
  const open = new Set(revealed);
  const collapsible = frozen ? new Set(frozen.collapsibleStarts) : undefined;
  const collapsed: CollapsedLink[] = [];
  let display = "",
    cursor = 0;
  for (const link of collapseSuppressed ? [] : links) {
    if (open.has(link.start)) continue;
    if (collapsible && !collapsible.has(link.start)) continue;
    display += canonical.slice(cursor, link.start);
    const displayStart = display.length;
    const label = decodeLabel(canonical.slice(link.labelStart, link.labelEnd));
    display += label.text;
    collapsed.push({
      ...link,
      displayStart,
      displayEnd: display.length,
      labelOffsets: label.offsets,
    });
    cursor = link.end;
  }
  display += canonical.slice(cursor);
  const hiddenOf = (link: CollapsedLink) =>
    link.end - link.start - (link.displayEnd - link.displayStart);
  return {
    canonical,
    display,
    links,
    collapsed,
    collapseSuppressed,
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
        return (
          link.displayStart + labelDisplayIndex(link, offset - link.labelStart)
        );
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
        const relative = offset - link.displayStart;
        return link.labelStart + (link.labelOffsets?.[relative] ?? relative);
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
