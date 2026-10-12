// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { revealSharedTab, useSharedTabReveal } from "./shared-tabs";

let resize: () => void;
const disconnect = vi.fn();
const observe = vi.fn();
beforeEach(() => {
  disconnect.mockClear();
  observe.mockClear();
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: () => void) {
        resize = callback;
      }
      observe = observe;
      disconnect = disconnect;
    },
  );
});
afterEach(() => vi.unstubAllGlobals());

function View({
  selected = "first",
  labels = ["First", "Second"],
}: {
  selected?: string;
  labels?: string[];
}) {
  const root = useRef<HTMLDivElement>(null);
  useSharedTabReveal(root, selected);
  return (
    <div data-testid="parent">
      <div className="shared-tab-strip" ref={root} data-testid="strip">
        {labels.map((label, index) => (
          <div
            key={index}
            className={`shared-tab-item shared-tab-item--closable${selected === (index === 0 ? "first" : "second") ? " is-selected" : ""}`}
          >
            <button
              className="shared-tab-control shared-tab-label"
              role="tab"
              aria-selected={selected === (index === 0 ? "first" : "second")}
            >
              {label}
            </button>
            <button
              className="shared-tab-control shared-tab-close"
              aria-label={`Close ${label}`}
            >
              ×
            </button>
          </div>
        ))}
      </div>
      <section data-testid="panel">Retained panel</section>
      <div className="shared-tab-strip">
        <button className="shared-tab-item shared-tab-control">
          Other strip
        </button>
      </div>
    </div>
  );
}
function dimensions(node: HTMLElement, values: Record<string, number>) {
  for (const [key, value] of Object.entries(values))
    Object.defineProperty(node, key, { configurable: true, value });
}
function geometry() {
  const strip = screen.getByTestId("strip");
  dimensions(strip, { clientWidth: 160 });
  const tabs = screen.getAllByRole("tab");
  dimensions(tabs[0].parentElement!, { offsetLeft: 0, offsetWidth: 120 });
  dimensions(tabs[1].parentElement!, { offsetLeft: 120, offsetWidth: 140 });
  return { strip, tabs };
}

it("writes only the owning strip horizontally and leaves visible items unchanged", () => {
  render(<View />);
  const { strip, tabs } = geometry();
  const parent = screen.getByTestId("parent"),
    panel = screen.getByTestId("panel");
  parent.scrollLeft = 17;
  parent.scrollTop = 19;
  panel.scrollLeft = 23;
  panel.scrollTop = 29;
  document.documentElement.scrollLeft = 31;
  document.documentElement.scrollTop = 37;
  const write = vi.fn();
  let position = 0;
  Object.defineProperty(strip, "scrollLeft", {
    configurable: true,
    get: () => position,
    set: (value) => {
      position = value;
      write(value);
    },
  });
  revealSharedTab(tabs[0]);
  expect(write).not.toHaveBeenCalled();
  revealSharedTab(tabs[1]);
  expect(write).toHaveBeenLastCalledWith(100);
  revealSharedTab(screen.getByRole("button", { name: "Close Second" }));
  expect(write).toHaveBeenCalledTimes(1);
  revealSharedTab(tabs[0]);
  expect(write).toHaveBeenLastCalledWith(0);
  expect([
    parent.scrollLeft,
    parent.scrollTop,
    panel.scrollLeft,
    panel.scrollTop,
    document.documentElement.scrollLeft,
    document.documentElement.scrollTop,
  ]).toEqual([17, 19, 23, 29, 31, 37]);
  expect(tabs[0].getAttribute("aria-selected")).toBe("true");
});

it("reveals focused inactive manual controls and sibling close targets without activation", () => {
  const view = render(<View />);
  const { strip, tabs } = geometry();
  const activate = vi.fn();
  strip.addEventListener("click", activate);
  act(() => tabs[1].focus({ preventScroll: true }));
  expect(strip.scrollLeft).toBe(100);
  dimensions(strip, { clientWidth: 100 });
  resize();
  expect(strip.scrollLeft).toBe(120);
  expect(document.activeElement).toBe(tabs[1]);
  expect(tabs[1].getAttribute("aria-selected")).toBe("false");
  dimensions(screen.getByRole("button", { name: "Close Second" }), {
    offsetLeft: 100,
    offsetWidth: 40,
  });
  act(() =>
    screen
      .getByRole("button", { name: "Close Second" })
      .focus({ preventScroll: true }),
  );
  strip.scrollLeft = 0;
  resize();
  expect(strip.scrollLeft).toBe(160);
  expect(activate).not.toHaveBeenCalled();
  const cleanupCalls = disconnect.mock.calls.length;
  view.unmount();
  expect(disconnect.mock.calls.length).toBeGreaterThan(cleanupCalls);
});

it("reveals selected automatic controls, observes changed labels and new inventory, and isolates focus in another strip", async () => {
  const view = render(<View />);
  const { strip, tabs } = geometry();
  view.rerender(<View selected="second" />);
  expect(strip.scrollLeft).toBe(100);
  act(() =>
    screen
      .getByRole("button", { name: "Other strip" })
      .focus({ preventScroll: true }),
  );
  dimensions(tabs[1].parentElement!, { offsetWidth: 220 });
  resize();
  expect(strip.scrollLeft).toBe(120);
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: "Other strip" }),
  );
  view.rerender(
    <View
      selected="second"
      labels={["First", "긴 이름 · changed locale", "Appended"]}
    />,
  );
  const appended = screen.getByRole("tab", { name: "Appended" }).parentElement!;
  await waitFor(() => expect(observe).toHaveBeenCalledWith(appended));
  expect(strip.scrollLeft).toBe(120);
  expect(screen.getByRole("tab", { name: "긴 이름 · changed locale" })).toBe(
    tabs[1],
  );
});

it("does not scroll hidden strips or detached controls", () => {
  render(<View />);
  const { strip, tabs } = geometry();
  dimensions(strip, { clientWidth: 0 });
  revealSharedTab(tabs[1]);
  expect(strip.scrollLeft).toBe(0);
  revealSharedTab(document.createElement("button"));
  revealSharedTab(null);
});
