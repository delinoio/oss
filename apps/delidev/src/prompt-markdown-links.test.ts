// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { findUrlTokens } from "./external-links";
import {
  LineEndings,
  MAX_COLLAPSED_LINKS,
  canonicalEditFromDisplay,
  convertBareUrls,
  convertTerminatedUrl,
  joinKeepingLineEndings,
  markdownLinkFor,
  normalizeLineEndings,
  projectMarkdown,
  scanMarkdown,
  selectionRevealsLink,
} from "./prompt-markdown-links";

function convert(text: string) {
  return convertBareUrls(text, 0, text.length).text;
}

describe("bare URL conversion", () => {
  it("wraps eligible HTTP(S) URLs and keeps surrounding text", () => {
    expect(convert("Review https://example.com/docs with $review")).toBe(
      "Review [https://example.com/docs](https://example.com/docs) with $review",
    );
    expect(convert("see http://a.test/x?y=1#z 확인")).toBe(
      "see [http://a.test/x?y=1#z](http://a.test/x?y=1#z) 확인",
    );
  });
  it("reuses punctuation and bracket trimming", () => {
    expect(convert("go https://a.test/x.")).toBe(
      "go [https://a.test/x](https://a.test/x).",
    );
    expect(convert("(https://a.test/x)")).toBe(
      "([https://a.test/x](https://a.test/x))",
    );
    expect(convert("https://en.wikipedia.org/wiki/Foo_(bar)")).toBe(
      "[https://en.wikipedia.org/wiki/Foo_(bar)](https://en.wikipedia.org/wiki/Foo_\\(bar\\))",
    );
  });
  it("leaves ineligible URLs untouched", () => {
    for (const text of [
      "www.example.com",
      "mailto:a@b.test",
      "javascript:alert(1)",
      "ftp://a.test/x",
      "https://",
      "https://user:pass@a.test/x",
      `https://a.test/${"x".repeat(2100)}`,
      "abchttps://a.test/x",
      "[https://a.test/x",
      "[Docs](https://a.test/x ok)",
      "!https://a.test/x",
    ])
      expect(convert(text)).toBe(text);
  });
  it("treats a URL glued to a letter, digit or mark of any script as not standalone", () => {
    for (const glue of [
      "한", // Hangul syllable
      "あ", // Hiragana
      "漢", // CJK ideograph
      "é", // precomposed Latin
      "ب", // Arabic
      "Ж", // Cyrillic
      "३", // Devanagari digit
      "１", // fullwidth digit
      "7",
      "\u0301", // combining acute accent
      "\u{20bb7}", // astral CJK ideograph (surrogate pair)
      "\u{1d7d8}", // astral mathematical digit
    ]) {
      const text = `${glue}https://example.test/x `;
      expect(convert(text)).toBe(text);
      expect(convert(`a ${glue}https://example.test/x b`)).toBe(
        `a ${glue}https://example.test/x b`,
      );
    }
  });
  it("still converts after whitespace, punctuation, symbols and emoji of any script", () => {
    for (const lead of [
      " ",
      "\n",
      "。",
      "、",
      "「",
      "(",
      ":",
      "😀",
      "\u00a0",
      "→",
    ]) {
      const text = `한${lead}https://example.test/x`;
      expect(convert(text)).toContain("[https://example.test/x](");
    }
  });
  it("never converts inside links, images, code spans or fences", () => {
    for (const text of [
      "[Docs](https://example.com/docs)",
      "![alt](https://example.com/a.png)",
      "[see https://example.com](https://other.test)",
      "use `https://example.com` here",
      "``a `https://example.com` b``",
      "```\nhttps://example.com\n```",
      "~~~js\nhttps://example.com\n",
      "<https://example.com>",
      "[ref]: https://example.com",
      '[x](mailto:a@b.test "https://example.com")',
    ])
      expect(convert(text)).toBe(text);
  });
  it("protects an image whose description holds links or URLs", () => {
    for (const text of [
      "![see https://plain.test [x](https://x.test)](image.png)",
      "![a [b](https://b.test) c https://c.test](i.png)",
      "![[x](https://x.test)](https://img.test/a.png)",
      '![a [b](https://b.test) [c](https://c.test)](i.png "t")',
      "![outer ![inner [l](https://l.test)](in.png) https://o.test](out.png)",
    ]) {
      expect(convert(text)).toBe(text);
      expect(scanMarkdown(text).links).toHaveLength(0);
      expect(convert(`pre https://p.test ${text}`)).toBe(
        `pre [https://p.test](https://p.test) ${text}`,
      );
    }
    // A following ordinary link is still found after the image.
    const links = scanMarkdown(
      "![a [b](https://b.test)](i.png) [c](https://c.test)",
    ).links;
    expect(links.map((link) => link.url)).toEqual(["https://c.test"]);
  });
  it("is idempotent and never nests", () => {
    const once = convert("a https://example.com/x b https://example.org/y");
    expect(convert(once)).toBe(once);
    expect(scanMarkdown(once).links).toHaveLength(2);
  });
  it("escapes delimiters without changing the resolved URL", () => {
    for (const url of [
      "https://a.test/x[1]",
      "https://a.test/x](y",
      "https://a.test/(a)(b",
      "https://a.test/x)y",
      "https://a.test/a?b=[c]&d=(e)",
    ]) {
      const link = markdownLinkFor(url);
      const scan = scanMarkdown(link);
      expect(scan.links).toHaveLength(1);
      expect(scan.links[0]!.url).toBe(url);
      expect(scan.links[0]!.label).toBe(url);
    }
  });
  it("skips URLs that continue outside the converted range", () => {
    expect(convertBareUrls("https://a.test/xyz", 0, 13).text).toBe(
      "https://a.test/xyz",
    );
    expect(
      convertBareUrls("https://a.test/?u=https://b.test", 20, 32).text,
    ).toBe("https://a.test/?u=https://b.test");
  });
  it("converts only the URL a delimiter terminated", () => {
    const text = "see https://a.test/x then https://b.test/y ";
    const end = text.length - 1;
    expect(convertTerminatedUrl(text, end).text).toBe(
      "see https://a.test/x then [https://b.test/y](https://b.test/y) ",
    );
  });
});

describe("link scanning", () => {
  it("accepts valid inline HTTP(S) links and rejects the rest", () => {
    expect(scanMarkdown("[Docs](https://example.com/docs)").links).toHaveLength(
      1,
    );
    for (const text of [
      "[Docs](ftp://example.com)",
      "[Docs](javascript:alert(1))",
      "[](https://example.com)",
      "[ ](https://example.com)",
      "[Docs](https://example.com",
      "[Docs] (https://example.com)",
      '[Docs](https://example.com "title")',
      "[Docs](https://user:pw@example.com)",
      "![Docs](https://example.com)",
      "[Docs](<https://example.com>)",
      "[Dou\nble](https://example.com)",
      "[[a](https://x.test)](https://y.test)x",
    ]) {
      const links = scanMarkdown(text).links;
      expect(links.map((link) => link.url)).not.toContain("https://y.test");
      if (!text.startsWith("[[")) expect(links).toHaveLength(0);
    }
  });
  it("treats an escaped image marker as a literal bang before an ordinary link", () => {
    // Only an odd run of backslashes escapes the bang.
    for (const text of [
      "\\![Docs](https://docs.test)",
      "\\\\\\![Docs](https://docs.test)",
      "x \\![Docs](https://docs.test) y",
    ]) {
      const links = scanMarkdown(text).links;
      expect(links).toHaveLength(1);
      expect(links[0]!.start).toBe(text.indexOf("[Docs"));
      expect(text[links[0]!.start - 1]).toBe("!");
    }
    for (const text of [
      "\\\\![Docs](https://docs.test)",
      "\\\\\\\\![Docs](https://docs.test)",
      "![Docs](https://docs.test)",
      "a\\\\![Docs](https://docs.test)",
    ]) {
      expect(scanMarkdown(text).links).toHaveLength(0);
      expect(convert(text)).toBe(text);
    }
    expect(convert("\\![see https://a.test](https://docs.test)")).toBe(
      "\\![see https://a.test](https://docs.test)",
    );
  });
  it("protects an autolink after an escaped backslash but not after an escape", () => {
    expect(convert("\\\\<https://x.test>")).toBe("\\\\<https://x.test>");
    expect(convert("\\<https://x.test>")).toBe(
      "\\<[https://x.test](https://x.test)>",
    );
  });
  it("keeps the inner link of a nested label as the only link", () => {
    const links = scanMarkdown("[[a](https://x.test)](https://y.test)").links;
    expect(links.map((link) => link.url)).toEqual(["https://x.test"]);
  });
  it("unescapes labels and tracks per-unit source offsets", () => {
    const text = "[a\\]b](https://example.com)";
    const [link] = scanMarkdown(text).links;
    expect(link!.label).toBe("a]b");
    expect(link!.labelOffsets).toEqual([1, 2, 4, 5]);
  });
  it("protects code, fences and autolinks", () => {
    const text = "`a [x](https://e.com) b`\n```\n[y](https://f.com)\n```";
    expect(scanMarkdown(text).links).toHaveLength(0);
  });
  it("reports an incomplete scan instead of hanging on adversarial input", () => {
    const text = "[](".repeat(60_000);
    const started = Date.now();
    const scan = scanMarkdown(text);
    expect(Date.now() - started).toBeLessThan(5000);
    expect(scan.links).toHaveLength(0);
    expect(convertBareUrls(text, 0, text.length, scan).changed).toBe(false);
  });
});

describe("bounded nested-label scanning", () => {
  const LIMIT = 256 * 1024;
  const shapes: [string, (count: number) => string][] = [
    [
      "opens first, then one destination per level",
      (count) => `${"[".repeat(count)}x${"](https://a.test)".repeat(count)}`,
    ],
    [
      "many open brackets, then a long gap before the first marker",
      (count) =>
        `${"[".repeat(count)}${"a".repeat(count * 24)}${"](https://a.test)".repeat(count)}`,
    ],
    [
      "recursively nested labels with text between levels",
      (count) =>
        `${"[a ".repeat(count)}x${"](https://a.test) b".repeat(count)}`,
    ],
    [
      "nested labels inside images",
      (count) =>
        `${"![a ".repeat(count)}x${"](https://a.test) b".repeat(count)}`,
    ],
  ];
  for (const [name, build] of shapes) {
    it(`stays linear near the prompt limit: ${name}`, () => {
      let count = 1;
      while (build(count + 1).length <= LIMIT) count = Math.ceil(count * 1.25);
      while (build(count).length > LIMIT) count--;
      const text = build(count);
      expect(text.length).toBeLessThanOrEqual(LIMIT);
      expect(text.length).toBeGreaterThan(LIMIT * 0.9);
      const started = performance.now();
      const scan = scanMarkdown(text);
      const elapsed = performance.now() - started;
      // A quadratic rescan of every candidate outer label takes many seconds.
      expect(elapsed).toBeLessThan(750);
      expect(scan.complete).toBe(true);
      // Only the innermost label may become a link; every outer label holds it.
      expect(scan.links.length).toBeLessThanOrEqual(1);
      const converted = convertBareUrls(text, 0, text.length, scan);
      expect(converted.changed).toBe(false);
    });
  }
  it("counts a link marker inside code as ordinary label text", () => {
    const text = "[a `](x)` b](https://y.test)";
    expect(scanMarkdown(text).links.map((link) => link.url)).toEqual([
      "https://y.test",
    ]);
  });
  it("still rejects a label that holds a real nested link", () => {
    expect(
      scanMarkdown("[a [b](https://b.test) c](https://c.test)").links.map(
        (link) => link.url,
      ),
    ).toEqual(["https://b.test"]);
    // An escaped bracket never forms a nested-link marker.
    expect(
      scanMarkdown("[a \\](x) b](https://y.test)").links.map(
        (link) => link.label,
      ),
    ).toEqual(["a ](x) b"]);
  });
});

describe("projection", () => {
  const source = "Hi [Docs](https://e.com/d) and [a\\]b](https://e.com/e)!";
  const scan = scanMarkdown(source);
  const projection = projectMarkdown(source, scan.links, () => true);
  it("shows labels only", () => {
    expect(projection.text).toBe("Hi Docs and a]b!");
  });
  it("maps link edges to the source edges and interiors to label units", () => {
    expect(projection.toSource(3)).toBe(3);
    expect(projection.toSource(5)).toBe(6);
    expect(projection.toSource(7)).toBe(26);
    expect(projection.toSource(0)).toBe(0);
    expect(projection.toDisplay(3)).toBe(3);
    expect(projection.toDisplay(26)).toBe(7);
    expect(projection.toDisplay(source.length)).toBe(projection.text.length);
  });
  it("keeps only the chosen links collapsed", () => {
    const partial = projectMarkdown(
      source,
      scan.links,
      (link) => link.label === "Docs",
    );
    expect(partial.text).toBe("Hi Docs and [a\\]b](https://e.com/e)!");
  });
  it("bounds the number of collapsed links", () => {
    const many = "[a](https://e.com) ".repeat(MAX_COLLAPSED_LINKS + 5);
    const links = scanMarkdown(many).links;
    expect(links).toHaveLength(MAX_COLLAPSED_LINKS + 5);
    expect(projectMarkdown(many, links, () => true).regions).toHaveLength(
      MAX_COLLAPSED_LINKS,
    );
  });
  it("reveals on strict interior carets and overlapping selections only", () => {
    const link = { start: 3, end: 26 };
    expect(selectionRevealsLink(link, { start: 3, end: 3 })).toBe(false);
    expect(selectionRevealsLink(link, { start: 26, end: 26 })).toBe(false);
    expect(selectionRevealsLink(link, { start: 4, end: 4 })).toBe(true);
    expect(selectionRevealsLink(link, { start: 0, end: 3 })).toBe(false);
    expect(selectionRevealsLink(link, { start: 26, end: 30 })).toBe(false);
    expect(selectionRevealsLink(link, { start: 0, end: 4 })).toBe(true);
    expect(selectionRevealsLink(link, { start: 10, end: 12 })).toBe(true);
  });
});

describe("display edits", () => {
  const source = "Hi [Docs](https://e.com/d) there";
  const projection = projectMarkdown(
    source,
    scanMarkdown(source).links,
    () => true,
  );
  it("maps typing after a collapsed link to the source link end", () => {
    // Display "Hi Docs there" -> type "x" right after the link label.
    const edit = canonicalEditFromDisplay(projection, "Hi Docsx there", 8);
    expect(edit.start).toBe(26);
    expect(edit.end).toBe(26);
    expect(edit.inserted).toBe("x");
    expect(edit.caret(8)).toBe(27);
  });
  it("maps typing before a collapsed link to the source link start", () => {
    const edit = canonicalEditFromDisplay(projection, "Hi xDocs there", 4);
    expect(edit.start).toBe(3);
    expect(edit.caret(4)).toBe(4);
  });
  it("maps deleting text behind the link through the hidden syntax", () => {
    const edit = canonicalEditFromDisplay(projection, "Hi Docs here", 8);
    expect([edit.start, edit.end, edit.inserted]).toEqual([27, 28, ""]);
    expect(edit.caret(8)).toBe(27);
  });
  it("keeps a label edit inside the label and removes a whole-label deletion as the link", () => {
    const one = projectMarkdown(
      "[ab](https://e.com/d)",
      scanMarkdown("[ab](https://e.com/d)").links,
      () => true,
    );
    const tail = canonicalEditFromDisplay(one, "a", 1);
    expect([tail.start, tail.end, tail.inserted]).toEqual([2, 3, ""]);
    const whole = canonicalEditFromDisplay(one, "", 0);
    expect([whole.start, whole.end]).toEqual([0, 21]);
  });
  it("picks the edit nearest the caret when characters repeat", () => {
    const flat = projectMarkdown("aa", [], () => true);
    expect(canonicalEditFromDisplay(flat, "aaa", 1).start).toBe(0);
    expect(canonicalEditFromDisplay(flat, "aaa", 3).start).toBe(2);
  });
  it("shifts collapsed regions through an edit outside them", () => {
    const shifted = projection.shifted(0, 0, "x", 1);
    expect(shifted?.regions[0]?.srcStart).toBe(4);
    expect(shifted?.toSource(5)).toBe(6);
    expect(projection.shifted(4, 5, "", -1)).toBeUndefined();
  });
});

// Deterministic generator so failures reproduce from the printed seed.
function random(seed: number) {
  let state = seed >>> 0;
  return () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 0x100000000;
  };
}
const FUZZ_PIECES = [
  "a",
  "한",
  "😀",
  " ",
  "\n",
  "\r\n",
  "\r",
  "\\",
  "[",
  "]",
  "(",
  ")",
  "`",
  "!",
  "<",
  ">",
  "[Docs](https://e.com/x)",
  "[a\\]b](https://e.com/y(z))",
  "https://a.test/p?q=[1]&r=(2)",
  "https://a.test/x.",
  "```\n",
  "[ref]:\n  ",
  "[ref]: /u\n  'T https://t.test'\n",
  "![alt https://r.test][ref]",
  "[https://r.test][]",
  "[ref]",
  "[ref]: i.png\n",
  '<a href="https://h.test">',
  "![i](https://e.com/i.png)",
  "$skill",
  "한https://a.test/g ",
  "7https://a.test/d ",
  "\\![i](https://e.com/e)",
  "\\\\![i](https://e.com/e)",
  "![a [b](https://b.test) https://c.test](i.png)",
  "<https://a.test/auto>",
  "\u0301",
  "\u{20bb7}",
];

describe("round-trip invariants", () => {
  for (let seed = 1; seed <= 150; seed++) {
    it(`holds for generated text (seed ${seed})`, () => {
      const next = random(seed);
      let text = "";
      const count = 1 + Math.floor(next() * 24);
      for (let index = 0; index < count; index++)
        text += FUZZ_PIECES[Math.floor(next() * FUZZ_PIECES.length)];
      const scan = scanMarkdown(text);
      // Links are ordered, disjoint, valid and their label offsets increase.
      let previousEnd = 0;
      for (const link of scan.links) {
        expect(link.start).toBeGreaterThanOrEqual(previousEnd);
        previousEnd = link.end;
        expect(link.labelOffsets).toHaveLength(link.label.length + 1);
        for (let index = 1; index < link.labelOffsets.length; index++)
          expect(link.labelOffsets[index]!).toBeGreaterThan(
            link.labelOffsets[index - 1]!,
          );
        expect(link.labelOffsets.at(-1)).toBe(link.labelEnd);
      }
      const reveal = new Set<number>();
      scan.links.forEach((link, index) => {
        if (next() < 0.4) reveal.add(index);
      });
      const projection = projectMarkdown(
        text,
        scan.links,
        (link) => !reveal.has(scan.links.indexOf(link)),
      );
      let lastSource = -1,
        lastDisplay = -1;
      for (let source = 0; source <= text.length; source++) {
        const display = projection.toDisplay(source);
        expect(display).toBeGreaterThanOrEqual(lastDisplay);
        lastDisplay = display;
      }
      // display -> canonical -> display is stable and toSource is monotonic.
      for (let display = 0; display <= projection.text.length; display++) {
        const source = projection.toSource(display);
        expect(source).toBeGreaterThanOrEqual(lastSource);
        lastSource = source;
        expect(projection.toDisplay(source)).toBe(display);
      }
      expect(projection.toSource(projection.text.length)).toBe(text.length);
      expect(projection.toSource(0)).toBe(0);
      // Conversion is idempotent, never shrinks text and keeps links valid.
      const converted = convert(text);
      expect(convert(converted)).toBe(converted);
      expect(converted.length).toBeGreaterThanOrEqual(text.length);
      for (const link of scanMarkdown(converted).links)
        expect(link.url.startsWith("http")).toBe(true);
      // Every link that conversion created follows a standalone boundary: not a
      // letter, digit or mark of any script, backslash, bang or open bracket.
      const original = new Map<string, number>();
      for (const link of scan.links) {
        const source = text.slice(link.start, link.end);
        original.set(source, (original.get(source) ?? 0) + 1);
      }
      for (const link of scanMarkdown(converted).links) {
        const source = converted.slice(link.start, link.end),
          known = original.get(source) ?? 0;
        if (known > 0) {
          original.set(source, known - 1);
          continue;
        }
        const before = [...converted.slice(0, link.start)].at(-1);
        if (before !== undefined)
          expect(before).not.toMatch(/[\p{L}\p{N}\p{M}\\![]/u);
      }
    });
  }
  it("round-trips converted URLs of generated shapes to the same URL", () => {
    const next = random(99);
    const alphabet = "abc123-._~:/?#[]@!$&'()*+,;=%";
    for (let index = 0; index < 300; index++) {
      let tail = "";
      const length = 1 + Math.floor(next() * 30);
      for (let step = 0; step < length; step++)
        tail += alphabet[Math.floor(next() * alphabet.length)];
      const url = `https://host.test/${tail}`;
      const converted = convert(`see ${url} end`);
      const links = scanMarkdown(converted).links;
      if (converted === `see ${url} end`) continue;
      // Trailing punctuation stays outside the link by the shared rules.
      const expected = findUrlTokens(url)[0]!.url;
      expect(links).toHaveLength(1);
      expect(links[0]!.url).toBe(expected);
      expect(links[0]!.label).toBe(expected);
    }
  });
});

// --- Line endings -----------------------------------------------------------

/** Writes every LF of `text` with the given ending form. */
function withEndings(text: string, ending: "\n" | "\r\n" | "\r"): string {
  return text.replace(/\n/gu, ending);
}

describe("line endings", () => {
  it("maps exact and normalized offsets around CRLF, lone CR and LF", () => {
    const exact = "a\r\nb\rc\nd\r\n",
      endings = new LineEndings(exact);
    expect(endings.text).toBe("a\nb\nc\nd\n");
    expect(endings.uniform).toBe(false);
    // Offsets: a=0 CR=1 LF=2 b=3 CR=4 c=5 LF=6 d=7 CR=8 LF=9 end=10.
    const normalized = [0, 1, 1, 2, 3, 4, 5, 6, 7, 7, 8];
    for (let offset = 0; offset <= exact.length; offset++)
      expect(endings.toNormalized(offset)).toBe(normalized[offset]);
    // A normalized line ending maps to the start of the exact ending.
    expect(endings.toExact(1)).toBe(1);
    expect(endings.toExact(2)).toBe(3);
    expect(endings.toExact(3)).toBe(4);
    expect(endings.toExact(4)).toBe(5);
    expect(endings.toExact(7)).toBe(8);
    expect(endings.toExact(8)).toBe(10);
    for (let offset = 0; offset <= endings.text.length; offset++)
      expect(endings.toNormalized(endings.toExact(offset))).toBe(offset);
    expect(new LineEndings("plain\nlines").uniform).toBe(true);
    expect(normalizeLineEndings("a\r\nb\rc\n")).toBe("a\nb\nc\n");
  });
  it("fuses no CR with a following LF when an edit makes them adjacent", () => {
    expect(joinKeepingLineEndings("a\r", "\nb")).toBe("a\n\nb");
    expect(joinKeepingLineEndings("a\r", "xb")).toBe("a\rxb");
    expect(joinKeepingLineEndings("a\n", "\nb")).toBe("a\n\nb");
  });
  const TEXTS = [
    "see [Docs](https://e.com/d)\nhttps://a.test/x end\n[ref]:\n  https://r.test\n",
    "```\nhttps://c.test\n```\nhttps://a.test/y\n",
    "![i https://i.test][logo]\n\n[logo]: i.png\n\nhttps://z.test\n",
    "line one\n\n[a]: /u\n  'T https://t.test'\nhttps://b.test\n",
  ];
  it("scans every ending form like LF with offsets in the exact text", () => {
    for (const text of TEXTS)
      for (const ending of ["\r\n", "\r"] as const) {
        const exact = withEndings(text, ending),
          lf = scanMarkdown(text),
          scan = scanMarkdown(exact),
          endings = new LineEndings(exact);
        expect(scan.complete).toBe(true);
        expect(scan.links.map((link) => link.url)).toEqual(
          lf.links.map((link) => link.url),
        );
        for (const link of scan.links) {
          expect(exact.slice(link.start, link.end)).toBe(
            withEndings(
              text.slice(
                lf.links[scan.links.indexOf(link)]!.start,
                lf.links[scan.links.indexOf(link)]!.end,
              ),
              ending,
            ),
          );
          expect(link.labelOffsets.at(-1)).toBe(link.labelEnd);
        }
        expect(
          scan.protectedRanges.map((range) => [
            endings.toNormalized(range.start),
            endings.toNormalized(range.end),
          ]),
        ).toEqual(lf.protectedRanges.map((range) => [range.start, range.end]));
        // Conversion agrees too: the same URLs convert, endings stay exact.
        expect(normalizeLineEndings(convert(exact))).toBe(convert(text));
        expect(convert(exact).split(ending).length).toBe(
          convert(text).split("\n").length,
        );
      }
  });
  it("shows the textarea text and maps selections inside CRLF text exactly", () => {
    const source = "a\r\n[Docs](https://e.com/d)\r\nb\rc";
    const projection = projectMarkdown(
      source,
      scanMarkdown(source).links,
      () => true,
    );
    expect(projection.text).toBe("a\nDocs\nb\nc");
    // Source offsets: a=0 CR=1 LF=2 link=3..26 CR=26 LF=27 b=28 CR=29 c=30.
    expect(projection.toDisplay(2)).toBe(1);
    expect(projection.toDisplay(3)).toBe(2);
    expect(projection.toDisplay(26)).toBe(6);
    expect(projection.toDisplay(28)).toBe(7);
    expect(projection.toDisplay(source.length)).toBe(projection.text.length);
    expect(projection.toSource(1)).toBe(1);
    expect(projection.toSource(2)).toBe(3);
    expect(projection.toSource(6)).toBe(26);
    expect(projection.toSource(7)).toBe(28);
    for (let display = 0; display <= projection.text.length; display++)
      expect(projection.toDisplay(projection.toSource(display))).toBe(display);
    // A caret inside the CRLF pair clamps to the start of the line ending.
    expect(projection.toDisplay(27)).toBe(projection.toDisplay(26));
  });
  it("turns a native edit across a line ending into the exact source range", () => {
    const source = "ab\r\ncd\ref";
    const projection = projectMarkdown(source, [], () => true);
    // Deleting the LF shown between "ab" and "cd" removes the whole CRLF.
    const joined = canonicalEditFromDisplay(projection, "abcd\nef", 2);
    expect([joined.start, joined.end, joined.inserted]).toEqual([2, 4, ""]);
    // Deleting a lone CR's shown LF removes only that unit.
    const lone = canonicalEditFromDisplay(projection, "ab\ncdef", 5);
    expect([lone.start, lone.end, lone.inserted]).toEqual([6, 7, ""]);
    const inserted = canonicalEditFromDisplay(projection, "ab\nx\ncd\nef", 4);
    // The textual diff may place the new line before or after the old one;
    // both leave the CRLF pair intact.
    expect([inserted.start, inserted.end, inserted.inserted]).toEqual([
      2,
      2,
      "\nx",
    ]);
  });
  it("shifts a frozen projection through an edit next to line endings", () => {
    for (const source of [
      "a\r\n[Docs](https://e.com/d) b",
      "a\r[Docs](https://e.com/d)\r\nb\r",
      "\r\n\r\n[Docs](https://e.com/d)",
    ]) {
      const projection = projectMarkdown(
        source,
        scanMarkdown(source).links,
        () => true,
      );
      for (const [from, to, inserted] of [
        [0, 0, "x"],
        [0, 0, "\n"],
        [1, 1, "x"],
        [1, 2, ""],
        [2, 3, "yz"],
        [projection.text.length, projection.text.length, "!\n"],
        [projection.text.length - 1, projection.text.length, ""],
      ] as const) {
        if (to > projection.text.length) continue;
        const start = projection.toSource(from),
          end = projection.toSource(to);
        if (projection.regionAt(from) || projection.regionAt(to)) continue;
        const next = joinKeepingLineEndings(
          source.slice(0, start),
          inserted + source.slice(end),
        );
        const shifted = projection.shifted(
          from,
          to,
          inserted,
          next.length - source.length,
        );
        const fresh = projectMarkdown(
          next,
          scanMarkdown(next).links,
          () => true,
        );
        // A region the edit touches forces a rebuild; every other edit must
        // equal the projection of the edited source exactly.
        if (!shifted) continue;
        expect(shifted.text).toBe(fresh.text);
        for (let display = 0; display <= fresh.text.length; display++)
          expect(shifted.toSource(display)).toBe(fresh.toSource(display));
        for (let offset = 0; offset <= next.length; offset++)
          expect(shifted.toDisplay(offset)).toBe(fresh.toDisplay(offset));
      }
    }
  });
  it("converts a URL pasted next to each ending form", () => {
    for (const ending of ["\n", "\r\n", "\r"]) {
      const text = `first${ending}https://a.test/x${ending}last`,
        from = 5 + ending.length,
        to = from + 16;
      const result = convertBareUrls(text, from, to);
      expect(result.text).toBe(
        `first${ending}[https://a.test/x](https://a.test/x)${ending}last`,
      );
    }
  });
});

describe("line-ending equivalence", () => {
  for (let seed = 1; seed <= 120; seed++) {
    it(`scans and projects every ending form alike (seed ${seed})`, () => {
      const next = random(seed * 7919);
      let text = "";
      const count = 1 + Math.floor(next() * 24);
      for (let index = 0; index < count; index++) {
        const piece = FUZZ_PIECES[Math.floor(next() * FUZZ_PIECES.length)]!;
        text += piece.replace(/\r\n?/gu, "\n");
      }
      const lf = scanMarkdown(text);
      for (const exact of [
        withEndings(text, "\r\n"),
        withEndings(text, "\r"),
        // Mixed: every other line break is CRLF, the rest a lone CR.
        text.replace(
          /\n/gu,
          (() => {
            let toggle = false;
            return () => ((toggle = !toggle) ? "\r\n" : "\r");
          })(),
        ),
      ]) {
        const scan = scanMarkdown(exact),
          endings = new LineEndings(exact);
        expect(scan.complete).toBe(lf.complete);
        expect(scan.links.map((link) => [link.label, link.url])).toEqual(
          lf.links.map((link) => [link.label, link.url]),
        );
        expect(
          scan.protectedRanges.map((range) => [
            endings.toNormalized(range.start),
            endings.toNormalized(range.end),
          ]),
        ).toEqual(lf.protectedRanges.map((range) => [range.start, range.end]));
        expect(normalizeLineEndings(convert(exact))).toBe(convert(text));
        const projection = projectMarkdown(exact, scan.links, () => true),
          reference = projectMarkdown(text, lf.links, () => true);
        expect(projection.text).toBe(reference.text);
        // The shown text is exactly what a textarea reports for it.
        expect(projection.text).toBe(normalizeLineEndings(projection.text));
        let last = -1;
        for (let display = 0; display <= projection.text.length; display++) {
          const source = projection.toSource(display);
          expect(source).toBeGreaterThanOrEqual(last);
          last = source;
          expect(projection.toDisplay(source)).toBe(display);
          // Same position as the LF text, through the line-ending map.
          expect(endings.toNormalized(source)).toBe(
            reference.toSource(display),
          );
        }
        let lastDisplay = -1;
        for (let source = 0; source <= exact.length; source++) {
          const display = projection.toDisplay(source);
          expect(display).toBeGreaterThanOrEqual(lastDisplay);
          lastDisplay = display;
        }
        expect(projection.toSource(projection.text.length)).toBe(exact.length);
      }
    });
  }
});

describe("reference definitions", () => {
  const URL = "https://example.test";
  it("protects a destination on the line after the label", () => {
    for (const text of [
      `[docs]:\n  ${URL}`,
      `[docs]:\n${URL}`,
      `[docs]:\t\n\t${URL}\n`,
      `   [docs]:\n   ${URL}`,
      `[docs]:\n  <${URL}/a b>`,
      `[docs]:\n  ${URL}/a(b)c`,
      `[docs]:   \n  ${URL} \n`,
    ])
      expect(convert(text)).toBe(text);
    // The first line alone is the definition when nothing follows it.
    expect(convert(`[docs]: ${URL}`)).toBe(`[docs]: ${URL}`);
  });
  it("protects a label that spans lines", () => {
    for (const text of [
      `[my\ndocs]:\n  ${URL}`,
      `[my\n docs]: ${URL}`,
      `[a\n\\]b]:\n  ${URL}`,
    ])
      expect(convert(text)).toBe(text);
  });
  it("protects titles on the destination line and on following lines", () => {
    for (const text of [
      `[docs]: /u "see ${URL}"`,
      `[docs]: /u 'see ${URL}'`,
      `[docs]: /u (see ${URL})`,
      `[docs]: /u\n  "see ${URL}"`,
      `[docs]:\n  /u\n  'see ${URL}'`,
      `[docs]:\n  /u\n  (see ${URL})`,
      `[docs]: /u "line one\nsee ${URL} two"`,
      `[docs]:\n  /u\n  "a\n  b ${URL}\n  c"\n`,
      `[docs]: <u v> "see ${URL}"`,
      `[docs]: /u "esc \\" ${URL}"`,
    ])
      expect(convert(text)).toBe(text);
  });
  it("ends the definition at a blank line, a stray token or a bad title", () => {
    const converted = (url: string) => `[${url}](${url})`;
    // No destination before a blank line: the URL after it is ordinary text.
    expect(convert(`[docs]:\n\n${URL}`)).toBe(`[docs]:\n\n${converted(URL)}`);
    // A title never spans a blank line.
    expect(convert(`[docs]: /u\n  "a\n\n  ${URL}"`)).toBe(
      `[docs]: /u\n  "a\n\n  ${converted(URL)}"`,
    );
    // A next-line title with trailing junk is a paragraph of its own.
    expect(convert(`[docs]: /u\n"t" junk ${URL}`)).toBe(
      `[docs]: /u\n"t" junk ${converted(URL)}`,
    );
    // The line after a complete definition is ordinary text.
    expect(convert(`[docs]: /u\n${URL}`)).toBe(`[docs]: /u\n${converted(URL)}`);
    expect(convert(`[docs]:\n  /u "t"\n${URL}`)).toBe(
      `[docs]:\n  /u "t"\n${converted(URL)}`,
    );
  });
  it("converts a URL pasted on the line after a bare label", () => {
    const before = "[docs]:\n  ",
      text = `${before}${URL}`;
    expect(convertBareUrls(text, before.length, text.length).changed).toBe(
      false,
    );
    const typed = convertTerminatedUrl(`${text}\n`, text.length);
    expect(typed.changed).toBe(false);
    // The destination line may be indented freely; the label line may not
    // start with four spaces.
    expect(convertBareUrls(`[docs]:\n      ${URL}`, 0, 100).changed).toBe(
      false,
    );
    expect(convert(`    [docs]:\n  ${URL}`)).toBe(
      `    [docs]:\n  [${URL}](${URL})`,
    );
  });
  it("keeps scanning links after a multiline definition", () => {
    const text = `[a]:\n  /u\n  "t"\n\nsee [b](https://b.test) ${URL}`;
    const scan = scanMarkdown(text);
    expect(scan.links.map((link) => link.url)).toEqual(["https://b.test"]);
    expect(convert(text)).toBe(
      `[a]:\n  /u\n  "t"\n\nsee [b](https://b.test) [${URL}](${URL})`,
    );
  });
  it("scans adversarial definition candidates within the budget", () => {
    for (const text of [
      "[a]:\n".repeat(50_000),
      "[\n".repeat(50_000),
      `[a]: /u "${"x\n".repeat(40_000)}`,
      `[${"a ".repeat(40_000)}`,
      "[a]: <".repeat(40_000),
    ]) {
      const started = Date.now();
      const scan = scanMarkdown(text);
      expect(Date.now() - started).toBeLessThan(5000);
      if (scan.complete) continue;
      expect(convertBareUrls(text, 0, text.length, scan).changed).toBe(false);
    }
  });
});

describe("reference links and images", () => {
  const URL = "https://example.test";
  const converted = `[${URL}](${URL})`;
  it("protects full, collapsed and shortcut images", () => {
    for (const text of [
      `![see ${URL} x][logo]\n\n[logo]: image.png`,
      `[logo]: image.png\n\n![see ${URL} x][logo]`,
      `![see ${URL} x][]\n\n[see ${URL} x]: image.png`,
      `![${URL} x]\n\n[${URL} x]: image.png`,
      `![${URL} x][]\n\n[${URL} x]: image.png`,
      `![alt ${URL} x][Logo  Name]\n\n[logo name]: image.png`,
      `![a [b](https://b.test) ${URL} x][logo]\n\n[logo]: image.png`,
      `![multi\nline ${URL} x][logo]\n\n[logo]: image.png`,
      `![see ${URL} x][logo]\n\n[logo]:\n  image.png\n  "t"`,
    ])
      expect(convert(text)).toBe(text);
  });
  it("protects full, collapsed and shortcut links", () => {
    for (const text of [
      `[see ${URL} x][docs]\n\n[docs]: /u`,
      `[see ${URL} x][]\n\n[see ${URL} x]: /u`,
      `[${URL} x]\n\n[${URL} x]: /u`,
      `[${URL} x][]\n\n[${URL} x]: /u`,
      `[docs]: /u\n\ntext [x ${URL} y][DOCS]`,
      `[see ${URL} x](bad url\n\n[see ${URL} x]: /u`,
    ])
      expect(convert(text)).toBe(text);
  });
  it("covers the whole image, bang included, and nothing more", () => {
    const text = `a ![x][logo] b\n\n[logo]: i.png`;
    const ranges = scanMarkdown(text).protectedRanges;
    expect(ranges[0]).toEqual({ start: 2, end: 12 });
    // An escaped bang is a literal character before an ordinary reference.
    const escaped = scanMarkdown(`\\![x][logo]\n\n[logo]: i.png`);
    expect(escaped.protectedRanges[0]).toEqual({ start: 2, end: 11 });
  });
  it("converts text whose label has no definition", () => {
    for (const text of [
      `![see ${URL} x][nope]\n\n[logo]: image.png`,
      `[see ${URL} x][nope]`,
      `[see ${URL} x]\n\n[docs]: /u`,
      `[see ${URL} x][]`,
      `![see ${URL} x]`,
    ]) {
      const result = convert(text);
      expect(result).not.toBe(text);
      expect(result).toContain(` ${converted} x]`);
    }
    // A label that holds a bracket or is overlong is never a reference.
    const nested = `[x ${URL} y][a[b]c]\n\n[a[b]c]: /u`;
    expect(convert(nested)).toContain(converted);
    const long = "l".repeat(1000);
    expect(convert(`[x ${URL} y][${long}]\n\n[${long}]: /u`)).toContain(
      converted,
    );
  });
  it("keeps an inner inline link inside a reference label found", () => {
    const text = `[a [b](https://b.test) ${URL}][docs]\n\n[docs]: /u`;
    const scan = scanMarkdown(text);
    expect(scan.links.map((link) => link.url)).toEqual(["https://b.test"]);
    expect(convert(text)).toBe(text);
  });
  it("scans reference-heavy and bracket-heavy text within the budget", () => {
    for (const text of [
      `${"[a][a]".repeat(40_000)}\n[a]: /u`,
      `${"[".repeat(30_000)}${"]".repeat(30_000)}\n[a]: /u`,
      `[a]: /u\n${"![a][a][a][] ".repeat(20_000)}`,
      `[a]: /u\n${"[x".repeat(40_000)}`,
    ]) {
      const started = Date.now();
      const scan = scanMarkdown(text);
      expect(Date.now() - started).toBeLessThan(5000);
      if (!scan.complete)
        expect(convertBareUrls(text, 0, text.length, scan).changed).toBe(false);
    }
  });
  it("protects raw HTML tags and their attribute values", () => {
    for (const text of [
      `<a href="${URL}">`,
      `<a href='${URL}' title="x">`,
      `<img src=${URL}/i.png alt="a">`,
      `<a\n  href="${URL}"\n>`,
      `<a href="${URL}"/>`,
      `</a>`,
    ])
      expect(convert(text)).toBe(text);
    // Text that only resembles a tag converts normally.
    expect(convert(`a < b ${URL} > c`)).toBe(`a < b ${converted} > c`);
    expect(convert(`<b ${URL}>`)).toBe(`<b ${converted}>`);
  });
  it("scans every line-ending form alike", () => {
    const text = `![see ${URL} x][logo]\n\n[logo]:\n  image.png\n\n${URL}\n`;
    for (const ending of ["\r\n", "\r"] as const) {
      const exact = withEndings(text, ending);
      expect(normalizeLineEndings(convert(exact))).toBe(convert(text));
      expect(convert(exact)).toContain(`${ending}${converted}${ending}`);
    }
  });
});
