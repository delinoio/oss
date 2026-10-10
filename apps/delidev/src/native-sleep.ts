// SPDX-License-Identifier: Apache-2.0
/** Read an exact duration from the original passive tool snapshots. */
export function nativeSleepDuration(started: Record<string, unknown>, completed: Record<string, unknown>): string {
  if (started.kind !== "sleep" || started.status !== "running" || !started.sleep || typeof started.sleep !== "object") return "";
  const value = (started.sleep as Record<string, unknown>).duration_ms;
  if (typeof value !== "string" || !/^(0|[1-9][0-9]{0,19})$/.test(value) || BigInt(value) > 18446744073709551615n) return "";
  if (Object.keys(completed).length) {
    if (completed.kind !== "sleep" || completed.status !== "completed" || !completed.sleep || typeof completed.sleep !== "object" || (completed.sleep as Record<string, unknown>).duration_ms !== value) return "";
  }
  return value;
}
