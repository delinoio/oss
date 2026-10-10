// SPDX-License-Identifier: Apache-2.0
export function configuredEffort(value: unknown, nativeDefault: string, unavailable: string): string {
  if (value === undefined || value === "") return nativeDefault;
  return typeof value === "string" ? value : unavailable;
}
