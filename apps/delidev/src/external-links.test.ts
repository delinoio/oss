// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  ExternalLinkFailure,
  externalLinkFailure,
  findUrlTokens,
  splitLinkedText,
  urlAtTerminalPosition,
  validHttpUrl,
} from "./external-links";

it("finds only complete explicit http and https tokens and keeps surrounding punctuation outside", () => {
  const text =
    'See https://example.com/path?q=1#part. Also (http://localhost:3000/a_(b)), "https://[::1]:8080/x"; ftp://x.test javascript:alert(1) example.com bare';
  expect(findUrlTokens(text).map((token) => token.url)).toEqual([
    "https://example.com/path?q=1#part",
    "http://localhost:3000/a_(b)",
    "https://[::1]:8080/x",
  ]);
  for (const token of findUrlTokens(text))
    expect(text.slice(token.start, token.end)).toBe(token.url);
});

it("rejects credentials, malformed hosts and oversized tokens", () => {
  expect(findUrlTokens("https://user:secret@example.com/x")).toEqual([]);
  expect(findUrlTokens("https:// https://")).toEqual([]);
  expect(findUrlTokens(`https://example.com/${"a".repeat(2100)}`)).toEqual([]);
  expect(validHttpUrl("file:///etc/passwd")).toBe(false);
  expect(validHttpUrl("https://example.com")).toBe(true);
});

it("splits without changing any character, whitespace or line break", () => {
  const text =
    "line one\n\n  https://example.com/a  \n\tsecond https://x.test/b\r\nend";
  const parts = splitLinkedText(text);
  expect(parts.map((part) => part.text).join("")).toBe(text);
  expect(parts.filter((part) => "url" in part)).toHaveLength(2);
  expect(splitLinkedText("")).toEqual([]);
  expect(splitLinkedText("no links here")).toEqual([{ text: "no links here" }]);
});

it("activates only the token under the pointer and joins full-width wrapped rows", () => {
  const rows = [
    "$ open https://example.com/a",
    "https://example.com/very/long/",
    "path?x=1 done",
  ];
  expect(urlAtTerminalPosition(rows, 0, 12, 80)).toBe("https://example.com/a");
  expect(urlAtTerminalPosition(rows, 0, 2, 80)).toBeUndefined();
  // Rows exactly as wide as the grid continue into the next row.
  const wrapped = ["go https://example.com/ab", "cdefgh tail"];
  expect(urlAtTerminalPosition(wrapped, 0, 10, 25)).toBe(
    "https://example.com/abcdefgh",
  );
  expect(urlAtTerminalPosition(wrapped, 1, 2, 25)).toBe(
    "https://example.com/abcdefgh",
  );
  expect(urlAtTerminalPosition(wrapped, 1, 8, 25)).toBeUndefined();
  // A shorter first row is a real line break.
  expect(urlAtTerminalPosition(wrapped, 1, 2, 80)).toBeUndefined();
  expect(urlAtTerminalPosition(wrapped, 5, 0, 25)).toBeUndefined();
});

it("maps native failure codes to stable categories", () => {
  expect(externalLinkFailure("browser-unavailable")).toBe(
    ExternalLinkFailure.BrowserUnavailable,
  );
  expect(externalLinkFailure("browser-launch-failed")).toBe(
    ExternalLinkFailure.BrowserLaunchFailed,
  );
  expect(externalLinkFailure("storage-unavailable")).toBe(
    ExternalLinkFailure.StorageUnavailable,
  );
  expect(externalLinkFailure("invalid-input")).toBe(
    ExternalLinkFailure.InvalidInput,
  );
  expect(externalLinkFailure(new Error("secret"))).toBe(
    ExternalLinkFailure.Other,
  );
});
