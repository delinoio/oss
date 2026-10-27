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
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: rs.fn((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: rs.fn(),
    removeListener: rs.fn(),
    addEventListener: rs.fn(),
    removeEventListener: rs.fn(),
    dispatchEvent: rs.fn(),
  })),
});
