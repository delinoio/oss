// SPDX-License-Identifier: Apache-2.0
import { act } from "@testing-library/react";
import { vi } from "vitest";

/** Dispatches a synthetic CSS animation event on the animated surface. */
export function dispatchAnimation(
  node: Element,
  name = "entrance-y4",
  type = "animationend",
) {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    node.dispatchEvent(event);
  });
}

/** Stubs OS reduced motion and returns a setter that notifies listeners. */
export function reducedMotion(initial: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: initial,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  // Only the reduced-motion query follows the stub; layout queries stay false.
  const other = {
    matches: false,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
  };
  vi.stubGlobal("matchMedia", (query: string) =>
    query.includes("prefers-reduced-motion") ? media : other,
  );
  return (next: boolean) =>
    act(() => {
      media.matches = next;
      listeners.forEach((changed) => changed());
    });
}

/** Sets or clears the Animation preference attribute and flushes observers. */
export async function setAnimation(value: string | undefined) {
  await act(async () => {
    if (value === undefined) delete document.documentElement.dataset.animation;
    else document.documentElement.dataset.animation = value;
    await Promise.resolve();
  });
}
