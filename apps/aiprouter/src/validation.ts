// SPDX-License-Identifier: Apache-2.0

// Shared Unicode-aware validation helpers for form drafts. Server limits are
// measured in UTF-8 bytes, so HTML maxlength and String.length are not valid
// substitutes.
const encoder = new TextEncoder();

/** Returns the number of bytes the value occupies when serialized as UTF-8. */
export function utf8ByteLength(value: string): number {
  return encoder.encode(value).length;
}

/** Returns true when the value is longer than limit UTF-8 bytes. */
export function exceedsUtf8Bytes(value: string, limit: number): boolean {
  return utf8ByteLength(value) > limit;
}
