// SPDX-License-Identifier: Apache-2.0
export interface PickerBounds { left: number; top: number; right: number; bottom: number }
export interface PickerAnchor extends PickerBounds { width: number }

/** Geometry only. Selection, queries and dialog ownership remain with the picker. */
export function pickerOverlayGeometry(anchor: PickerAnchor, bounds: PickerBounds, contentHeight: number) {
  const inset = 8, gap = 4;
  const leftBound = bounds.left + inset, rightBound = Math.max(leftBound, bounds.right - inset);
  const topBound = bounds.top + inset, bottomBound = Math.max(topBound, bounds.bottom - inset);
  const below = Math.max(0, bottomBound - Math.max(topBound, anchor.bottom + gap));
  const above = Math.max(0, Math.min(bottomBound, anchor.top - gap) - topBound);
  const desired = Math.min(280, Math.max(0, contentHeight));
  const useBelow = desired <= below || below >= above;
  const maxHeight = Math.min(280, useBelow ? below : above);
  const height = Math.min(desired, maxHeight);
  const width = Math.min(Math.max(0, anchor.width), rightBound - leftBound);
  const left = Math.max(leftBound, Math.min(anchor.left, rightBound - width));
  const top = Math.max(topBound, Math.min(useBelow ? anchor.bottom + gap : anchor.top - gap - height, bottomBound - height));
  return { left, top, width, maxHeight };
}

export function pickerSurfaceOwner(trigger: HTMLElement): HTMLElement | undefined {
 for (let node: HTMLElement | null = trigger.parentElement; node; node = node.parentElement) {
  if (!node.matches(".settings-task-body, .sidebar-pane, .sidebar-pane-dialog, .sidebar, [data-picker-boundary]")) continue;
  const rect = node.getBoundingClientRect();
  // Compact sidebar wrappers use display:contents; their pane owns the box.
  if (rect.width > 0 && rect.height > 0) return node;
 }
 return undefined;
}

export function pickerSurfaceBounds(trigger: HTMLElement): PickerBounds {
  const viewport = window.visualViewport;
  const left = viewport?.offsetLeft ?? 0, top = viewport?.offsetTop ?? 0;
  const bounds = { left, top, right: left + (viewport?.width ?? window.innerWidth), bottom: top + (viewport?.height ?? window.innerHeight) };
  const owner = pickerSurfaceOwner(trigger);
  if (!owner) return bounds;
  const rect = owner.getBoundingClientRect();
  return { left: Math.max(bounds.left, rect.left), top: Math.max(bounds.top, rect.top), right: Math.min(bounds.right, rect.right), bottom: Math.min(bounds.bottom, rect.bottom) };
}
