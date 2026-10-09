// SPDX-License-Identifier: Apache-2.0
import type { CSSProperties } from "react";

/** Resolve the displayed percentage through live semantic theme tokens. */
export function quotaColor(percent: number): string {
  if (percent === 0) return "var(--danger-text)";
  if (percent === 50) return "var(--warning-text)";
  if (percent === 100) return "var(--success-text)";
  return percent < 50
    ? `color-mix(in srgb, var(--danger-text) ${100 - percent * 2}%, var(--warning-text) ${percent * 2}%)`
    : `color-mix(in srgb, var(--warning-text) ${200 - percent * 2}%, var(--success-text) ${percent * 2 - 100}%)`;
}

export function quotaColorStyle(percent: number): CSSProperties {
  return { "--quota-fill": quotaColor(percent) } as CSSProperties;
}
