// SPDX-License-Identifier: Apache-2.0

/**
 * Builds the server's source-scoped model key for tests: `source:` followed by
 * the unpadded base64url JSON of the exact source and native model identity,
 * matching `ModelIdentity.Key` in `cmds/delidev-cli/internal/domain`.
 */
export function sourceModelKey(identity: {
  provider_id?: string;
  subscription_service?: string;
  native_id: string;
}): string {
  const bytes = new TextEncoder().encode(JSON.stringify(identity));
  const binary = Array.from(bytes, (byte) => String.fromCharCode(byte)).join(
    "",
  );
  return `source:${btoa(binary)
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "")}`;
}
