// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  buildLayout,
  canonicalRangeForDisplay,
  convertBareUrls,
  convertTypedUrl,
  diffDisplay,
  linkAtCaret,
  markdownLinkFor,
  revealedStarts,
  scanMarkdown,
  shiftStarts,
} from "./prompt-markdown-links";

const URL = "https://example.com/a?b=1";
const LINK = `[${URL}](${URL})`;

it("converts a pasted bare URL and keeps the surrounding text", () => {
  const text = `see ${URL} now`;
  const result = convertBareUrls(text, 0, text.length)!;
  expect(result.text).toBe(`see ${LINK} now`);
  expect(result.count).toBe(1);
  expect(result.map(text.length)).toBe(result.text.length);
});

it("converts only the typed word and keeps trailing punctuation outside", () => {
  const text = `go ${URL}. `;
  const result = convertTypedUrl(text, text.length - 1)!;
  expect(result.text).toBe(`go ${LINK}. `);
});

it("leaves a URL without a delimiter and unrelated text untouched", () => {
  expect(convertTypedUrl("see plain text ", 14)).toBeUndefined();
  expect(convertBareUrls(`${URL} tail`, 0, 3)).toBeUndefined();
});

it.each([
  "www.example.com",
  "mailto:a@example.com",
  "javascript:alert(1)",
  "ftp://example.com/file",
  "https://user:secret@example.com/x",
  "https://",
  `https://example.com/${"a".repeat(2100)}`,
  "ahttps://example.com",
])("never converts %s", (input) => {
  const text = `${input} `;
  expect(convertTypedUrl(text, text.length - 1)).toBeUndefined();
});

it("skips links, images, code spans, fences and autolinks and is idempotent", () => {
  const text = [
    `[label](${URL}) ![img](${URL}) \`${URL}\``,
    "```",
    URL,
    "```",
    `<${URL}> [broken](${URL} "title")`,
  ].join("\n");
  expect(convertBareUrls(text, 0, text.length)).toBeUndefined();
  const once = convertBareUrls(`x ${URL} y`, 0, 3 + URL.length)!;
  expect(convertBareUrls(once.text, 0, once.text.length)).toBeUndefined();
});

it("escapes delimiters without changing the resolved URL", () => {
  const url = "https://example.com/a_(b)[c]*d~e";
  const link = markdownLinkFor(url);
  const scan = scanMarkdown(link);
  expect(scan.links).toHaveLength(1);
  expect(scan.links[0]!.url).toBe(url);
  expect(scan.links[0]!.end).toBe(link.length);
  const converted = convertBareUrls(`${url} `, 0, url.length)!;
  expect(converted.text).toBe(`${link} `);
});

it("rejects invalid Markdown links so they stay visible source", () => {
  for (const source of [
    "[a](javascript:alert(1))",
    "[](https://example.com)",
    "[a](https://user:pw@example.com)",
    "[a](www.example.com)",
    "[a] (https://example.com)",
    "[a](https://example.com",
    "[a\nb](https://example.com)",
  ])
    expect(scanMarkdown(source).links).toEqual([]);
});

it("maps offsets between canonical and display text", () => {
  const canonical = `a ${LINK} b ${LINK}`;
  const layout = buildLayout(canonical);
  expect(layout.display).toBe(`a ${URL} b ${URL}`);
  expect(layout.collapsed).toHaveLength(2);
  const first = layout.collapsed[0]!;
  expect(layout.toDisplay(first.start)).toBe(first.displayStart);
  expect(layout.toDisplay(first.end)).toBe(first.displayEnd);
  expect(layout.toCanonical(first.displayStart)).toBe(first.start);
  expect(layout.toCanonical(first.displayEnd)).toBe(first.end);
  expect(layout.toCanonical(first.displayStart + 3)).toBe(first.labelStart + 3);
  expect(layout.toCanonical(layout.display.length)).toBe(canonical.length);
});

it("reveals a link to its full source without changing canonical text", () => {
  const canonical = `a ${LINK} b`;
  const layout = buildLayout(canonical, [2]);
  expect(layout.display).toBe(canonical);
  expect(layout.collapsed).toEqual([]);
  expect(layout.toDisplay(5)).toBe(5);
});

it("reveals on interior carets and overlapping selections only", () => {
  const { links } = scanMarkdown(`a ${LINK} b`);
  const link = links[0]!;
  expect(revealedStarts(links, link.start, link.start)).toEqual([]);
  expect(revealedStarts(links, link.end, link.end)).toEqual([]);
  expect(revealedStarts(links, link.start + 1, link.start + 1)).toEqual([
    link.start,
  ]);
  expect(revealedStarts(links, 0, link.start)).toEqual([]);
  expect(revealedStarts(links, 0, link.start + 1)).toEqual([link.start]);
  expect(linkAtCaret(links, link.end)).toBe(link);
  expect(linkAtCaret(links, link.end + 1)).toBeUndefined();
});

it("maps display edits and expands edits that cross a link boundary", () => {
  const canonical = `ab ${LINK} cd`;
  const layout = buildLayout(canonical);
  const link = layout.collapsed[0]!;
  expect(canonicalRangeForDisplay(layout, 0, 1)).toEqual({ start: 0, end: 1 });
  expect(
    canonicalRangeForDisplay(
      layout,
      link.displayStart - 1,
      link.displayStart + 2,
    ),
  ).toEqual({ start: link.start - 1, end: link.end });
  expect(
    canonicalRangeForDisplay(layout, link.displayStart, link.displayEnd),
  ).toEqual({ start: link.start, end: link.end });
  expect(
    canonicalRangeForDisplay(
      layout,
      link.displayStart + 1,
      link.displayStart + 3,
    ),
  ).toEqual({ start: link.labelStart + 1, end: link.labelStart + 3 });
});

it("resolves repeated characters with the reported caret", () => {
  expect(diffDisplay("ab", "abb", 2)).toEqual({
    start: 1,
    end: 1,
    inserted: "b",
  });
  expect(diffDisplay("abc", "ac", 1)).toEqual({
    start: 1,
    end: 2,
    inserted: "",
  });
});

it("shifts retained link starts across a replacement", () => {
  expect(shiftStarts([2, 10, 20], { start: 5, end: 12 }, 1)).toEqual([2, 14]);
});

it("converts only unprotected URLs in a large paste with many protected shapes", () => {
  const shapes = "[a](b) `c` ".repeat(8000);
  const urls = Array.from(
    { length: 4000 },
    (_, index) => `https://example.com/${index}`,
  ).join(" ");
  const text = `${shapes}${urls} [x](https://inside.example.com) end`;
  const result = convertBareUrls(text, 0, text.length)!;
  expect(result.count).toBe(4000);
  expect(result.text.endsWith(" [x](https://inside.example.com) end")).toBe(
    true,
  );
  expect(result.text.startsWith(shapes)).toBe(true);
});

it.each([
  ["[see [docs]](https://example.com)", 0],
  ["![img [alt]](https://example.com/a.png)", 0],
  ["[a [b [c]]](https://example.com)", 0],
])(
  "protects a nested-bracket label shape without collapsing it: %s",
  (text) => {
    const scan = scanMarkdown(text);
    expect(scan.links).toEqual([]);
    expect(scan.protectedRanges).toEqual([{ start: 0, end: text.length }]);
    expect(convertBareUrls(text, 0, text.length)).toBeUndefined();
    const pasted = `${text} ${URL}`;
    expect(convertBareUrls(pasted, 0, pasted.length)!.text).toBe(
      `${text} ${LINK}`,
    );
  },
);

it("keeps escaped label brackets collapsible and recovers from unmatched openers", () => {
  const escaped = "[a \\[b\\]](https://example.com)";
  expect(scanMarkdown(escaped).links).toHaveLength(1);
  const unmatched = "[a [b](https://example.com)";
  const scan = scanMarkdown(unmatched);
  expect(scan.links).toHaveLength(1);
  expect(scan.links[0]!.start).toBe(3);
});

it("bounds scanning of unterminated and oversized link destinations", () => {
  const unterminated = "[a](".repeat(60000);
  expect(scanMarkdown(unterminated).links).toEqual([]);
  const long = `[a](${"x".repeat(20000)}) https://example.com`;
  const scan = scanMarkdown(long);
  expect(scan.links).toEqual([]);
  expect(scan.protectedRanges.length).toBeGreaterThan(0);
});

it("shows decoded label escapes and maps offsets across them", () => {
  const url = "https://[::1]/a";
  const canonical = `go ${markdownLinkFor(url)} now`;
  expect(canonical).toContain("https://\\[::1\\]/a");
  const layout = buildLayout(canonical);
  expect(layout.display).toBe(`go ${url} now`);
  const link = layout.collapsed[0]!;
  expect(layout.display.slice(link.displayStart, link.displayEnd)).toBe(url);
  const labelStart = link.labelStart;
  for (
    let display = link.displayStart + 1;
    display < link.displayEnd;
    display++
  ) {
    const canonicalOffset = layout.toCanonical(display);
    expect(layout.toDisplay(canonicalOffset)).toBe(display);
    expect(canonicalOffset).toBeGreaterThanOrEqual(labelStart);
    expect(canonicalOffset).toBeLessThanOrEqual(link.labelEnd);
  }
  // Offsets after the link stay aligned after the hidden syntax.
  expect(layout.toDisplay(canonical.length)).toBe(layout.display.length);
  expect(layout.toCanonical(layout.display.length)).toBe(canonical.length);
  // A position between a backslash and its escaped character rounds down.
  const bracket = canonical.indexOf("\\[");
  expect(layout.toDisplay(bracket + 1)).toBe(layout.toDisplay(bracket));
  // Replacing one decoded character replaces its whole escape sequence.
  const at = link.displayStart + "https://".length;
  expect(canonicalRangeForDisplay(layout, at, at + 1)).toEqual({
    start: bracket,
    end: bracket + 2,
  });
  const revealed = buildLayout(canonical, [link.start]);
  expect(revealed.display).toBe(canonical);
});

it("converts a URL that follows a closed fenced block", () => {
  const text = "```\ncode\n```\nhttps://example.com";
  expect(scanMarkdown(text).protectedRanges).toEqual([{ start: 0, end: 12 }]);
  expect(convertBareUrls(text, 0, text.length)!.text).toBe(
    "```\ncode\n```\n[https://example.com](https://example.com)",
  );
  const links = "```\ncode\n```\n[a](https://example.com)";
  expect(scanMarkdown(links).links).toHaveLength(1);
});

it("keeps the complete canonical source when links exceed the collapse limit", () => {
  const canonical = "[x](https://a) ".repeat(16384);
  const layout = buildLayout(canonical);
  expect(layout.links).toHaveLength(16384);
  expect(layout.collapsed).toHaveLength(16384);
  const limited = buildLayout(canonical, [], 256);
  expect(limited.collapseSuppressed).toBe(true);
  expect(limited.display).toBe(canonical);
  expect(limited.collapsed).toEqual([]);
  expect(limited.links).toHaveLength(16384);
  expect(limited.toDisplay(40)).toBe(40);
  expect(limited.toCanonical(40)).toBe(40);
  expect(limited.collapsedContaining(40)).toBeUndefined();
});

it.each([
  [255, 256, false],
  [256, 256, false],
  [257, 256, true],
  [3, 2, true],
  [2, 2, false],
  [1, 0, true],
])(
  "suppresses collapse for %s links at limit %s: %s",
  (count, limit, suppressed) => {
    const canonical = "[x](https://a) ".repeat(count);
    const layout = buildLayout(canonical, [], limit);
    expect(layout.collapseSuppressed).toBe(suppressed);
    expect(layout.collapsed).toHaveLength(suppressed ? 0 : count);
    expect(layout.display).toBe(suppressed ? canonical : "x ".repeat(count));
  },
);
