// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import { revealSharedTab } from "./shared-tabs";

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
