// SPDX-License-Identifier: Apache-2.0
// Match Go's escaped JSON retention bounds without changing native wire content.
export function claudeRetainedJSONBytes(value: unknown): number {
  return new TextEncoder().encode(JSON.stringify(value).replace(/[<>&\u2028\u2029]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`)).length;
}
