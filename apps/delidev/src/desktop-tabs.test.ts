// SPDX-License-Identifier: Apache-2.0
import { afterEach, describe, expect, it, vi } from "vitest";
import { revealDesktopTab } from "./desktop-tabs";

function fixture(left: number, right: number) {
  const strip = document.createElement("div"), item = document.createElement("div"), close = document.createElement("button");
  strip.className = "desktop-tab-strip";
  item.className = "desktop-tab-item";
  item.append(close); strip.append(item); document.body.append(strip);
  vi.spyOn(strip, "getBoundingClientRect").mockReturnValue({ left: 10, right: 110, width: 100 } as DOMRect);
  const bounds = vi.spyOn(item, "getBoundingClientRect").mockReturnValue({ left, right } as DOMRect);
  strip.scrollLeft = 100; strip.scrollTop = 20;
  return { strip, close, bounds };
}
afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); });
describe("tab strip visibility", () => {
  it("reveals a close target without moving focus or panel scroll", () => {
    const { strip, close } = fixture(90, 160);
    close.focus(); revealDesktopTab(close);
    expect(strip.scrollLeft).toBe(150);
    expect(strip.scrollTop).toBe(20);
    expect(document.activeElement).toBe(close);
  });
  it("reveals the left edge and leaves visible items in place", () => {
    const { strip, close, bounds } = fixture(-20, 70);
    revealDesktopTab(close); expect(strip.scrollLeft).toBe(70);
    bounds.mockReturnValue({ left: 20, right: 100 } as DOMRect);
    revealDesktopTab(close); expect(strip.scrollLeft).toBe(70);
  });
  it("leaves retained hidden or inert panes untouched", () => {
    const { strip, close } = fixture(90, 160);
    strip.hidden = true; revealDesktopTab(close); expect(strip.scrollLeft).toBe(100);
    strip.hidden = false; strip.setAttribute("inert", ""); revealDesktopTab(close);
    expect(strip.scrollLeft).toBe(100);
    revealDesktopTab(document.body); expect(strip.scrollLeft).toBe(100);
  });
});
