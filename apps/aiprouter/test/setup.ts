// SPDX-License-Identifier: Apache-2.0
import { afterEach, rs } from "@rstest/core";
import { cleanup } from "@testing-library/react";
afterEach(() => {
  cleanup();
  window.history.replaceState(null, "", "/");
});
// jsdom has no top-layer rendering. Browser acceptance must verify native focus trapping.
if (!HTMLDialogElement.prototype.showModal)
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute("open", "");
  };
if (!HTMLDialogElement.prototype.close)
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute("open");
  };
// matchMedia evaluates min-width/max-width queries against window.innerWidth
// (jsdom default 1024) and notifies listeners on "resize" events.
function matchesWidth(query: string) {
  const width = window.innerWidth;
  const min = /\(min-width:\s*(\d+)px\)/.exec(query);
  const max = /\(max-width:\s*(\d+)px\)/.exec(query);
  return (!min || width >= Number(min[1])) && (!max || width <= Number(max[1]));
}
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: rs.fn((query: string) => {
    const listeners = new Set<() => void>();
    return {
      get matches() {
        return matchesWidth(query);
      },
      media: query,
      onchange: null,
      addListener: rs.fn(),
      removeListener: rs.fn(),
      addEventListener: (_type: string, listener: () => void) => {
        listeners.add(listener);
        window.addEventListener("resize", listener);
      },
      removeEventListener: (_type: string, listener: () => void) => {
        listeners.delete(listener);
        window.removeEventListener("resize", listener);
      },
      dispatchEvent: rs.fn(),
    };
  }),
});
