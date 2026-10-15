// SPDX-License-Identifier: Apache-2.0
// The precedence table of the prompt composer Markdown links contract, as
// tests. Part one asserts one row per construct and per interaction. Part two
// is a differential test: a slow reference model written from the documented
// precedence alone (no shared helper, no budget, quadratic scans) must agree
// with the production scanner on seeded random inputs drawn from the Markdown
// metacharacters, so a remaining cell of the state machine is found by this
// test instead of in review.
import { describe, expect, it } from "vitest";
import { validHttpUrl } from "./external-links";
import { convertBareUrls, scanMarkdown } from "./prompt-markdown-links";

const convert = (text: string) => convertBareUrls(text, 0, text.length).text;
const urls = (text: string) => scanMarkdown(text).links.map((link) => link.url);
const isProtected = (text: string, needle: string, from = 0) => {
  const at = text.indexOf(needle, from);
  expect(at).toBeGreaterThanOrEqual(0);
  return scanMarkdown(text).protectedRanges.some(
    (range) => range.start <= at && range.end >= at + needle.length,
  );
};

describe("precedence table", () => {
  it("P1 fenced code block outranks every inline construct and definition", () => {
    const text = "```\n[a]: /u\n[x](https://a.test) `\n```\n\n![y][a]";
    expect(urls(text)).toEqual([]);
    expect(isProtected(text, "[a]: /u")).toBe(true);
    // The definition inside the fence defines nothing for the reference.
    expect(convert("```\n[a]: /u\n```\n\n[see https://a.test x][a]")).toBe(
      "```\n[a]: /u\n```\n\n[see [https://a.test](https://a.test) x][a]",
    );
    // A fence opening interrupts a code span instead of being swallowed by it.
    expect(convert("`a\n```\nb\n```\nhttps://a.test`")).toBe(
      "`a\n```\nb\n```\n[https://a.test](https://a.test)`",
    );
  });

  it("P2 a definition outranks a code span only where it starts outside one", () => {
    expect(isProtected("[a]: https://a.test `x`", "https://a.test")).toBe(true);
    // A line start inside a code span is not a definition line.
    expect(convert("`a\n[a]: /u`\n\n[see https://a.test x][a]")).toBe(
      "`a\n[a]: /u`\n\n[see [https://a.test](https://a.test) x][a]",
    );
    expect(convert("[a]: /u\n\n[see https://a.test x][a]")).toBe(
      "[a]: /u\n\n[see https://a.test x][a]",
    );
  });

  it("P2 a definition claims its destination and title: no link starts inside", () => {
    for (const text of [
      '[a]: <x> "[b](https://b.test)"',
      "[a]: /x[b](https://b.test)",
      "[a]: /x\n  '[b](https://b.test) t'",
      // A rejected definition keeps its destination protected when indented
      // by tabs, as it does when indented by spaces.
      '\t[a]: https://a.test "unterminated',
      ' \t[a]: https://a.test "unterminated',
    ]) {
      expect(urls(text)).toEqual([]);
      expect(convert(text)).toBe(text);
    }
    expect(urls("[a]: /x\n\n[b](https://b.test)")).toEqual(["https://b.test"]);
  });

  it("P3 a code span closes at the next backtick run of equal length and ignores backslashes", () => {
    // The backslash is literal inside code, so the second backtick closes it.
    expect(convert("`x\\` `https://a.test`")).toBe("`x\\` `https://a.test`");
    expect(convert("`x\\` https://a.test")).toBe(
      "`x\\` [https://a.test](https://a.test)",
    );
    // Outside code a backslash makes the next backtick literal.
    expect(convert("\\`https://a.test`")).toBe(
      "\\`[https://a.test](https://a.test)`",
    );
    expect(convert("``a ` https://a.test``")).toBe("``a ` https://a.test``");
    // An unmatched run is literal text and never retried shorter.
    expect(convert("``a ` https://a.test")).toBe(
      "``a ` [https://a.test](https://a.test)",
    );
  });

  it("P3 a code span never crosses a blank line", () => {
    expect(convert("`a\n\nhttps://a.test`")).toBe(
      "`a\n\n[https://a.test](https://a.test)`",
    );
  });

  it("P3 a code span outranks a link or image bracket inside it", () => {
    expect(urls("`[a](https://a.test)`")).toEqual([]);
    expect(urls("[`](x)`](https://a.test)")).toEqual(["https://a.test"]);
  });

  it("P4 an autolink or raw HTML tag outranks a link bracket inside it", () => {
    expect(urls('<a href="[x](https://a.test)">')).toEqual([]);
    expect(isProtected('<a href="https://a.test">', "https://a.test")).toBe(
      true,
    );
    expect(convert("\\<https://a.test>")).toBe(
      "\\<[https://a.test](https://a.test)>",
    );
  });

  it("P5 an image is consumed whole, inline or by reference", () => {
    expect(urls("![a [b](https://b.test)](i.png)")).toEqual([]);
    const reference = "[logo]: i.png\n\n![see [docs](https://d.test)][logo]";
    expect(urls(reference)).toEqual([]);
    expect(isProtected(reference, "![see [docs](https://d.test)][logo]")).toBe(
      true,
    );
    // The bang is part of the image unless an odd run of backslashes escapes it.
    expect(urls("\\![a](https://a.test)")).toEqual(["https://a.test"]);
    expect(urls("\\\\![a](https://a.test)")).toEqual([]);
  });

  it("P6 an inline link needs a label without a nested link; an image does not count", () => {
    expect(urls("[a ![i](i.png)](https://a.test)")).toEqual(["https://a.test"]);
    expect(urls("[a [b](https://b.test)](https://a.test)")).toEqual([
      "https://b.test",
    ]);
    expect(urls("[![i](i.png) [b](https://b.test)](https://a.test)")).toEqual([
      "https://b.test",
    ]);
    // An image bracket pair is not a link marker even when unmatched brackets
    // surround it.
    expect(urls("[a ![i](i.png) b](https://a.test)")).toEqual([
      "https://a.test",
    ]);
  });

  it("P6 a backslash escapes only punctuation: before whitespace it ends the destination", () => {
    expect(urls("[a](https://a.test\\ more)")).toEqual([]);
    expect(urls("[a](https://a.test\\\tmore) [b](https://b.test)")).toEqual([
      "https://b.test",
    ]);
    // The malformed construct claims nothing, so the URL in it converts.
    expect(convert("[a]( https://a.test\\ more)")).toBe(
      "[a]( [https://a.test](https://a.test)\\ more)",
    );
    // An escaped punctuation character stays inside the destination.
    expect(urls("[a](https://a.test/\\) b)")).toEqual([]);
    expect(urls("[a](https://a.test/\\))")).toEqual(["https://a.test/)"]);
  });

  it("P6 a title is scanned up to the protected-span limit", () => {
    const title = (count: number) =>
      `[a](https://a.test "${"w ".repeat(count)}https://t.test") https://c.test`;
    // A 1000-unit title is part of the link syntax, so its URL stays plain.
    expect(convert(title(500))).toBe(
      title(500).replace("https://c.test", "[https://c.test](https://c.test)"),
    );
    // Beyond the limit the title is abandoned and the syntax is plain text.
    expect(convert(title(2100))).toContain("[https://t.test](https://t.test)");
  });

  it("P7 a reference form is protected only when its label has a definition", () => {
    const text = (label: string) =>
      `[a]: /u\n\n[see https://a.test x][${label}]`;
    expect(convert(text("A"))).toBe(text("A"));
    expect(convert(text("b"))).toContain("[https://a.test](https://a.test)");
    // A reference link keeps the link nested in its text; an image does not.
    expect(urls("[a]: /u\n\n[x [n](https://n.test)][a]")).toEqual([
      "https://n.test",
    ]);
    expect(urls("[a]: /u\n\n![x [n](https://n.test)][a]")).toEqual([]);
  });

  it("P8 a bare URL converts only outside every earlier claim", () => {
    for (const text of [
      "[a](https://a.test)",
      "![a](https://a.test)",
      "`https://a.test`",
      "```\nhttps://a.test\n```",
      "<https://a.test>",
      "[a]: https://a.test",
      "[https://a.test x][a]\n\n[a]: /u",
    ])
      expect(convert(text)).toBe(text);
    expect(convert("x https://a.test y")).toBe(
      "x [https://a.test](https://a.test) y",
    );
  });

  it("a construct never crosses a blank line", () => {
    expect(urls("[a\n\nb](https://a.test)")).toEqual([]);
    expect(convert("[a]: /u\n\n[see https://a.test x][a]")).toBe(
      "[a]: /u\n\n[see https://a.test x][a]",
    );
    expect(convert("[a](\n\nhttps://a.test)")).toBe(
      "[a](\n\n[https://a.test](https://a.test))",
    );
  });
});

// --- Reference model ----------------------------------------------------------

interface ModelLink {
  start: number;
  end: number;
  labelStart: number;
  labelEnd: number;
  label: string;
  url: string;
}
interface ModelScan {
  links: ModelLink[];
  mask: boolean[];
}

const AUTOLINK = /^<[A-Za-z][A-Za-z0-9+.-]{1,31}:[^\s<>]*>/u;
const HTML_TAG =
  /^<\/?[A-Za-z][A-Za-z0-9-]*(?:\s+[A-Za-z_:][\w:.-]*(?:\s*=\s*(?:[^\s"'=<>`]+|'[^']*'|"[^"]*"))?)*\s*\/?>/u;
const normalize = (label: string) =>
  label.trim().replace(/\s+/gu, " ").toLowerCase().toUpperCase();
const skipBlanks = (t: string, from: number) => {
  let i = from;
  while (t[i] === " " || t[i] === "\t") i++;
  return i;
};
const restBlank = (t: string, from: number) => {
  const i = skipBlanks(t, from);
  return i >= t.length || t[i] === "\n";
};

/** Strict definition at the `[` of a line, or null. `t` ends at the segment. */
function modelStrictDefinition(t: string, b: number) {
  let close = -1;
  for (let i = b + 1; i < t.length; i++) {
    if (t[i] === "\\") {
      i++;
      continue;
    }
    if (t[i] === "[") return null;
    if (t[i] === "]") {
      close = i;
      break;
    }
  }
  if (close < 0 || t[close + 1] !== ":") return null;
  const label = t.slice(b + 1, close);
  if (label.length > 999 || !/\S/u.test(label)) return null;
  if (/\n[ \t]*(\n|$)/u.test(label)) return null;
  let i = skipBlanks(t, close + 2);
  if (t[i] === "\n") {
    i = skipBlanks(t, i + 1);
    if (i >= t.length || t[i] === "\n") return null;
  }
  let destEnd: number;
  if (t[i] === "<") {
    let j = i + 1;
    while (j < t.length && t[j] !== ">") {
      if (t[j] === "\n" || t[j] === "<") return null;
      if (t[j] === "\\") {
        if (t[j + 1] === "\n") return null;
        j++;
      }
      j++;
    }
    if (t[j] !== ">") return null;
    destEnd = j + 1;
  } else {
    let j = i,
      depth = 0;
    for (; j < t.length; j++) {
      const c = t[j]!;
      if (c === "\\" && !/\s/u.test(t[j + 1] ?? " ")) {
        j++;
        continue;
      }
      if (/[\s\u0000-\u001f\u007f]/u.test(c)) break;
      if (c === "(") depth++;
      else if (c === ")") {
        if (depth === 0) break;
        depth--;
      }
    }
    if (j === i || depth !== 0) return null;
    destEnd = j;
  }
  const after = skipBlanks(t, destEnd),
    endsLine = after >= t.length || t[after] === "\n";
  let titleOpen = -1;
  if (endsLine) {
    if (after < t.length) {
      const next = skipBlanks(t, after + 1);
      if (next < t.length && t[next] !== "\n") titleOpen = next;
    }
  } else if (after > destEnd) titleOpen = after;
  else return null;
  if (titleOpen >= 0) {
    const opener = t[titleOpen]!;
    if (opener === '"' || opener === "'" || opener === "(") {
      const closer = opener === "(" ? ")" : opener;
      let titleEnd = -1;
      for (let j = titleOpen + 1; j < t.length; j++) {
        if (t[j] === "\\") j++;
        else if (t[j] === closer) {
          titleEnd = j + 1;
          break;
        } else if (opener === "(" && t[j] === "(") break;
        else if (t[j] === "\n" && restBlank(t, j + 1)) break;
      }
      if (titleEnd >= 0 && restBlank(t, titleEnd))
        return { end: titleEnd, label };
    }
  }
  return endsLine ? { end: destEnd, label } : null;
}

function modelDefinition(t: string, lineStart: number) {
  const b = skipBlanks(t, lineStart);
  if (t[b] !== "[" || b - lineStart > 3) return null;
  const strict = modelStrictDefinition(t, b);
  if (strict) return strict;
  const loose = /^[ \t]{0,3}\[([^\]\n]+)\]:[ \t]*\S+/u.exec(
    t.slice(
      lineStart,
      t.indexOf("\n", lineStart) < 0 ? undefined : t.indexOf("\n", lineStart),
    ),
  );
  return loose ? { end: lineStart + loose[0].length, label: loose[1]! } : null;
}

/** `[start, end)` of every segment and the fence claims between them. */
function modelBlocks(text: string) {
  const lines: [number, number][] = [];
  for (let at = 0; ;) {
    const nl = text.indexOf("\n", at);
    if (nl < 0) {
      lines.push([at, text.length]);
      break;
    }
    lines.push([at, nl]);
    at = nl + 1;
  }
  const fences: [number, number][] = [],
    segments: [number, number][] = [];
  let open: { char: string; length: number; start: number } | null = null,
    current: [number, number] | null = null;
  for (const [start, end] of lines) {
    const line = text.slice(start, end);
    if (open) {
      const m = /^ {0,3}(`{3,}|~{3,})[ \t]*$/u.exec(line);
      if (m && m[1]![0] === open.char && m[1]!.length >= open.length) {
        fences.push([open.start, end]);
        open = null;
      }
      continue;
    }
    const m = /^ {0,3}(`{3,}|~{3,})(.*)$/u.exec(line);
    if (m && !(m[1]![0] === "`" && m[2]!.includes("`"))) {
      if (current) segments.push(current);
      current = null;
      open = { char: m[1]![0]!, length: m[1]!.length, start };
    } else if (/^[ \t]*$/u.test(line)) {
      if (current) segments.push(current);
      current = null;
    } else current = [current ? current[0] : start, end];
  }
  if (open) fences.push([open.start, text.length]);
  if (current) segments.push(current);
  return { fences, segments };
}

function modelDestination(t: string, from: number) {
  let i = from;
  const ws = () => {
    while (t[i] === " " || t[i] === "\t" || t[i] === "\n") i++;
  };
  ws();
  const destStart = i,
    angle = t[i] === "<";
  let destEnd: number;
  if (angle) {
    i++;
    while (i < t.length && t[i] !== ">") {
      if (t[i] === "\n" || t[i] === "<") return null;
      i += t[i] === "\\" ? 2 : 1;
    }
    if (t[i] !== ">") return null;
    i++;
    destEnd = i;
  } else {
    let depth = 0;
    while (i < t.length) {
      const c = t[i]!;
      // A backslash escapes only ASCII punctuation; before whitespace (or the
      // end of the segment) it is a literal character and the run ends there.
      if (c === "\\" && /[!-/:-@[-`{-~]/u.test(t[i + 1] ?? "")) {
        i += 2;
        continue;
      }
      if (/\s/u.test(c)) break;
      if (c === "(") depth++;
      else if (c === ")") {
        if (depth === 0) break;
        depth--;
      }
      i++;
    }
    i = destEnd = Math.min(i, t.length);
  }
  ws();
  const touched = i !== destEnd;
  if (t[i] === '"' || t[i] === "'" || t[i] === "(") {
    const closer = t[i] === "(" ? ")" : t[i]!;
    i++;
    while (i < t.length && t[i] !== closer) i += t[i] === "\\" ? 2 : 1;
    if (t[i] !== closer) return null;
    i++;
    ws();
  }
  if (t[i] !== ")") return null;
  return {
    destStart,
    destEnd,
    end: i + 1,
    simple: !angle && destStart === from && !touched && destEnd > destStart,
  };
}

function referenceModel(text: string): ModelScan {
  const mask = new Array<boolean>(text.length).fill(false);
  const claim = (start: number, end: number) => {
    for (let i = start; i < Math.min(end, text.length); i++) mask[i] = true;
  };
  const { fences, segments } = modelBlocks(text);
  for (const [start, end] of fences) claim(start, end);

  const definitions = new Set<string>();
  const opens: number[] = [];
  const pairs = new Map<
    number,
    { close: number; image: boolean; segmentEnd: number }
  >();
  const linkMarkers: number[] = [];
  for (const [segStart, segEnd] of segments) {
    const t = text.slice(0, segEnd);
    const stack: { open: number; image: boolean }[] = [];
    let lastBang = -9,
      i = segStart;
    while (i < segEnd) {
      if (i === 0 || text[i - 1] === "\n") {
        const def = modelDefinition(t, i);
        if (def) {
          claim(i, def.end);
          definitions.add(normalize(def.label));
          i = def.end;
          continue;
        }
      }
      const c = text[i]!;
      if (c === "\\") {
        i += 2;
      } else if (c === "`") {
        let runEnd = i;
        while (text[runEnd] === "`") runEnd++;
        const length = runEnd - i;
        let closerEnd = -1;
        for (let j = runEnd; j < segEnd && closerEnd < 0; j++) {
          if (text[j] !== "`" || text[j - 1] === "`") continue;
          let k = j;
          while (k < segEnd && text[k] === "`") k++;
          if (k - j === length) closerEnd = k;
        }
        if (closerEnd >= 0) {
          claim(i, closerEnd);
          i = closerEnd;
        } else i = runEnd;
      } else if (c === "<") {
        const head = text.slice(i, Math.min(segEnd, i + 2100));
        const tag = AUTOLINK.exec(head) ?? HTML_TAG.exec(head);
        if (tag) {
          claim(i, i + tag[0].length);
          i += tag[0].length;
        } else i++;
      } else {
        if (c === "!") lastBang = i;
        if (c === "[") {
          stack.push({ open: i, image: lastBang === i - 1 });
          opens.push(i);
        }
        if (c === "]") {
          const top = stack.pop();
          if (top)
            pairs.set(top.open, {
              close: i,
              image: top.image,
              segmentEnd: segEnd,
            });
          if (text[i + 1] === "(" && !top?.image) linkMarkers.push(i);
        }
        i++;
      }
    }
  }

  const links: ModelLink[] = [];
  const known = (label: string | undefined) =>
    label !== undefined && definitions.has(normalize(label));
  const ownLabel = (open: number, close: number) =>
    close - open - 1 <= 999 && !opens.some((o) => o > open && o < close)
      ? text.slice(open + 1, close)
      : undefined;
  const referenceEnd = (open: number, pair: { close: number }) => {
    const { close } = pair;
    const second = text[close + 1] === "[" ? pairs.get(close + 1) : undefined;
    if (
      second &&
      second.close - close - 2 <= 999 &&
      !opens.some((o) => o > close + 1 && o < second.close)
    ) {
      const given = text.slice(close + 2, second.close);
      if (known(/\S/u.test(given) ? given : ownLabel(open, close)))
        return second.close + 1;
    }
    return known(ownLabel(open, close)) ? close + 1 : undefined;
  };
  let consumed = 0;
  for (const open of opens) {
    if (open < consumed) continue;
    const pair = pairs.get(open);
    if (!pair) continue;
    const { close, image } = pair,
      start = image ? open - 1 : open,
      t = text.slice(0, pair.segmentEnd);
    if (text[close + 1] === "(") {
      const dest = modelDestination(t, close + 2);
      if (dest) {
        if (!image && linkMarkers.some((m) => m > open && m < close - 1))
          continue;
        claim(start, dest.end);
        consumed = dest.end;
        if (image || !dest.simple) continue;
        const raw = text.slice(dest.destStart, dest.destEnd);
        const url = raw.replace(/\\([!-/:-@[-`{-~])/gu, "$1");
        let label = "";
        for (let i = open + 1; i < close; i++) {
          if (
            text[i] === "\\" &&
            i + 1 < close &&
            /[!-/:-@[-`{-~]/u.test(text[i + 1]!)
          ) {
            label += text[i + 1];
            i++;
          } else label += text[i];
        }
        if (
          url.length > 2048 ||
          !validHttpUrl(url) ||
          /[\u0000- \u007f]/u.test(url) ||
          !/\S/u.test(label) ||
          label.includes("\n")
        )
          continue;
        links.push({
          start,
          end: dest.end,
          labelStart: open + 1,
          labelEnd: close,
          label,
          url,
        });
        continue;
      }
    }
    if (definitions.size === 0) continue;
    const end = referenceEnd(open, pair);
    if (end !== undefined) {
      claim(start, end);
      if (image) consumed = end;
    }
  }
  return { links, mask };
}

// --- Differential test ----------------------------------------------------------

const TOKENS = [
  "[",
  "]",
  "(",
  ")",
  "!",
  "\\",
  "`",
  "``",
  "<",
  ">",
  " ",
  "\n",
  "\n\n",
  '"',
  "'",
  ":",
  "a",
  "x y",
  "https://a.test",
  "](https://b.test)",
  "](u)",
  "[a]:",
  "[a]: /u",
  "[a]",
  "[a][a]",
  "![a](i.png)",
  "```\n",
  "~~~\n",
  "<https://c.test>",
  "<b>",
  "\t",
  "  ",
];

// Whole constructs, mixed in so a random draft reaches nested and adjacent
// shapes (links in definitions, images in labels, references to definitions).
const PHRASES = [
  "[a](https://b.test)",
  "[x y](https://c.test)",
  "[a [b](https://c.test)](https://d.test)",
  "[a](https://e.test\\ more)",
  "[a](https://e.test\\\tmore)",
  "[a](https://e.test\\\nmore)",
  "![a](i.png\\ more)",
  "[open ![i](i.png)](https://d.test)",
  "![x [y](https://c.test)](i.png)",
  "![x][a]",
  "[x][a]",
  "[x][]",
  "[a]: /u\n",
  '\t[a]: https://e.test "unterminated\n',
  " \t[a]: /u\n",
  '[a]: <x> "t"\n',
  "[a]:\n  /u\n  'T'\n",
  "`code`",
  '<a href="x">',
  "\n\n[a]: /u\n\n",
];

function pickToken(next: () => number) {
  return next() < 0.35
    ? PHRASES[Math.floor(next() * PHRASES.length)]!
    : TOKENS[Math.floor(next() * TOKENS.length)]!;
}

function generator(seed: number) {
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}

function maskOf(text: string, ranges: { start: number; end: number }[]) {
  const mask = new Array<boolean>(text.length).fill(false);
  for (const range of ranges)
    for (let i = range.start; i < range.end; i++) mask[i] = true;
  return mask;
}

// A large run for a one-off audit: DELIDEV_MARKDOWN_MODEL_SEEDS=300000.
const MODEL_SEEDS = Math.max(
  6000,
  Number(process.env.DELIDEV_MARKDOWN_MODEL_SEEDS) || 0,
);

describe("differential test against the reference model", () => {
  it("agrees on hand-picked precedence interactions", () => {
    for (const text of [
      "![see [docs](https://d.test)][logo]\n\n[logo]: i.png",
      "[open ![logo](logo.png)](https://docs.test)",
      "`x\\` `https://a.test`",
      "```\n[a]: /u\n```\n\n[b https://a.test x][a]",
      '[a]: <x> "[b](https://b.test)"',
      '[a](https://a.test "' + "t ".repeat(600) + '")',
      '\t[docs]: https://example.test "unterminated',
      ' \t[docs]: https://example.test "unterminated\nhttps://b.test',
      "[label](https://a.test\\ more) https://b.test",
      "[label](https://a.test\\\tmore) https://b.test",
      "[label](https://a.test\\\nmore) https://b.test",
      "[label](https://a.test\\) https://b.test",
      "![label](i.png\\ more) https://b.test",
    ]) {
      const model = referenceModel(text),
        scan = scanMarkdown(text);
      expect(scan.complete).toBe(true);
      expect(scan.links.map((l) => [l.start, l.end, l.url])).toEqual(
        model.links.map((l) => [l.start, l.end, l.url]),
      );
      expect(maskOf(text, scan.protectedRanges)).toEqual(model.mask);
    }
  });

  it("agrees with the production scanner on seeded random Markdown", () => {
    const failures: string[] = [];
    let withLinks = 0,
      withDefinitions = 0,
      withClaims = 0;
    const bits = (mask: boolean[]) =>
      mask.map((on) => (on ? "1" : "0")).join("");
    const rows = (links: ModelLink[]) =>
      JSON.stringify(
        links.map((l) => [
          l.start,
          l.end,
          l.labelStart,
          l.labelEnd,
          l.label,
          l.url,
        ]),
      );
    for (let seed = 1; seed <= MODEL_SEEDS; seed++) {
      const next = generator(seed);
      const count = 1 + Math.floor(next() * 16);
      let text = "";
      for (let i = 0; i < count; i++) text += pickToken(next);
      const model = referenceModel(text),
        scan = scanMarkdown(text);
      if (model.links.length) withLinks++;
      if (/^ {0,3}\[[^\]\n]+\]:/mu.test(text)) withDefinitions++;
      if (model.mask.some(Boolean)) withClaims++;
      const got = `${rows(scan.links)} ${bits(maskOf(text, scan.protectedRanges))} ${scan.complete}`;
      const want = `${rows(model.links)} ${bits(model.mask)} true`;
      if (got !== want) {
        failures.push(
          `${JSON.stringify(text)}\n  scan  ${got}\n  model ${want}`,
        );
        if (failures.length >= 5) break;
      }
    }
    expect(failures).toEqual([]);
    // The generator reaches the constructs it is meant to compare.
    expect(withLinks).toBeGreaterThan(150);
    expect(withDefinitions).toBeGreaterThan(300);
    expect(withClaims).toBeGreaterThan(3000);
  });

  it("keeps the structural invariants on random Markdown", () => {
    for (let seed = 10_001; seed <= 12_000; seed++) {
      const next = generator(seed);
      const count = 1 + Math.floor(next() * 24);
      let text = "";
      for (let i = 0; i < count; i++) text += pickToken(next);
      const scan = scanMarkdown(text);
      let previousEnd = 0;
      for (const link of scan.links) {
        // Links are ordered, disjoint, protected and never inside other claims.
        expect(link.start).toBeGreaterThanOrEqual(previousEnd);
        previousEnd = link.end;
        expect(link.labelOffsets).toHaveLength(link.label.length + 1);
        expect(
          scan.protectedRanges.some(
            (range) => range.start <= link.start && range.end >= link.end,
          ),
        ).toBe(true);
      }
      // Conversion is idempotent.
      const once = convert(text);
      expect(convert(once)).toBe(once);
    }
  });
});
