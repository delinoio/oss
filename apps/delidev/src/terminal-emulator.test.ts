// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { i18n, SupportedLanguage } from "./localization";
const fixture = vi.hoisted(() => ({
  core: { dispose: vi.fn() },
  renderer: { cols: 80, rows: 24, destroy: vi.fn(), init: vi.fn(), setThemeColors: vi.fn(), setAccessibility: vi.fn(), setInputEnabled: vi.fn(), measureDimensions: vi.fn(() => ({ rows: 900, cols: 2000 })), resize: vi.fn(), focus: vi.fn(), write: vi.fn(), reset: vi.fn(), paste: vi.fn() },
  load: vi.fn(), construct: vi.fn(), disconnect: vi.fn(),
}));
vi.mock("@wterm/ghostty", () => ({ GhosttyCore: { load: fixture.load } }));
vi.mock("@wterm/dom", () => ({ WTerm: function (...args: unknown[]) { fixture.construct(...args); return fixture.renderer; } }));
vi.mock("@wterm/ghostty/ghostty-vt.wasm", () => ({ default: "/static/wasm/ghostty-vt.hashed.wasm" }));
import { openTerminalScreen } from "./terminal-emulator";
beforeEach(() => {
  vi.clearAllMocks(); fixture.load.mockResolvedValue(fixture.core); fixture.renderer.init.mockResolvedValue(undefined);
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect = fixture.disconnect; });
});
afterEach(() => vi.unstubAllGlobals());
const host = () => { const value = document.createElement("div"); document.body.append(value); Object.defineProperties(value, { clientWidth: { value: 600 }, clientHeight: { value: 400 } }); return value; };
it("serializes raw output and reset, bounds resize and localizes existing accessibility", async () => {
  let loaded!: (value: unknown) => void; fixture.load.mockReturnValue(new Promise(resolve => { loaded = resolve; }));
  const resize = vi.fn(), screen = openTerminalScreen(host(), vi.fn(), resize, vi.fn());
  const first = new Uint8Array([0xe2, 0x82]), second = new Uint8Array([0xac]);
  const writes = [screen.write(first), screen.write(second, true)]; screen.enabled(true);
  expect(fixture.renderer.write).not.toHaveBeenCalled(); loaded(fixture.core); await Promise.all(writes);
  expect(fixture.load).toHaveBeenCalledWith({ wasmPath: "/static/wasm/ghostty-vt.hashed.wasm", scrollbackLimit: 5000, imageStorageLimit: 0 });
  expect(fixture.construct.mock.calls[0][1]).toMatchObject({ autoResize: false, autoFocus: false, inputEnabled: false, debug: false, announceOutput: true });
  expect(fixture.renderer.write.mock.calls).toEqual([[first], [second]]);
  expect(fixture.renderer.reset.mock.invocationCallOrder[0]).toBeLessThan(fixture.renderer.write.mock.invocationCallOrder[1]);
  expect(resize).toHaveBeenCalledWith(500, 1000); expect(fixture.renderer.focus).not.toHaveBeenCalled();
  screen.focus(); expect(fixture.renderer.focus).toHaveBeenCalledOnce();
  await i18n.changeLanguage(SupportedLanguage.Korean); expect(fixture.renderer.setAccessibility.mock.calls.at(-1)?.[0].exitHint).toContain("터미널");
  screen.dispose(); expect(fixture.renderer.destroy).toHaveBeenCalledOnce(); expect(fixture.core.dispose).toHaveBeenCalledOnce(); expect(fixture.disconnect).toHaveBeenCalledOnce();
});
it("settles disposed writes and releases a late core without focus", async () => {
  let loaded!: (value: unknown) => void; fixture.load.mockReturnValue(new Promise(resolve => { loaded = resolve; }));
  const screen = openTerminalScreen(host(), vi.fn(), vi.fn(), vi.fn()); screen.focus(); screen.enabled(true);
  const write = screen.write(new Uint8Array([65])); screen.dispose(); await write; loaded(fixture.core); await Promise.resolve(); await Promise.resolve();
  expect(fixture.core.dispose).toHaveBeenCalledOnce(); expect(fixture.construct).not.toHaveBeenCalled(); expect(fixture.renderer.focus).not.toHaveBeenCalled();
});
it("releases initialization failures with content-free diagnostics and recovery", async () => {
  fixture.renderer.init.mockRejectedValue(new Error("untrusted output"));
  const warn = vi.spyOn(console, "warn").mockImplementation(() => {}), unavailable = vi.fn();
  const screen = openTerminalScreen(host(), vi.fn(), vi.fn(), unavailable); screen.enabled(true); await screen.write(new Uint8Array([65]));
  expect(unavailable).toHaveBeenCalledOnce(); expect(warn).toHaveBeenCalledWith({ event: "delidev_terminal_renderer_unavailable", stage: "initialization" });
  expect(fixture.renderer.destroy).toHaveBeenCalledOnce(); expect(fixture.core.dispose).toHaveBeenCalledOnce(); screen.dispose(); expect(fixture.renderer.write).not.toHaveBeenCalled();
});
it("intercepts shortcuts and paste once, gates text and preserves binary bytes", async () => {
  const element = host(), input = vi.fn(), screen = openTerminalScreen(element, input, vi.fn(), vi.fn(), () => true);
  await screen.write(new Uint8Array()); screen.enabled(true);
  const options = fixture.construct.mock.calls[0][1] as { onData: (value: string) => void; onBinary: (value: Uint8Array) => void };
  options.onData("한"); const bytes = new Uint8Array([0, 255]); options.onBinary(bytes); expect(input.mock.calls).toEqual([[new TextEncoder().encode("한")], [bytes]]);
  const bubbled = vi.fn(); element.addEventListener("paste", bubbled);
  const paste = new Event("paste", { bubbles: true, cancelable: true }); Object.defineProperty(paste, "clipboardData", { value: { getData: () => "pasted" } }); element.dispatchEvent(paste);
  expect(fixture.renderer.paste).toHaveBeenCalledOnce(); expect(bubbled).not.toHaveBeenCalled();
  const key = new KeyboardEvent("keydown", { key: "1", bubbles: true, cancelable: true }); element.dispatchEvent(key); expect(key.defaultPrevented).toBe(true);
  screen.enabled(false); options.onData("blocked"); options.onBinary(bytes); element.dispatchEvent(paste); expect(input).toHaveBeenCalledTimes(2); expect(fixture.renderer.paste).toHaveBeenCalledOnce(); screen.dispose();
});
