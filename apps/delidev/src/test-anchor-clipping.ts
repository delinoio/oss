// SPDX-License-Identifier: Apache-2.0
import { vi } from "vitest";

const rect = (left: number, top: number, width: number, height: number) =>
  ({
    left,
    top,
    width,
    height,
    right: left + width,
    bottom: top + height,
    x: left,
    y: top,
    toJSON: () => ({}),
  }) as DOMRect;

/**
 * jsdom has no layout. This places one anchor inside a 100px high clipping
 * scroll owner (the element carrying `data-test-clip-owner` and
 * `overflow: auto`) and lets a test move the anchor with `state.anchorTop`:
 * 120 is inside the owner, 240 is completely below it. Every other element
 * keeps an empty box. The caller restores mocks.
 */
export function mockAnchorClipping(
  isAnchor: (element: HTMLElement) => boolean,
) {
  const state = { anchorTop: 120 };
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      if (this.hasAttribute("data-test-clip-owner"))
        return rect(0, 100, 600, 100);
      if (isAnchor(this)) return rect(500, state.anchorTop, 32, 32);
      return rect(0, 0, 0, 0);
    },
  );
  return state;
}
