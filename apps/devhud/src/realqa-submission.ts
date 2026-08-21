import { assertUuidV7 } from "@delinoio/devhud-api-client";
import { sanitizeChromeContext, type SanitizedBrowserContext } from "./browser-context.ts";

export const PublicImageWarning = Object.freeze({
  en: "Anyone who knows the image URL can view it. The image remains public until you delete it, an administrator removes it, or your account deletion completes.",
  ko: "이미지 URL을 아는 사람은 누구나 이미지를 볼 수 있습니다. 이미지는 사용자가 삭제하거나, 관리자가 제거하거나, 계정 삭제가 완료될 때까지 공개 상태로 유지됩니다.",
});

const markerPattern = /<!--\s*devhud-submission:[^>]*-->/giu;
const secretPatterns = [
  /\b(?:ghp|github_pat)_[A-Za-z0-9_]+\b/gu,
  /\bAKIA[0-9A-Z]{16}\b/gu,
  /\beyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/gu,
  /-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----/gu,
  /\b(?:authorization|cookie|password|passwd|secret|token|access[_ -]?key(?:[_ -]?id)?)\s*[:=]\s*[^\s,;]+/giu,
] as const;

export interface IssueBodyInput {
  readonly userBody: string;
  readonly diagnostics: SanitizedBrowserContext | null;
  readonly imageUrls: readonly string[];
  readonly submissionId: string;
  readonly diagnosticsSummary: string;
}

export function editableBrowserDiagnostics(context: SanitizedBrowserContext): string {
  return JSON.stringify(context, null, 2);
}

export function parseEditableBrowserDiagnostics(value: string): SanitizedBrowserContext {
  let decoded: unknown;
  try { decoded = JSON.parse(value); } catch { throw new TypeError("Browser diagnostics must be valid JSON"); }
  const redacted = redactUnknown(decoded, new WeakSet<object>());
  const sanitized = sanitizeChromeContext(redacted);
  if (sanitized.kind !== "sanitized") throw new TypeError("Browser diagnostics do not match the sanitized context contract");
  return sanitized.context;
}

export function composeIssueBody(input: IssueBodyInput): string {
  assertUuidV7(input.submissionId);
  const sections: string[] = [];
  const userBody = redactText(input.userBody).replace(markerPattern, "[redacted submission marker]").trim();
  if (userBody) sections.push(userBody);
  if (input.diagnostics !== null) {
    const diagnosticJson = JSON.stringify(input.diagnostics, null, 2);
    sections.push(`<details>\n<summary>${escapeHtml(redactText(input.diagnosticsSummary))}</summary>\n\n<pre><code>${escapeHtml(redactText(diagnosticJson))}</code></pre>\n</details>`);
  }
  for (const [index, imageUrl] of input.imageUrls.entries()) {
    sections.push(`![RealQA image ${index + 1}](${canonicalPublicImageUrl(imageUrl)})`);
  }
  sections.push(`<!-- devhud-submission:${input.submissionId} -->`);
  return sections.join("\n\n");
}

/** The GitHub provider owns the final marker write and reconciliation contract. */
export function stripFinalSubmissionMarker(value: string, submissionId: string): string {
  assertUuidV7(submissionId);
  const marker = `<!-- devhud-submission:${submissionId} -->`;
  if (value === marker) return "";
  const suffix = `\n\n${marker}`;
  if (!value.endsWith(suffix)) throw new TypeError("Issue body does not end with its submission marker");
  return value.slice(0, -suffix.length);
}

export function canonicalPublicImageUrl(value: string): string {
  try {
    const parsed = new URL(value);
    if (parsed.protocol !== "https:" || parsed.username || parsed.password || parsed.search || parsed.hash) throw new Error();
    return parsed.toString();
  } catch { throw new TypeError("Public image URL must be HTTPS without credentials, query, or fragment"); }
}

export function decodeSha256Hex(value: string): Uint8Array {
  if (!/^[0-9a-f]{64}$/u.test(value)) throw new TypeError("SHA-256 must be lowercase hexadecimal");
  return Uint8Array.from({ length: 32 }, (_, index) => Number.parseInt(value.slice(index * 2, index * 2 + 2), 16));
}

function redactUnknown(value: unknown, seen: WeakSet<object>): unknown {
  if (typeof value === "string") return redactText(value);
  if (value === null || typeof value !== "object") return value;
  if (seen.has(value)) throw new TypeError("Browser diagnostics must not contain cycles");
  seen.add(value);
  const output = Array.isArray(value)
    ? value.map((entry) => redactUnknown(entry, seen))
    : Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, redactUnknown(entry, seen)]));
  seen.delete(value);
  return output;
}

function redactText(value: string): string {
  let redacted = value.replaceAll("\0", "");
  for (const pattern of secretPatterns) redacted = redacted.replace(pattern, "[redacted]");
  return redacted;
}

function escapeHtml(value: string): string {
  return value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;").replaceAll("'", "&#39;");
}
