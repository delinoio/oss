// SPDX-License-Identifier: Apache-2.0
import { describe, expect, it } from "vitest";
import { findUrlTokens } from "./external-links";
import {
  MAX_COLLAPSED_LINKS,
  canonicalEditFromDisplay,
  convertBareUrls,
  convertTerminatedUrl,
  markdownLinkFor,
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
    const shifted = projection.shifted(0, 0, 1, 1, "xHi Docs there");
    expect(shifted?.regions[0]?.srcStart).toBe(4);
    expect(shifted?.toSource(5)).toBe(6);
    expect(projection.shifted(4, 5, 0, -1, "Hi Dcs there")).toBeUndefined();
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
  "![i](https://e.com/i.png)",
  "$skill",
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
