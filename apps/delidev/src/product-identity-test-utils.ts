// SPDX-License-Identifier: Apache-2.0
/** Match a safe presentation number without treating it as resource authority. */
export function numberedAction(label: string): RegExp {
  const escaped = label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`^${escaped} · Item \\d+$`);
}
