// SPDX-License-Identifier: Apache-2.0
export function validateSessionName(title: string, fallback: string) {
  const name = title.trim() || fallback;
  return { name, valid: new TextEncoder().encode(name).length <= 256 };
}
