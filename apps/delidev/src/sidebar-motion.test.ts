// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, test, vi } from "vitest";
import { SidebarPaneMotion, type SidebarPresentation } from "./sidebar-motion";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

function fixture() {
  vi.useFakeTimers();
  const app = document.createElement("div");
  const pane = document.createElement("dialog");
  const content = document.createElement("div");
  app.append(pane);
  pane.append(content);
  let rendered = 288;
  const frozen: string[] = [];
  vi.spyOn(app, "getBoundingClientRect").mockImplementation(() => {
    frozen.push(app.style.gridTemplateColumns);
    return {} as DOMRect;
  });
  vi.stubGlobal("getComputedStyle", () => ({
    gridTemplateColumns: `${53 + rendered}px 1000px`,
    transitionDuration: "0.18s",
  }));
  const values: SidebarPresentation[] = [];
  const motion = new SidebarPaneMotion(app, pane, content, (value) =>
    values.push(value),
  );
  const end = (propertyName = "grid-template-columns", target = app) => {
    const event = new Event("transitionend", { bubbles: true });
    Object.defineProperty(event, "propertyName", { value: propertyName });
    target.dispatchEvent(event);
  };
  return {
    app,
    pane,
    content,
    values,
    motion,
    frozen,
    end,
    render: (width: number) => {
      rendered = width;
    },
  };
}

test("initial restoration is immediate; closing hides only at the current endpoint", () => {
  const f = fixture();
  f.motion.present(false, 288, false);
  expect(f.values.at(-1)).toEqual({
    generation: 1,
    settled: true,
    collapsed: false,
    focusAllowed: true,
  });
  expect(vi.getTimerCount()).toBe(0);
  f.motion.present(true, 288, true);
  expect(f.pane.hidden).toBe(false);
  expect(f.pane.open).toBe(true);
  expect(f.app.style.gridTemplateColumns).toBe("53px minmax(0, 1fr)");
  expect(f.content.style.transform).toBe("translateX(-288px)");
  f.render(100);
  f.end();
  f.end("transform");
  f.end("grid-template-columns", f.content);
  expect(f.values.at(-1)?.settled).toBe(false);
  f.render(0);
  f.end();
  expect(f.pane.hidden).toBe(true);
  expect(f.pane.open).toBe(false);
  expect(f.values.at(-1)).toEqual({
    generation: 2,
    settled: true,
    collapsed: true,
    focusAllowed: true,
  });
  expect(vi.getTimerCount()).toBe(0);
});

test("reversal freezes rendered geometry and retires the old fallback and completion", async () => {
  const f = fixture();
  let complete!: () => void;
  const finished = new Promise<void>((resolve) => {
    complete = resolve;
  });
  Object.defineProperty(f.app, "getAnimations", {
    configurable: true,
    value: () => [{ transitionProperty: "grid-template-columns", finished }],
  });
  f.motion.present(true, 288, true);
  f.render(144);
  Object.defineProperty(f.app, "getAnimations", { value: () => [] });
  f.motion.present(false, 288, true);
  expect(f.frozen.at(-1)).toBe("197px minmax(0, 1fr)");
  expect(f.values.at(-1)).toEqual({
    generation: 2,
    settled: false,
    collapsed: false,
    focusAllowed: true,
  });
  complete();
  await Promise.resolve();
  expect(f.pane.hidden).toBe(false);
  expect(f.values.at(-1)?.settled).toBe(false);
  vi.advanceTimersByTime(239);
  expect(f.values.at(-1)?.settled).toBe(false);
  vi.advanceTimersByTime(1);
  expect(f.pane.hidden).toBe(false);
  expect(f.pane.open).toBe(true);
  expect(f.app.style.gridTemplateColumns).toBe("341px minmax(0, 1fr)");
  expect(f.values.at(-1)).toEqual({
    generation: 2,
    settled: true,
    collapsed: false,
    focusAllowed: true,
  });
});

test("reduced motion, breakpoint or navigation settlement retires pending work", () => {
  const f = fixture();
  f.motion.present(true, 288, true);
  f.motion.present(false, 256, false);
  expect(vi.getTimerCount()).toBe(0);
  expect(f.app.style.gridTemplateColumns).toBe("309px minmax(0, 1fr)");
  expect(f.content.style.transform).toBe("translateX(0px)");
  f.end();
  vi.advanceTimersByTime(240);
  expect(f.pane.hidden).toBe(false);
  expect(f.values.at(-1)).toEqual({
    generation: 2,
    settled: true,
    collapsed: false,
    focusAllowed: true,
  });
  f.motion.present(true, 256, false);
  expect(f.pane.hidden).toBe(true);
});

test("disposal settles the latest endpoint and removes timers and owned styles", () => {
  const f = fixture();
  f.motion.present(true, 288, true);
  const count = f.values.length;
  f.motion.dispose();
  f.end();
  vi.advanceTimersByTime(240);
  expect(f.pane.hidden).toBe(true);
  expect(f.pane.open).toBe(false);
  expect(f.app.style.gridTemplateColumns).toBe("");
  expect(f.content.style.transform).toBe("");
  expect(vi.getTimerCount()).toBe(0);
  expect(f.values).toHaveLength(count);
});

test("cancellation of the current CSS transition settles its endpoint", async () => {
  const f = fixture();
  let cancel!: (error: Error) => void;
  const finished = new Promise<void>((_, reject) => {
    cancel = reject;
  });
  Object.defineProperty(f.app, "getAnimations", {
    value: () => [{ transitionProperty: "grid-template-columns", finished }],
  });
  f.motion.present(true, 288, true);
  cancel(new Error("transition cancelled"));
  await Promise.resolve();
  expect(f.pane.hidden).toBe(true);
  expect(f.values.at(-1)).toEqual({
    generation: 2,
    settled: true,
    collapsed: true,
    focusAllowed: false,
  });
  expect(vi.getTimerCount()).toBe(0);
});
