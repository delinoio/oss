// SPDX-License-Identifier: Apache-2.0
import { invoke, isTauri } from "@tauri-apps/api/core";
import type { MessageKey } from "./localization";

/** Native failure codes that an external link action can surface. */
export enum ExternalLinkFailure {
  BrowserUnavailable = "browser-unavailable",
  BrowserLaunchFailed = "browser-launch-failed",
  StorageUnavailable = "storage-unavailable",
  InvalidInput = "invalid-input",
  Other = "other",
}

export const externalLinkFailureMessages: Record<
  ExternalLinkFailure,
  MessageKey
> = {
  [ExternalLinkFailure.BrowserUnavailable]: "external-link.notInstalled",
  [ExternalLinkFailure.BrowserLaunchFailed]: "external-link.launchFailed",
  [ExternalLinkFailure.StorageUnavailable]: "external-link.settingsUnreadable",
  [ExternalLinkFailure.InvalidInput]: "external-link.invalid",
  [ExternalLinkFailure.Other]: "external-link.failed",
};

export function externalLinkFailure(error: unknown): ExternalLinkFailure {
  const code = typeof error === "string" ? error : undefined;
  return (
    Object.values(ExternalLinkFailure).find((value) => value === code) ??
    ExternalLinkFailure.Other
  );
}

const selectedBrowserFailures = new Set([
  ExternalLinkFailure.BrowserUnavailable,
  ExternalLinkFailure.BrowserLaunchFailed,
  ExternalLinkFailure.StorageUnavailable,
]);

/**
 * Message for failures that point at the browser selection (a missing browser,
 * a failed launch or unreadable preferences). Other failures keep the caller's
 * own message. Wrapped errors from `NativeOpenError` are recognized too.
 */
export function selectedBrowserFailureMessage(
  error: unknown,
): MessageKey | undefined {
  const failure = externalLinkFailure(
    error instanceof NativeOpenError ? error.code : error,
  );
  return selectedBrowserFailures.has(failure)
    ? externalLinkFailureMessages[failure]
    : undefined;
}

/** Keeps a native failure code across a caller's wider try block. */
export class NativeOpenError extends Error {
  constructor(readonly code: unknown) {
    super("Native browser opening failed.");
  }
}

/** Web previews and mobile have no native opener, so links stay inert text. */
export const externalLinksAvailable = () => isTauri();

/**
 * Asks the native host to open one explicit URL. The host validates and
 * classifies it independently and resolves the saved browser; this function
 * never retries or falls back.
 */
export async function openExternalUrl(url: string): Promise<void> {
  await invoke("open_external_url", { url });
}

export interface UrlToken {
  start: number;
  end: number;
  url: string;
}

interface UrlMatch extends UrlToken {
  rawEnd: number;
}

const candidate = /https?:\/\/[^\s<>"'`\\^{|}]+/giu;
const closers: Record<string, string> = { ")": "(", "]": "[" };
const trailing = new Set([".", ",", ";", ":", "!", "?", "'", '"']);

function trimmedEnd(text: string): number {
  let end = text.length;
  for (;;) {
    const last = text[end - 1];
    if (last === undefined) return end;
    if (trailing.has(last)) {
      end -= 1;
      continue;
    }
    const opener = closers[last];
    if (opener !== undefined) {
      const body = text.slice(0, end);
      const open = body.split(opener).length - 1;
      const close = body.split(last).length - 1;
      if (close > open) {
        end -= 1;
        continue;
      }
    }
    return end;
  }
}

/**
 * Finds complete explicit HTTP(S) tokens. Trailing sentence punctuation and
 * unbalanced closing brackets stay outside the URL. The native host repeats
 * validation; this detection only decides which text is activatable.
 */
function findUrlMatches(text: string): UrlMatch[] {
  const tokens: UrlMatch[] = [];
  if (text.length > 1_000_000) return tokens;
  for (const match of text.matchAll(candidate)) {
    const begin = match.index!;
    const raw = match[0];
    const url = raw.slice(0, trimmedEnd(raw));
    if (url.length > 2048 || !validHttpUrl(url)) continue;
    tokens.push({
      start: begin,
      end: begin + url.length,
      rawEnd: begin + raw.length,
      url,
    });
  }
  return tokens;
}

export function findUrlTokens(text: string): UrlToken[] {
  return findUrlMatches(text).map(({ rawEnd: _rawEnd, ...token }) => token);
}

export function validHttpUrl(value: string): boolean {
  try {
    const parsed = new URL(value);
    return (
      (parsed.protocol === "http:" || parsed.protocol === "https:") &&
      parsed.hostname !== "" &&
      parsed.username === "" &&
      parsed.password === ""
    );
  } catch {
    return false;
  }
}

/**
 * Splits message text without changing it. A URL at the buffer boundary stays
 * plain text until the owning message or block has completed.
 */
export function splitLinkedText(
  text: string,
  complete: boolean,
): ({ text: string } | { text: string; url: string })[] {
  const parts: ({ text: string } | { text: string; url: string })[] = [];
  let offset = 0;
  for (const token of findUrlMatches(text)) {
    if (!complete && token.rawEnd === text.length) continue;
    if (token.start > offset)
      parts.push({ text: text.slice(offset, token.start) });
    parts.push({ text: text.slice(token.start, token.end), url: token.url });
    offset = token.end;
  }
  if (offset < text.length) parts.push({ text: text.slice(offset) });
  return parts;
}

/**
 * Resolves the URL under one character of a terminal's logical line. Rows that
 * fill the whole grid width and continue without whitespace are joined so a URL
 * wrapped across rows activates as one token.
 */
export function urlAtTerminalPosition(
  rows: readonly string[],
  row: number,
  index: number,
  columns: number,
): string | undefined {
  const text = (value: string) => value.replace(/ /g, " ").trimEnd();
  if (row < 0 || row >= rows.length) return;
  const filled = (value: string) =>
    columns > 0 && value.length >= columns && !/\s$/.test(value);
  let first = row;
  while (
    first > 0 &&
    filled(text(rows[first - 1]!)) &&
    /^\S/.test(text(rows[first]!))
  )
    first -= 1;
  let last = row;
  while (
    last + 1 < rows.length &&
    filled(text(rows[last]!)) &&
    /^\S/.test(text(rows[last + 1]!))
  )
    last += 1;
  let offset = 0;
  for (let current = first; current < row; current += 1)
    offset += text(rows[current]!).length;
  const line = rows
    .slice(first, last + 1)
    .map((value) => text(value))
    .join("");
  const position = offset + index;
  return findUrlTokens(line).find(
    (token) => position >= token.start && position < token.end,
  )?.url;
}
