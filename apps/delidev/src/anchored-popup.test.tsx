// SPDX-License-Identifier: Apache-2.0
import { useRef, useState } from "react";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  AnchoredPopupAlign,
  AnchoredPopupWidth,
  useAnchoredPopup,
  type AnchoredPopupOptions,
} from "./anchored-popup";

type Rect = { left: number; top: number; width: number; height: number };
const rectOf = ({ left, top, width, height }: Rect): DOMRect =>
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

let anchorRect: Rect, popupRect: Rect, popupScroll: number;
let viewport: { width: number; height: number };
let shown: ReturnType<typeof vi.fn>, hidden: ReturnType<typeof vi.fn>;
const invalid = vi.fn();
let disconnects: number;
let observers: MockObserver[];
class MockObserver {
  callback: () => void;
  constructor(callback: () => void) {
    this.callback = callback;
    observers.push(this);
  }
  observe() {}
  disconnect() {
    disconnects++;
  }
}

beforeEach(() => {
  anchorRect = { left: 40, top: 100, width: 220, height: 40 };
  popupRect = { left: 0, top: 0, width: 220, height: 120 };
  popupScroll = 118;
  viewport = { width: 800, height: 600 };
  disconnects = 0;
  observers = [];
  invalid.mockClear();
  shown = vi.fn();
  hidden = vi.fn();
  vi.stubGlobal("ResizeObserver", MockObserver);
  vi.spyOn(window, "innerWidth", "get").mockImplementation(
    () => viewport.width,
  );
  vi.spyOn(window, "innerHeight", "get").mockImplementation(
    () => viewport.height,
  );
  Object.defineProperty(HTMLElement.prototype, "showPopover", {
    configurable: true,
    value: shown,
  });
  Object.defineProperty(HTMLElement.prototype, "hidePopover", {
    configurable: true,
    value: hidden,
  });
  Object.defineProperty(HTMLElement.prototype, "scrollHeight", {
    configurable: true,
    get() {
      return (this as HTMLElement).dataset.testPopup ? popupScroll : 0;
    },
  });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      if (this.dataset.testPopup) return rectOf(popupRect);
      if (this.dataset.testAnchor) return rectOf(anchorRect);
      if (this.dataset.testBoundary)
        return rectOf({ left: 20, top: 60, width: 400, height: 300 });
      return rectOf({ left: 0, top: 0, width: 0, height: 0 });
    },
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  delete (HTMLElement.prototype as { showPopover?: unknown }).showPopover;
  delete (HTMLElement.prototype as { hidePopover?: unknown }).hidePopover;
  delete (HTMLElement.prototype as { scrollHeight?: unknown }).scrollHeight;
});

function Fixture({
  initial = true,
  withBoundary = false,
  ...options
}: { initial?: boolean; withBoundary?: boolean } & Partial<
  Pick<AnchoredPopupOptions, "width" | "align">
>) {
  const [open, setOpen] = useState(initial);
  const anchor = useRef<HTMLButtonElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  useAnchoredPopup({
    open,
    anchor,
    popup,
    onInvalid: () => {
      invalid();
      setOpen(false);
    },
    boundary: withBoundary
      ? (node) => node.closest<HTMLElement>("[data-test-boundary]")
      : undefined,
    ...options,
  });
  const content = (
    <>
      <button
        ref={anchor}
        data-test-anchor="true"
        onClick={() => setOpen((value) => !value)}
      >
        Anchor
      </button>
      {open ? (
        <div
          ref={popup}
          data-test-popup="true"
          popover="manual"
          className="anchored-popup"
        >
          <p>row</p>
        </div>
      ) : null}
    </>
  );
  return withBoundary ? (
    <section data-test-boundary="true">{content}</section>
  ) : (
    content
  );
}
const popupNode = (container: HTMLElement) =>
  container.querySelector<HTMLElement>("[data-test-popup]")!;

test("anchors 4px below the trigger at trigger width and shows a manual popover", () => {
  const { container } = render(<Fixture />);
  const popup = popupNode(container);
  expect(shown).toHaveBeenCalledOnce();
  expect(popup.getAttribute("popover")).toBe("manual");
  expect(popup.style.left).toBe("40px");
  expect(popup.style.top).toBe("144px");
  expect(popup.style.width).toBe("220px");
  expect(popup.style.maxHeight).toBe("280px");
});

test("flips above, clamps to the owning boundary and caps the height", () => {
  anchorRect = { left: 300, top: 330, width: 220, height: 40 };
  popupScroll = 400;
  const { container } = render(<Fixture withBoundary />);
  const popup = popupNode(container);
  // Boundary spans 20..420 x 60..360 with an 8px inset.
  expect(Number.parseFloat(popup.style.left)).toBeLessThanOrEqual(412 - 220);
  expect(Number.parseFloat(popup.style.left)).toBeGreaterThanOrEqual(28);
  const top = Number.parseFloat(popup.style.top);
  const maxHeight = Number.parseFloat(popup.style.maxHeight);
  expect(maxHeight).toBeLessThanOrEqual(280);
  expect(top).toBeGreaterThanOrEqual(68);
  expect(top + maxHeight).toBeLessThanOrEqual(352 + 0.001);
  // The trigger sits in the lower half, so the popup lies above it.
  expect(top + maxHeight).toBeLessThanOrEqual(330 - 4 + 0.001);
});

test("content width and end alignment keep the right edges together", () => {
  popupRect = { ...popupRect, width: 120 };
  const { container } = render(
    <Fixture
      width={AnchoredPopupWidth.Content}
      align={AnchoredPopupAlign.End}
    />,
  );
  const popup = popupNode(container);
  expect(popup.style.width).toBe("120px");
  expect(Number.parseFloat(popup.style.left)).toBe(260 - 120);
});

test("repositions on scroll outside the popup and resize but not on popup scroll", () => {
  const { container } = render(<Fixture />);
  const popup = popupNode(container);
  expect(popup.style.top).toBe("144px");
  anchorRect = { ...anchorRect, top: 150 };
  fireEvent.scroll(popup);
  expect(popup.style.top).toBe("144px");
  fireEvent.scroll(document.body);
  expect(popup.style.top).toBe("194px");
  anchorRect = { ...anchorRect, left: 60 };
  fireEvent(window, new Event("resize"));
  expect(popup.style.left).toBe("60px");
  anchorRect = { ...anchorRect, top: 170 };
  act(() => observers.forEach((observer) => observer.callback()));
  expect(popup.style.top).toBe("214px");
});

test("repositions when the popup content changes", async () => {
  const { container } = render(<Fixture />);
  const popup = popupNode(container);
  anchorRect = { ...anchorRect, top: 200 };
  await act(async () => {
    popup.append(document.createElement("p"));
    await Promise.resolve();
  });
  expect(popup.style.top).toBe("244px");
});

test("closing and unmounting hide the popover and release every listener", () => {
  const add = vi.spyOn(document, "addEventListener");
  const remove = vi.spyOn(document, "removeEventListener");
  const windowRemove = vi.spyOn(window, "removeEventListener");
  const { container, unmount } = render(<Fixture />);
  const scrollAdds = add.mock.calls.filter(([type]) => type === "scroll");
  expect(scrollAdds).toHaveLength(1);
  fireEvent.click(container.querySelector("button")!);
  expect(hidden).toHaveBeenCalledOnce();
  expect(disconnects).toBe(1);
  expect(remove.mock.calls.filter(([type]) => type === "scroll")).toHaveLength(
    1,
  );
  expect(windowRemove.mock.calls.map(([type]) => type)).toContain("resize");
  fireEvent.click(container.querySelector("button")!);
  expect(shown).toHaveBeenCalledTimes(2);
  unmount();
  expect(disconnects).toBe(2);
  expect(hidden).toHaveBeenCalledTimes(2);
});

test("a disabled, inert or hidden anchor invalidates the popup", () => {
  const { container } = render(<Fixture />);
  const anchor = container.querySelector("button")!;
  act(() => {
    anchor.disabled = true;
  });
  fireEvent.scroll(document.body);
  expect(invalid).toHaveBeenCalled();
  expect(container.querySelector("[data-test-popup]")).toBeNull();
});

test("an anchor inside a closed dialog invalidates the popup", () => {
  const { container } = render(
    <dialog>
      <Fixture />
    </dialog>,
  );
  // jsdom dialogs are closed unless opened, so the popup is never presented.
  expect(invalid).toHaveBeenCalled();
  expect(shown).not.toHaveBeenCalled();
  expect(container.querySelector("[data-test-popup]")).toBeNull();
});
