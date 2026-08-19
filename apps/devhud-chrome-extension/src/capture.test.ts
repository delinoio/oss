// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { injectedCapture } from "./capture.js";

async function select(element: Element) {
  Object.defineProperty(element, "getBoundingClientRect", {
    value: () => ({ x: 1, y: 2, width: 3, height: 4 }),
  });
  const capture = injectedCapture(true);
  element.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
  return await capture;
}

describe("injected capture", () => {
  afterEach(() => {
    vi.useRealTimers();
    window.history.replaceState(null, "", "/");
    document.body.replaceChildren();
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

  it("preserves redacted path structure beyond 16 KiB", async () => {
    const segmentCount = 2_000;
    window.history.replaceState(null, "", `/${"segment/".repeat(segmentCount)}`);

    const result = await injectedCapture(false);

    if (!result) throw new Error("capture was unexpectedly cancelled");
    expect(new TextEncoder().encode(result.url).byteLength).toBeGreaterThan(16 * 1024);
    expect(result.url.match(/<redacted>/gu)).toHaveLength(segmentCount);
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
  });

  it("bounds escaped multibyte text without breaking markup", async () => {
    document.body.innerHTML = `<main>${"<&한".repeat(128 * 1024)}</main>`;

    const result = await select(document.body.firstElementChild!);

    if (!result) throw new Error("capture was unexpectedly cancelled");
    expect(new TextEncoder().encode(result.outerHtml).byteLength).toBeLessThanOrEqual(128 * 1024);
    expect(result.outerHtml.endsWith("</main>")).toBe(true);
  });

  it("cancels an abandoned interactive selection", async () => {
    vi.useFakeTimers();
    const capture = injectedCapture(true);

    await vi.advanceTimersByTimeAsync(30_000);

    await expect(capture).resolves.toBeNull();
  });
});
