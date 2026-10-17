// SPDX-License-Identifier: Apache-2.0
import { useRef, useState, type CSSProperties } from "react";
import { act, cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import {
  AnchoredPopupAlign,
  AnchoredPopupWidth,
  useAnchoredPopup,
  useOutsidePointerDismiss,
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
let clipRects: Record<string, Rect>;
let viewport: { width: number; height: number };
let shown: ReturnType<typeof vi.fn>, hidden: ReturnType<typeof vi.fn>;
const invalid = vi.fn();
let disconnects: number;
let observers: MockObserver[];
class MockObserver {
  callback: () => void;
  observed: Element[] = [];
  constructor(callback: () => void) {
    this.callback = callback;
    observers.push(this);
  }
  observe(target: Element) {
    this.observed.push(target);
  }
  disconnect() {
    disconnects++;
  }
}

beforeEach(() => {
  anchorRect = { left: 40, top: 100, width: 220, height: 40 };
  popupRect = { left: 0, top: 0, width: 220, height: 120 };
  popupScroll = 118;
  viewport = { width: 800, height: 600 };
  clipRects = {};
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
      if (this.dataset.testClip)
        return rectOf(
          clipRects[this.dataset.testClip] ?? {
            left: 0,
            top: 0,
            width: 0,
            height: 0,
          },
        );
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

// Wrappers around the anchor, outermost first; each one is a clipping (or
// deliberately non-clipping) ancestor whose box comes from `clipRects`.
type ClipOwner = { id: string; style: CSSProperties };
function Fixture({
  initial = true,
  withBoundary = false,
  clips = [],
  anchorStyle,
  ...options
}: {
  initial?: boolean;
  withBoundary?: boolean;
  clips?: ClipOwner[];
  anchorStyle?: CSSProperties;
} & Partial<Pick<AnchoredPopupOptions, "width" | "align">>) {
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
        style={anchorStyle}
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
  const wrapped = clips.reduceRight(
    (inner, owner) => (
      <div data-test-clip={owner.id} style={owner.style}>
        {inner}
      </div>
    ),
    content,
  );
  return withBoundary ? (
    <section data-test-boundary="true">{wrapped}</section>
  ) : (
    wrapped
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

test("repositions when a row before the anchor is inserted or removed", async () => {
  const { container } = render(<Fixture />);
  const popup = popupNode(container);
  expect(popup.style.top).toBe("144px");
  const row = document.createElement("div");
  // A sibling inserted before the anchor's row shifts it without any resize.
  anchorRect = { ...anchorRect, top: 160 };
  await act(async () => {
    container.insertBefore(row, container.firstChild);
    await Promise.resolve();
  });
  expect(popup.style.top).toBe("204px");
  anchorRect = { ...anchorRect, top: 100 };
  await act(async () => {
    row.remove();
    await Promise.resolve();
  });
  expect(popup.style.top).toBe("144px");
});

test("observes every ancestor for size changes that shift the anchor", () => {
  const { container } = render(<Fixture />);
  const anchor = container.querySelector("button")!;
  const observed = observers.flatMap((observer) => observer.observed);
  for (
    let owner: HTMLElement | null = anchor;
    owner;
    owner = owner.parentElement
  )
    expect(observed).toContain(owner);
  expect(observed).toContain(popupNode(container));
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

const auto: CSSProperties = { overflow: "auto" };
const rowRect: Rect = { left: 40, top: 100, width: 220, height: 40 };

test("closes when the anchor scrolls completely out of its clipping owner", () => {
  clipRects = { list: { left: 0, top: 0, width: 400, height: 300 } };
  const { container } = render(
    <Fixture clips={[{ id: "list", style: auto }]} />,
  );
  expect(invalid).not.toHaveBeenCalled();
  expect(popupNode(container).style.top).toBe("144px");
  // Partially visible rows keep their menu.
  anchorRect = { ...rowRect, top: 280 };
  fireEvent.scroll(document.body);
  expect(invalid).not.toHaveBeenCalled();
  expect(popupNode(container).style.top).toBe("324px");
  // Scrolled entirely below, then entirely above, the owner's box.
  anchorRect = { ...rowRect, top: 300 };
  fireEvent.scroll(document.body);
  expect(invalid).toHaveBeenCalledOnce();
  expect(container.querySelector("[data-test-popup]")).toBeNull();
  expect(hidden).toHaveBeenCalled();
});

test("closes when the anchor leaves the box of an outer clipping ancestor only", () => {
  // The inner list still contains the row, but the tray that scrolls the whole
  // list away does not: every clipping ancestor counts, not only the nearest.
  clipRects = {
    tray: { left: 0, top: 400, width: 400, height: 200 },
    list: { left: 0, top: 0, width: 400, height: 300 },
  };
  const { container } = render(
    <Fixture
      clips={[
        { id: "tray", style: { overflowY: "scroll" } },
        { id: "list", style: auto },
      ]}
    />,
  );
  // The anchor starts inside the list, but the tray scrolled it away from view.
  expect(invalid).toHaveBeenCalledOnce();
  expect(container.querySelector("[data-test-popup]")).toBeNull();
});

test("uses the intersection of nested clipping owners", () => {
  clipRects = {
    outer: { left: 0, top: 0, width: 400, height: 500 },
    inner: { left: 0, top: 0, width: 400, height: 300 },
  };
  const { container } = render(
    <Fixture
      clips={[
        { id: "outer", style: { overflow: "hidden" } },
        { id: "inner", style: auto },
      ]}
    />,
  );
  expect(invalid).not.toHaveBeenCalled();
  // Visible through the outer owner but below the inner one.
  anchorRect = { ...rowRect, top: 320 };
  fireEvent.scroll(document.body);
  expect(invalid).toHaveBeenCalledOnce();
  expect(container.querySelector("[data-test-popup]")).toBeNull();
});

test("owners that do not intersect leave nothing visible", () => {
  clipRects = {
    outer: { left: 0, top: 0, width: 400, height: 100 },
    inner: { left: 0, top: 200, width: 400, height: 100 },
  };
  render(
    <Fixture
      clips={[
        { id: "outer", style: auto },
        { id: "inner", style: auto },
      ]}
    />,
  );
  expect(invalid).toHaveBeenCalledOnce();
});

test("ancestors that do not clip, or have no measurable box, never close the popup", () => {
  clipRects = {
    plain: { left: 500, top: 500, width: 50, height: 50 },
    contents: { left: 500, top: 500, width: 50, height: 50 },
    unmeasured: { left: 0, top: 0, width: 0, height: 0 },
  };
  const { container } = render(
    <Fixture
      clips={[
        { id: "plain", style: { overflow: "visible" } },
        { id: "contents", style: { display: "contents", overflow: "auto" } },
        { id: "unmeasured", style: auto },
      ]}
    />,
  );
  fireEvent.scroll(document.body);
  expect(invalid).not.toHaveBeenCalled();
  expect(popupNode(container).style.top).toBe("144px");
});

test("a fixed anchor is not clipped and an absolute one only from its containing block", () => {
  clipRects = {
    list: { left: 0, top: 400, width: 400, height: 100 },
    containing: { left: 0, top: 0, width: 400, height: 300 },
  };
  const fixed = render(
    <Fixture
      anchorStyle={{ position: "fixed" }}
      clips={[{ id: "list", style: auto }]}
    />,
  );
  expect(invalid).not.toHaveBeenCalled();
  fixed.unmount();
  // The scrolling list is not the containing block of the absolute anchor, so
  // it does not clip it; the positioned ancestor beyond it does.
  const absolute = render(
    <Fixture
      anchorStyle={{ position: "absolute" }}
      clips={[
        {
          id: "containing",
          style: { overflow: "hidden", position: "relative" },
        },
        { id: "list", style: auto },
      ]}
    />,
  );
  expect(invalid).not.toHaveBeenCalled();
  expect(popupNode(absolute.container).style.top).toBe("144px");
  anchorRect = { ...rowRect, top: 320 };
  fireEvent.scroll(document.body);
  expect(invalid).toHaveBeenCalledOnce();
});

test("closes when the page scrolls the anchor out of the window", () => {
  const { container } = render(<Fixture />);
  expect(invalid).not.toHaveBeenCalled();
  // Partly inside the window keeps the popup.
  anchorRect = { ...rowRect, top: viewport.height - 10 };
  fireEvent.scroll(document.body);
  expect(invalid).not.toHaveBeenCalled();
  // It flips above the anchor instead of leaving the window.
  expect(Number.parseFloat(popupNode(container).style.top)).toBeLessThan(
    viewport.height,
  );
  anchorRect = { ...rowRect, top: viewport.height };
  fireEvent.scroll(document.body);
  expect(invalid).toHaveBeenCalledOnce();
  expect(container.querySelector("[data-test-popup]")).toBeNull();
});

test("an anchor without a measurable box is not judged by the window", () => {
  anchorRect = { left: 0, top: 0, width: 0, height: 0 };
  const { container } = render(<Fixture />);
  expect(invalid).not.toHaveBeenCalled();
  expect(popupNode(container)).toBeTruthy();
});

test("an outside pointer press dismisses, inside presses and closed state do not", () => {
  const dismissed = vi.fn();
  function Probe({ open }: { open: boolean }) {
    const anchor = useRef<HTMLButtonElement>(null),
      popup = useRef<HTMLDivElement>(null);
    useOutsidePointerDismiss(open, [anchor, popup], dismissed);
    return (
      <>
        <button ref={anchor}>anchor</button>
        <div ref={popup}>
          <span data-inside>inside</span>
        </div>
        <p data-outside>outside</p>
      </>
    );
  }
  const view = render(<Probe open={false} />);
  fireEvent.pointerDown(view.container.querySelector("[data-outside]")!);
  expect(dismissed).not.toHaveBeenCalled();
  view.rerender(<Probe open />);
  fireEvent.pointerDown(view.container.querySelector("[data-inside]")!);
  fireEvent.pointerDown(view.container.querySelector("button")!);
  expect(dismissed).not.toHaveBeenCalled();
  fireEvent.pointerDown(view.container.querySelector("[data-outside]")!);
  expect(dismissed).toHaveBeenCalledOnce();
  view.rerender(<Probe open={false} />);
  fireEvent.pointerDown(view.container.querySelector("[data-outside]")!);
  expect(dismissed).toHaveBeenCalledOnce();
});
