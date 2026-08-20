// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { injectedCapture } from "./capture.js";

const originalShowModal = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, "showModal");
const originalClose = Object.getOwnPropertyDescriptor(HTMLDialogElement.prototype, "close");
const originalRangeClientRects = Object.getOwnPropertyDescriptor(Range.prototype, "getClientRects");

async function select(
  element: Element,
  bounds = { x: 1, y: 2, width: 3, height: 4 },
  dispatchSelection?: (overlayWindow: Window) => void,
) {
  vi.useFakeTimers();
  Object.defineProperty(element, "getBoundingClientRect", {
    value: () => bounds,
  });
  const elementFromPoint = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
  Object.defineProperty(document, "elementFromPoint", { configurable: true, value: () => element });
  const capture = injectedCapture(true, location.origin);
  const overlayWindow = document.querySelector("iframe")?.contentWindow;
  if (!overlayWindow) throw new Error("selection overlay was not created");
  if (dispatchSelection) dispatchSelection(overlayWindow);
  else overlayWindow.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, clientX: bounds.x, clientY: bounds.y }));
  try {
    await vi.advanceTimersByTimeAsync(1_000);
    return await capture;
  } finally {
    if (elementFromPoint) Object.defineProperty(document, "elementFromPoint", elementFromPoint);
    else delete (document as Partial<Document>).elementFromPoint;
  }
}

describe("injected capture", () => {
  beforeEach(() => {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockReturnValue({ x: 1, y: 2, width: 100, height: 20 } as DOMRect);
    Object.defineProperty(HTMLDialogElement.prototype, "showModal", {
      configurable: true,
      value: vi.fn(function (this: HTMLDialogElement) { this.setAttribute("open", ""); }),
    });
    Object.defineProperty(HTMLDialogElement.prototype, "close", {
      configurable: true,
      value: vi.fn(function (this: HTMLDialogElement) { this.removeAttribute("open"); }),
    });
    Object.defineProperty(Range.prototype, "getClientRects", {
      configurable: true,
      value: vi.fn(() => [{ x: 1, y: 2, width: 100, height: 20 }]),
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    window.history.replaceState(null, "", "/");
    document.body.replaceChildren();
    if (originalShowModal) Object.defineProperty(HTMLDialogElement.prototype, "showModal", originalShowModal);
    else delete (HTMLDialogElement.prototype as Partial<HTMLDialogElement>).showModal;
    if (originalClose) Object.defineProperty(HTMLDialogElement.prototype, "close", originalClose);
    else delete (HTMLDialogElement.prototype as Partial<HTMLDialogElement>).close;
    if (originalRangeClientRects) Object.defineProperty(Range.prototype, "getClientRects", originalRangeClientRects);
    else delete (Range.prototype as Partial<Range>).getClientRects;
  });

  it("rejects a changed live origin before collecting page context", async () => {
    document.title = "secret";
    document.body.innerHTML = "<main>secret</main>";

    await expect(injectedCapture(false, "https://different.example")).resolves.toBeNull();

    expect(document.querySelector("iframe")).toBeNull();
  });

  it.each([
    '<input type="password" title="secret" aria-label="credential">',
    '<div aria-hidden="true" title="secret">hidden</div>',
  ])("drops selection metadata when the selected element is excluded", async (markup) => {
    document.body.innerHTML = markup;
    const result = await select(document.body.firstElementChild!);
    expect(result).not.toBeNull();
    expect(result?.selectedBounds).toBeNull();
    expect(result?.accessibility).toEqual({});
    expect(result?.outerHtml).toBe("");
  });

  it("bounds non-markup strings by UTF-8 bytes", async () => {
    document.title = "한".repeat(4 * 1024);
    document.body.innerHTML = `<main aria-label="${"한".repeat(4 * 1024)}">safe</main>`;
    const result = await select(document.body.firstElementChild!);
    if (!result) throw new Error("capture was unexpectedly cancelled");
    const encoder = new TextEncoder();
    expect(encoder.encode(result.title).byteLength).toBeLessThanOrEqual(4 * 1024);
    expect(encoder.encode(result.accessibility["aria-label"]).byteLength).toBeLessThanOrEqual(4 * 1024);
  });

  it("reads only allowlisted attributes without enumerating the selected element", async () => {
    document.body.innerHTML = '<main aria-label="safe" data-secret="excluded">visible</main>';
    const selected = document.body.firstElementChild!;
    const attributes = vi.fn(() => {
      throw new Error("attributes must not be enumerated");
    });
    Object.defineProperty(selected, "attributes", { configurable: true, get: attributes });

    const result = await select(selected);

    expect(result?.accessibility).toEqual({ "aria-label": "safe" });
    expect(result?.outerHtml).toBe('<main aria-label="safe">visible</main>');
    expect(attributes).not.toHaveBeenCalled();
  });

  it("does not encode an oversized untrusted string in full", async () => {
    document.title = "x".repeat(1024 * 1024);
    const encode = vi.spyOn(TextEncoder.prototype, "encode");
    try {
      const result = await injectedCapture(false, location.origin);

      if (!result) throw new Error("capture was unexpectedly cancelled");
      expect(result.title).toHaveLength(4 * 1024);
      expect(encode.mock.calls.every(([value]) => (value ?? "").length <= 4 * 1024)).toBe(true);
    } finally {
      encode.mockRestore();
    }
  });

  it.each([
    ["zero width", { x: 1, y: 2, width: 0, height: 4 }],
    ["zero height", { x: 1, y: 2, width: 3, height: 0 }],
    ["negative width", { x: 1, y: 2, width: -1, height: 4 }],
    ["non-finite position", { x: Number.NaN, y: 2, width: 3, height: 4 }],
    ["non-finite dimension", { x: 1, y: 2, width: 3, height: Number.POSITIVE_INFINITY }],
    ["offscreen layout box", { x: innerWidth + 1, y: 2, width: 3, height: 4 }],
  ])("excludes a selection with a %s", async (_case, bounds) => {
    document.body.innerHTML = '<main aria-label="safe">safe</main>';

    const result = await select(document.body.firstElementChild!, bounds);

    expect(result?.selectedBounds).toBeNull();
    expect(result?.accessibility).toEqual({});
    expect(result?.outerHtml).toBe("");
  });

  it("excludes a selection clipped by a zero-area overflow ancestor", async () => {
    document.body.innerHTML = '<div style="width: 0; height: 0; overflow: hidden"><main aria-label="secret">hidden</main></div>';
    Object.defineProperty(document.querySelector("div"), "getBoundingClientRect", {
      value: () => ({ x: 1, y: 2, width: 0, height: 0 }),
    });

    const result = await select(document.querySelector("main")!);

    expect(result?.selectedBounds).toBeNull();
    expect(result?.accessibility).toEqual({});
    expect(result?.outerHtml).toBe("");
  });

  it("does not traverse a fully clipped subtree", async () => {
    document.body.innerHTML = '<main><div style="overflow: hidden"><p>secret</p></div><p>visible</p></main>';
    Object.defineProperty(document.querySelector("div"), "getBoundingClientRect", {
      value: () => ({ x: 1, y: 2, width: 0, height: 0 }),
    });

    const result = await select(document.querySelector("main")!);

    expect(result?.outerHtml).toContain("visible");
    expect(result?.outerHtml).not.toContain("secret");
  });

  it.each([
    ["zero opacity", '<div style="opacity: 0"><p>secret</p></div>'],
    ["collapsed visibility", '<table><tbody><tr style="visibility: collapse"><td>secret</td></tr></tbody></table>'],
  ])("excludes descendants hidden by %s", async (_state, hiddenMarkup) => {
    document.body.innerHTML = `<main>${hiddenMarkup}<p>visible</p></main>`;

    const result = await select(document.querySelector("main")!);

    expect(result?.outerHtml).toContain("visible");
    expect(result?.outerHtml).not.toContain("secret");
  });

  it("excludes a selection hidden by an ancestor", async () => {
    document.body.innerHTML = '<section aria-hidden="true"><main title="secret">hidden</main></section>';

    const result = await select(document.querySelector("main")!);

    expect(result?.selectedBounds).toBeNull();
    expect(result?.accessibility).toEqual({});
    expect(result?.outerHtml).toBe("");
  });

  it("uses the browser visibility check for hidden subtrees", async () => {
    const descriptor = Object.getOwnPropertyDescriptor(Element.prototype, "checkVisibility");
    const checkVisibility = vi.fn(function (this: Element) {
      return !this.matches('[style*="content-visibility: hidden"], [style*="content-visibility: hidden"] *');
    });
    Object.defineProperty(Element.prototype, "checkVisibility", { configurable: true, value: checkVisibility });
    document.body.innerHTML = '<main><div style="content-visibility: hidden"><p>secret</p></div><p>visible</p></main>';

    try {
      const result = await select(document.querySelector("main")!);

      expect(result?.outerHtml).toContain("visible");
      expect(result?.outerHtml).not.toContain("secret");
      expect(checkVisibility).toHaveBeenCalledWith({ checkOpacity: true, checkVisibilityCSS: true, contentVisibilityAuto: true });
    } finally {
      if (descriptor) Object.defineProperty(Element.prototype, "checkVisibility", descriptor);
      else delete (Element.prototype as Partial<Element>).checkVisibility;
    }
  });

  it("excludes text without a positive rendered range", async () => {
    document.body.innerHTML = "<main><p>secret</p><p>visible</p></main>";
    vi.mocked(Range.prototype.getClientRects).mockImplementation(function (this: Range) {
      return (this.startContainer.textContent === "secret" ? [] : [{ x: 1, y: 2, width: 100, height: 20 }]) as unknown as DOMRectList;
    });

    const result = await select(document.querySelector("main")!);

    expect(result?.outerHtml).toContain("visible");
    expect(result?.outerHtml).not.toContain("secret");
  });

  it("excludes fully transparent text", async () => {
    document.body.innerHTML = '<main><p style="color: transparent">secret</p><p>visible</p></main>';

    const result = await select(document.querySelector("main")!);

    expect(result?.outerHtml).toContain("visible");
    expect(result?.outerHtml).not.toContain("secret");
  });

  it("excludes text fully clipped by its own container", async () => {
    document.body.innerHTML = '<main><p style="overflow: hidden">secret</p><p>visible</p></main>';
    vi.mocked(Range.prototype.getClientRects).mockImplementation(function (this: Range) {
      return [this.startContainer.textContent === "secret"
        ? { x: 200, y: 2, width: 100, height: 20 }
        : { x: 1, y: 2, width: 100, height: 20 }] as unknown as DOMRectList;
    });

    const result = await select(document.querySelector("main")!);

    expect(result?.outerHtml).toContain("visible");
    expect(result?.outerHtml).not.toContain("secret");
  });

  it("preserves text when any rendered range intersects the visible area", async () => {
    document.body.innerHTML = "<main>visible</main>";
    vi.mocked(Range.prototype.getClientRects).mockReturnValue([
      { x: innerWidth + 1, y: 2, width: 100, height: 20 },
      { x: -10, y: 2, width: 20, height: 20 },
    ] as unknown as DOMRectList);

    const result = await select(document.querySelector("main")!);

    expect(result?.outerHtml).toBe("<main>visible</main>");
  });

  it("preserves redacted path structure beyond 16 KiB", async () => {
    const segmentCount = 2_000;
    window.history.replaceState(null, "", `/${"segment/".repeat(segmentCount)}`);

    const result = await injectedCapture(false, location.origin);

    if (!result) throw new Error("capture was unexpectedly cancelled");
    expect(new TextEncoder().encode(result.url).byteLength).toBeGreaterThan(16 * 1024);
    expect(result.url.match(/%3Credacted%3E/gu)).toHaveLength(segmentCount);
    expect(result.url.endsWith("/")).toBe(true);
  });

  it("bounds deeply nested markup during iterative traversal", async () => {
    const root = document.createElement("main");
    const elements: Element[] = [root];
    document.body.append(root);
    let parent = root;
    for (let index = 0; index < 1_000; index += 1) {
      const child = document.createElement("div");
      child.title = "x".repeat(2 * 1024);
      parent.append(child);
      elements.push(child);
      parent = child;
    }

    try {
      const result = await select(root);

      if (!result) throw new Error("capture was unexpectedly cancelled");
      expect(result.outerHtml).not.toBe("");
      expect(new TextEncoder().encode(result.outerHtml).byteLength).toBeLessThanOrEqual(128 * 1024);
      expect(result.outerHtml.match(/<div/gu)?.length).toBeLessThan(1_000);
      expect(result.outerHtml.match(/<div/gu)?.length).toBe(result.outerHtml.match(/<\/div>/gu)?.length);
      expect(result.outerHtml.endsWith("</main>")).toBe(true);
    } finally {
      for (let index = elements.length - 1; index >= 0; index -= 1) elements[index]!.remove();
    }
  }, 15_000);

  it("degrades to context-free capture when sanitizer traversal exceeds its node bound", async () => {
    const root = document.createElement("main");
    const fragment = document.createDocumentFragment();
    for (let index = 0; index < 10_001; index += 1) fragment.append(document.createTextNode(""));
    root.append(fragment);
    document.body.append(root);

    const result = await select(root);

    expect(result).toBeNull();
    expect(Range.prototype.getClientRects).toHaveBeenCalledTimes(9_999);
  }, 15_000);

  it("bounds escaped multibyte text without breaking markup", async () => {
    document.body.innerHTML = `<main>${"<&한".repeat(128 * 1024)}</main>`;

    const result = await select(document.body.firstElementChild!);

    if (!result) throw new Error("capture was unexpectedly cancelled");
    expect(new TextEncoder().encode(result.outerHtml).byteLength).toBeLessThanOrEqual(128 * 1024);
    expect(result.outerHtml.endsWith("</main>")).toBe(true);
  });

  it("isolates the complete pointer sequence from the selected page", async () => {
    document.body.innerHTML = "<main>safe</main>";
    const pagePointerHandler = vi.fn();
    for (const eventName of ["pointerdown", "mousedown", "mouseup", "click"]) {
      document.addEventListener(eventName, pagePointerHandler);
    }

    try {
      const result = await select(document.body.firstElementChild!, undefined, (overlayWindow) => {
        for (const eventName of ["pointerdown", "mousedown", "mouseup", "click"]) {
          overlayWindow.dispatchEvent(new MouseEvent(eventName, { bubbles: true, cancelable: true, clientX: 1, clientY: 2 }));
        }
      });

      expect(result?.outerHtml).toBe("<main>safe</main>");
      expect(pagePointerHandler).not.toHaveBeenCalled();
      expect(document.querySelector("iframe")).toBeNull();
    } finally {
      for (const eventName of ["pointerdown", "mousedown", "mouseup", "click"]) {
        document.removeEventListener(eventName, pagePointerHandler);
      }
    }
  });

  it("keeps the selection shield through double-click completion", async () => {
    vi.useFakeTimers();
    document.body.innerHTML = '<a href="#activated">safe</a>';
    const selected = document.querySelector("a")!;
    const pagePointerHandler = vi.fn();
    for (const eventName of ["pointerdown", "mousedown", "mouseup", "click", "dblclick"]) {
      document.addEventListener(eventName, pagePointerHandler);
    }
    const elementFromPoint = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
    Object.defineProperty(document, "elementFromPoint", { configurable: true, value: () => selected });

    try {
      const capture = injectedCapture(true, location.origin);
      const overlay = document.querySelector("iframe");
      const overlayWindow = overlay?.contentWindow;
      if (!overlay || !overlayWindow) throw new Error("selection overlay was not created");

      for (const detail of [1, 2]) {
        for (const eventName of ["pointerdown", "mousedown", "mouseup", "click"]) {
          overlayWindow.dispatchEvent(new MouseEvent(eventName, { bubbles: true, cancelable: true, clientX: 1, clientY: 2, detail }));
        }
      }
      overlayWindow.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, clientX: 1, clientY: 2, detail: 2 }));

      expect(pagePointerHandler).not.toHaveBeenCalled();
      expect(document.querySelector("iframe")).toBe(overlay);
      await vi.advanceTimersByTimeAsync(999);
      expect(document.querySelector("iframe")).toBe(overlay);
      await vi.advanceTimersByTimeAsync(1);
      await expect(capture).resolves.toMatchObject({ outerHtml: "<a>safe</a>" });
      expect(document.querySelector("iframe")).toBeNull();
      expect(location.hash).toBe("");
    } finally {
      if (elementFromPoint) Object.defineProperty(document, "elementFromPoint", elementFromPoint);
      else delete (document as Partial<Document>).elementFromPoint;
      for (const eventName of ["pointerdown", "mousedown", "mouseup", "click", "dblclick"]) {
        document.removeEventListener(eventName, pagePointerHandler);
      }
    }
  });

  it("places the selection shield in a modal top-layer dialog", async () => {
    document.body.innerHTML = "<dialog open>page modal</dialog><main>safe</main>";

    const capture = injectedCapture(true, location.origin);
    const overlay = document.querySelector("iframe");
    const shield = overlay?.parentElement;
    if (!(shield instanceof HTMLDialogElement) || !overlay?.contentWindow) throw new Error("selection shield was not created");

    expect(HTMLDialogElement.prototype.showModal).toHaveBeenCalledOnce();
    expect(shield.open).toBe(true);

    overlay.contentWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    await expect(capture).resolves.toBeNull();
    expect(document.querySelectorAll("dialog")).toHaveLength(1);
  });

  it("closes the top-layer shield before hit testing the page", async () => {
    vi.useFakeTimers();
    document.body.innerHTML = "<main>safe</main>";
    const selected = document.querySelector("main")!;
    const elementFromPoint = Object.getOwnPropertyDescriptor(document, "elementFromPoint");
    const capture = injectedCapture(true, location.origin);
    const overlay = document.querySelector("iframe");
    const shield = overlay?.parentElement;
    if (!(shield instanceof HTMLDialogElement) || !overlay?.contentWindow) throw new Error("selection shield was not created");
    Object.defineProperty(document, "elementFromPoint", {
      configurable: true,
      value: vi.fn(() => {
        expect(shield.open).toBe(false);
        return selected;
      }),
    });

    try {
      overlay.contentWindow.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true, clientX: 1, clientY: 2 }));
      await vi.advanceTimersByTimeAsync(1_000);
      await expect(capture).resolves.toMatchObject({ outerHtml: "<main>safe</main>" });
    } finally {
      if (elementFromPoint) Object.defineProperty(document, "elementFromPoint", elementFromPoint);
      else delete (document as Partial<Document>).elementFromPoint;
    }
  });

  it("degrades to manual selection when the top-layer shield cannot open", async () => {
    vi.mocked(HTMLDialogElement.prototype.showModal).mockImplementationOnce(() => { throw new DOMException("blocked"); });

    await expect(injectedCapture(true, location.origin)).resolves.toBeNull();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("degrades to manual selection when the page exceeds the picker scan budget", async () => {
    const fragment = document.createDocumentFragment();
    for (let index = 0; index < 10_000; index += 1) fragment.append(document.createElement("div"));
    document.body.append(fragment);

    await expect(injectedCapture(true, location.origin)).resolves.toBeNull();
    expect(document.querySelector("iframe")).toBeNull();
    expect(HTMLDialogElement.prototype.showModal).not.toHaveBeenCalled();
  });

  it("cancels selection with Escape and removes the overlay", async () => {
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toBeNull();
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("exposes a localized focusable selection dialog", async () => {
    const capture = injectedCapture(true, location.origin, "ko");
    const overlay = document.querySelector("iframe");
    const overlayDocument = overlay?.contentDocument;
    if (!overlay || !overlayDocument) throw new Error("selection overlay was not created");

    expect(overlay.tabIndex).toBe(0);
    expect(overlay.hasAttribute("aria-hidden")).toBe(false);
    expect(overlay.title).toBe("DevHUD 요소 선택");
    expect(overlayDocument.documentElement.lang).toBe("ko");
    expect(overlayDocument.body.getAttribute("role")).toBe("dialog");
    expect(overlayDocument.body.getAttribute("aria-modal")).toBe("true");
    expect(overlayDocument.body.textContent).toContain("Shift+Tab");

    overlay.contentWindow!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    await expect(capture).resolves.toBeNull();
  });

  it("cycles eligible elements with Tab and confirms with Enter", async () => {
    document.body.innerHTML = '<main aria-label="first">first</main><article aria-label="second">second</article>';
    for (const [index, element] of Array.from(document.body.children).entries()) {
      Object.defineProperty(element, "getBoundingClientRect", {
        value: () => ({ x: index * 10, y: index * 10, width: 10, height: 10 }),
      });
    }
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toMatchObject({ outerHtml: '<article aria-label="second">second</article>' });
  });

  it("starts on the first visible candidate when the first DOM candidate is hidden", async () => {
    document.body.innerHTML = "<main hidden>hidden</main><article>visible</article>";
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toMatchObject({ outerHtml: "<article>visible</article>" });
  });

  it("skips keyboard candidates clipped by a zero-area overflow ancestor", async () => {
    document.body.innerHTML = '<div style="width: 0; height: 0; overflow: hidden"><main>hidden</main></div><article>visible</article>';
    Object.defineProperty(document.querySelector("div"), "getBoundingClientRect", {
      value: () => ({ x: 1, y: 2, width: 0, height: 0 }),
    });
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true }));
    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toMatchObject({ outerHtml: "<article>visible</article>" });
  });

  it("starts on the previously focused eligible element", async () => {
    document.body.innerHTML = '<a href="#first">first</a><a href="#second">second</a>';
    const focused = document.querySelectorAll("a")[1]!;
    focused.focus();
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toMatchObject({ outerHtml: '<a>second</a>' });
  });

  it("wraps backward with Shift+Tab and confirms with Space", async () => {
    document.body.innerHTML = "<main>first</main><article>second</article>";
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Tab", shiftKey: true, bubbles: true, cancelable: true }));
    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: " ", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toMatchObject({ outerHtml: "<article>second</article>" });
  });

  it("restores page focus after keyboard cancellation", async () => {
    document.body.innerHTML = '<a href="#target">target</a>';
    const trigger = document.querySelector("a") as HTMLAnchorElement;
    trigger.focus();
    const capture = injectedCapture(true, location.origin);
    const overlayWindow = document.querySelector("iframe")?.contentWindow;
    if (!overlayWindow) throw new Error("selection overlay was not created");

    overlayWindow.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));

    await expect(capture).resolves.toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("cancels an abandoned interactive selection", async () => {
    vi.useFakeTimers();
    const capture = injectedCapture(true, location.origin);

    await vi.advanceTimersByTimeAsync(30_000);

    await expect(capture).resolves.toBeNull();
    expect(document.querySelector("iframe")).toBeNull();
  });
});
