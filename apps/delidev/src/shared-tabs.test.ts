// SPDX-License-Identifier: Apache-2.0
import { expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { revealSharedTab, useSharedTabReveal } from "./shared-tabs";

it("reveals only the enclosing horizontal strip while retaining panel/document scroll", () => {
  const panel = document.createElement("div"),
    strip = document.createElement("div"),
    item = document.createElement("div"),
    control = document.createElement("button");
  strip.className = "shared-tab-strip";
  item.className = "shared-tab-item";
  panel.append(strip);
  strip.append(item);
  item.append(control);
  document.body.append(panel);
  panel.scrollTop = 37;
  document.documentElement.scrollTop = 53;
  Object.defineProperties(strip, { clientWidth: { value: 200 } });
  Object.defineProperties(item, {
    offsetLeft: { value: 240, configurable: true },
    offsetWidth: { value: 100 },
  });
  revealSharedTab(control);
  expect(strip.scrollLeft).toBe(140);
  Object.defineProperty(item, "offsetLeft", { value: 20 });
  revealSharedTab(control);
  expect(strip.scrollLeft).toBe(20);
  expect(panel.scrollTop).toBe(37);
  expect(document.documentElement.scrollTop).toBe(53);
  revealSharedTab(null);
  panel.remove();
  document.documentElement.scrollTop = 0;
});

it("reveals a manually focused inactive tab on resize without activating it", () => {
  const strip = document.createElement("div"),
    selected = document.createElement("button"),
    focused = document.createElement("button");
  strip.className = "shared-tab-strip";
  for (const button of [selected, focused]) {
    button.className = "shared-tab-item shared-tab-control";
    strip.append(button);
    Object.defineProperty(button, "offsetWidth", { value: 100 });
  }
  selected.setAttribute("aria-selected", "true");
  focused.setAttribute("aria-selected", "false");
  Object.defineProperty(strip, "clientWidth", { value: 200 });
  Object.defineProperty(selected, "offsetLeft", { value: 0 });
  Object.defineProperty(focused, "offsetLeft", { value: 240 });
  document.body.append(strip);
  let resize = () => {};
  const disconnect = vi.fn();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        resize = callback;
      }
      observe() {}
      disconnect = disconnect;
    },
  );
  const view = renderHook(() =>
    useSharedTabReveal({ current: strip }, "first"),
  );
  try {
    focused.focus();
    resize();
    expect(strip.scrollLeft).toBe(140);
    expect(focused.getAttribute("aria-selected")).toBe("false");
    focused.blur();
    resize();
    expect(strip.scrollLeft).toBe(0);
  } finally {
    view.unmount();
    expect(disconnect).toHaveBeenCalledTimes(1);
    vi.unstubAllGlobals();
    strip.remove();
  }
});
