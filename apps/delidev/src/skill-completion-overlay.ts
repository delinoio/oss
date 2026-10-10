// SPDX-License-Identifier: Apache-2.0
import type { PickerAnchor, PickerBounds } from "./picker-overlay";

/** Presentation geometry only; skill acceptance remains with its original scope. */
export function skillCompletionGeometry(anchor: PickerAnchor, bounds: PickerBounds, contentHeight: number, scale = 1) {
  const margin = 8, gap = 8;
  const leftBound = bounds.left + margin, rightBound = Math.max(leftBound, bounds.right - margin);
  const topBound = bounds.top + margin, bottomBound = Math.max(topBound, bounds.bottom - margin);
  const above = Math.max(0, Math.min(bottomBound, anchor.top - gap) - topBound);
  const below = Math.max(0, bottomBound - Math.max(topBound, anchor.bottom + gap));
  const desired = Math.min(220 * scale, Math.max(0, contentHeight));
  const useAbove = desired <= above || desired > below && above >= below;
  const maxHeight = Math.min(220 * scale, useAbove ? above : below);
  const height = Math.min(desired, maxHeight);
  const width = Math.min(Math.max(0, anchor.width), rightBound - leftBound);
  const left = Math.max(leftBound, Math.min(anchor.left, rightBound - width));
  const top = Math.max(topBound, Math.min(useAbove ? anchor.top - gap - height : anchor.bottom + gap, bottomBound - height));
  return { left, top, width, maxHeight };
}
