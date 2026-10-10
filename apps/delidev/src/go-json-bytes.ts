// SPDX-License-Identifier: Apache-2.0
/** Match encoding/json retention accounting after strict document validation. */
export function goJsonBytes(value: unknown): number {
  // Go escapes HTML-significant characters and JavaScript line separators,
  // even inside nested stringified tool inputs. Raw UTF-8 undercounts them.
  const json = JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, character =>
    `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`);
  return new TextEncoder().encode(json).byteLength;
}
