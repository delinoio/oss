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
  afterEach(() => vi.useRealTimers());

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

  it("cancels an abandoned interactive selection", async () => {
    vi.useFakeTimers();
    const capture = injectedCapture(true);

    await vi.advanceTimersByTimeAsync(30_000);

    await expect(capture).resolves.toBeNull();
  });
});
