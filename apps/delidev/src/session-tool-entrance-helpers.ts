// SPDX-License-Identifier: Apache-2.0
import { act } from "@testing-library/react";
import { vi } from "vitest";

/** Dispatches a CSS animation event with the given animation name on `node`. */
export function dispatchAnimation(
  node: Element,
  name = "entrance-y2",
  type = "animationend",
) {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    node.dispatchEvent(event);
  });
}

/** Stubs `matchMedia` and returns a setter that notifies reduced-motion listeners. */
export function stubReducedMotion(initial: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: initial,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  return (next: boolean) =>
    act(() => {
      media.matches = next;
      listeners.forEach((changed) => changed());
    });
}

/** Switches the global Animation preference and notifies mutation observers. */
export async function setAnimation(value: "false" | undefined) {
  await act(async () => {
    if (value === undefined) delete document.documentElement.dataset.animation;
    else document.documentElement.dataset.animation = value;
    await Promise.resolve();
  });
}
