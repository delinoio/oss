// SPDX-License-Identifier: Apache-2.0
// Published engine and DOM compatibility patches, with synthetic output only.
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { afterEach, expect, it, vi } from "vitest";
import { WTerm } from "@wterm/dom";
import { GhosttyCore } from "@wterm/ghostty";
async function setup() {
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(performance.now()), 0));
  vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  const bytes = await readFile(createRequire(import.meta.url).resolve("@wterm/ghostty/ghostty-vt.wasm"));
  vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array(bytes), { headers: { "Content-Type": "application/wasm" } })));
  const core = await GhosttyCore.load({ wasmPath: "https://fixture.test/renderer-ghostty.wasm", scrollbackLimit: 5000, imageStorageLimit: 0 });
  const region = document.createElement("section"); region.setAttribute("role", "region"); region.setAttribute("aria-label", "Terminal output");
  const host = document.createElement("div"); region.append(host); document.body.append(region);
  const onData = vi.fn(), onBinary = vi.fn(), clipboard = vi.fn();
  const terminal = new WTerm(host, { core, autoFocus: false, autoResize: false, inputEnabled: false, announceOutput: true, onData, onBinary, onClipboardWrite: clipboard });
  await terminal.init();
  return { terminal, core, host, region, onData, onBinary, clipboard, dispose() { terminal.destroy(); core.dispose(); } };
}
afterEach(() => { vi.unstubAllGlobals(); document.body.replaceChildren(); });
const write = (terminal: WTerm, value: string) => terminal.write(new TextEncoder().encode(value));
it("suppresses all disabled deliveries and normalizes bracketed paste exactly once", async () => {
  const value = await setup();
  try {
    const { terminal, core, host, onData } = value;
    expect(document.activeElement).not.toBe(host.querySelector("textarea"));
    write(terminal, "\x1b[?2004h\x1b[?1004h\x1b[?1000h\x1b[6n");
    terminal.focus(); terminal.paste("blocked\n");
    const input = host.querySelector("textarea")!;
    input.dispatchEvent(new KeyboardEvent("keydown", { key: "x", code: "KeyX", bubbles: true }));
    input.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    input.dispatchEvent(new CompositionEvent("compositionend", { data: "한", bubbles: true }));
    expect(onData).not.toHaveBeenCalled(); expect(value.onBinary).not.toHaveBeenCalled();
    terminal.setInputEnabled(true); terminal.paste("a\r\nb\n\x1b[201~");
    expect(onData).toHaveBeenCalledExactlyOnceWith("\x1b[200~a\rb\r[201~\x1b[201~");
    onData.mockClear();
    input.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
    input.dispatchEvent(new CompositionEvent("compositionend", { data: "한", bubbles: true }));
    expect(onData).toHaveBeenCalledExactlyOnceWith("한");
    terminal.setInputEnabled(false); terminal.reset(); expect(core.getScrollbackCount()).toBe(0);
    terminal.setInputEnabled(true); onData.mockClear(); terminal.paste("a\r\nb\n\x1b");
    expect(onData).toHaveBeenCalledExactlyOnceWith("a\rb\r\x1b");
  } finally { value.dispose(); }
});
it("creates inert OSC 8 text, consumes OSC 52, and resets selection and partial parser state", async () => {
  const value = await setup();
  try {
    const { terminal, host, region, onData, clipboard } = value; terminal.setInputEnabled(true);
    expect(region.getAttribute("aria-label")).toBe("Terminal output");
    expect(host.hasAttribute("aria-label")).toBe(false);
    // Host clipboard authority remains disconnected in the production adapter.
    terminal.onClipboardWrite = null;
    write(terminal, "\x1b]8;;https://fixture.test/link\x07inert\x1b]8;;\x07\x1b]52;c;c2VjcmV0\x07\x1b]52;c;?\x07");
    await new Promise(resolve => requestAnimationFrame(resolve));
    expect(host.querySelector("a, [href]")).toBeNull(); expect(clipboard).not.toHaveBeenCalled(); expect(onData).not.toHaveBeenCalled();
    terminal.write(new Uint8Array([0xe2])); write(terminal, "\x1b[3"); terminal.reset(); write(terminal, "suffix");
    expect(terminal.getSelectionText()).toBeNull(); expect(await terminal.readText()).toContain("suffix");
    expect(host.querySelector("textarea")?.getAttribute("aria-label")).toBe("Terminal");
    terminal.setAccessibilityStrings({ terminal: "터미널 입력", exitHint: "이동 안내", output: "터미널 출력", limit: "알림 중지" });
    expect(host.querySelector("textarea")?.getAttribute("aria-label")).toBe("터미널 입력");
    expect(host.querySelector('[role="log"]')?.getAttribute("aria-label")).toBe("터미널 출력");
  } finally { value.dispose(); }
});
it("clamps measured dimensions before public engine resize", async () => {
  const value = await setup();
  try {
    let width = 20_000_000, height = 20_000_000;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) {
      return { x: 0, y: 0, top: 0, left: 0, bottom: this === value.host ? height : 16, right: this === value.host ? width : 8,
        width: this === value.host ? width : 8, height: this === value.host ? height : 16, toJSON() { return {}; } };
    });
    value.terminal.fit(); expect(value.terminal.rows).toBe(500); expect(value.terminal.cols).toBe(1000);
    width = 1; height = 1; value.terminal.fit(); expect(value.terminal.rows).toBe(1); expect(value.terminal.cols).toBe(1);
  } finally { value.dispose(); }
});
