// SPDX-License-Identifier: Apache-2.0

const encoder = new TextEncoder();

// Retained native metadata uses Go encoding/json's default HTML escaping.
// JSON.stringify alone omits these escapes and undercounts the shared bound.
export function claudeRetainedJSONBytes(value: unknown): number {
  const encoded = JSON.stringify(value).replace(
    /[<>&\u2028\u2029]/g,
    (character) =>
      `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`,
  );
  return encoder.encode(encoded).length;
}
