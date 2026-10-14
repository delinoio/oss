// SPDX-License-Identifier: Apache-2.0

function base64Url(text: string): string {
  const bytes = new TextEncoder().encode(text);
  const binary = Array.from(bytes, (byte) => String.fromCharCode(byte)).join(
    "",
  );
  return btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");
}

/**
 * Builds a source model key from exact JSON text without canonicalizing it, so
 * tests can express non-canonical payloads (field order, whitespace, duplicate
 * fields).
 */
export function rawSourceModelKey(json: string): string {
  return `source:${base64Url(json)}`;
}

/**
 * Builds the server's source-scoped model key for tests: `source:` followed by
 * the unpadded base64url JSON of the exact source and native model identity,
 * matching `ModelIdentity.Key` in `cmds/delidev-cli/internal/domain`. The
 * identity fields must be passed in the server's field order (`provider_id` or
 * `subscription_service`, then `native_id`); the JSON uses Go's HTML-safe
 * escaping of `<`, `>`, `&`, U+2028 and U+2029.
 */
export function sourceModelKey(identity: {
  provider_id?: string;
  subscription_service?: string;
  native_id: string;
}): string {
  return rawSourceModelKey(
    JSON.stringify(identity).replace(
      /[<>&\u2028\u2029]/g,
      (char) => `\\u${char.charCodeAt(0).toString(16).padStart(4, "0")}`,
    ),
  );
}
