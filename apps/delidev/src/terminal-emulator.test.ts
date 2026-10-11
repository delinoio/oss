// SPDX-License-Identifier: Apache-2.0
import { afterEach, expect, it, vi } from "vitest";
import { i18n } from "./localization";
const fixture = vi.hoisted(() => ({ load: vi.fn(), terms: [] as any[], cores: [] as any[] }));
vi.mock("@wterm/ghostty/ghostty-vt.wasm?url", () => ({ default: "/static/wasm/ghostty.hash.wasm" }));
vi.mock("@wterm/ghostty", () => ({ GhosttyCore: { load: fixture.load } }));
vi.mock("@wterm/dom", () => ({ WTerm: class {
  rows = 24; cols = 80; init = vi.fn(async () => {}); destroy = vi.fn(); reset = vi.fn(); focus = vi.fn(); fit = vi.fn();
  setThemeColors = vi.fn(); setInputEnabled = vi.fn(); setAccessibilityStrings = vi.fn();
  write = vi.fn(); paste = vi.fn();
  constructor(readonly host: HTMLElement, readonly options: any) { fixture.terms.push(this); }
} }));
import { openTerminalScreen } from "./terminal-emulator";
function core() { const value = { dispose: vi.fn() }; fixture.cores.push(value); return value; }
function host() { const value = document.createElement("div"); document.body.append(value); return value; }
function observer() { vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} }); }
afterEach(() => { fixture.terms.length = 0; fixture.cores.length = 0; fixture.load.mockReset(); vi.unstubAllGlobals(); document.body.replaceChildren(); });
it("queues original bytes and explicit reset behind one asynchronous initialization", async () => {
  observer(); let resolve!: (value: any) => void;
  fixture.load.mockReturnValue(new Promise(done => { resolve = done; }));
  const unavailable = vi.fn(), input = vi.fn(), screen = openTerminalScreen(host(), input, vi.fn(), unavailable);
  screen.enabled(true); screen.focus();
  const first = screen.write(new Uint8Array([0xe2, 0x82])), second = screen.write(new Uint8Array([0xac]), true);
  expect(fixture.terms).toHaveLength(0); expect(fixture.load).toHaveBeenCalledTimes(1);
  resolve(core()); await Promise.all([first, second]);
  const terminal = fixture.terms[0];
  expect(terminal.options).toMatchObject({ autoFocus: false, autoResize: false, inputEnabled: false, debug: false, announceOutput: true });
  expect(fixture.load).toHaveBeenCalledWith({ wasmPath: "/static/wasm/ghostty.hash.wasm", scrollbackLimit: 5000, imageStorageLimit: 0 });
  expect(terminal.write.mock.calls.map(([bytes]: [Uint8Array]) => [...bytes])).toEqual([[0xe2, 0x82], [0xac]]);
  expect(terminal.reset).toHaveBeenCalledTimes(1); expect(terminal.focus).toHaveBeenCalledTimes(1); expect(unavailable).not.toHaveBeenCalled();
  screen.enabled(false); terminal.options.onData("blocked"); terminal.options.onBinary(new Uint8Array([255])); expect(input).not.toHaveBeenCalled();
  screen.enabled(true); terminal.options.onBinary(new Uint8Array([255])); expect(input).toHaveBeenCalledWith(new Uint8Array([255]));
  screen.dispose(); screen.dispose(); expect(terminal.destroy).toHaveBeenCalledTimes(1); expect(fixture.cores[0].dispose).toHaveBeenCalledTimes(1);
});
it("disposes late cores without creating a renderer or stealing focus", async () => {
  observer(); let resolve!: (value: any) => void; fixture.load.mockReturnValue(new Promise(done => { resolve = done; }));
  const screen = openTerminalScreen(host(), vi.fn(), vi.fn(), vi.fn()); screen.focus(); const write = screen.write(new Uint8Array([97])); screen.dispose();
  await write; const loaded = core(); resolve(loaded); await Promise.resolve(); await Promise.resolve();
  expect(fixture.terms).toHaveLength(0); expect(loaded.dispose).toHaveBeenCalledTimes(1);
});
it("fails closed with content-free errors and exactly one complete teardown", async () => {
  observer(); fixture.load.mockResolvedValue(core()); const unavailable = vi.fn(); const screen = openTerminalScreen(host(), vi.fn(), vi.fn(), unavailable);
  await screen.write(new Uint8Array()); const terminal = fixture.terms[0]; terminal.write.mockImplementation(() => { throw new Error("sensitive synthetic command/path"); });
  const log = vi.spyOn(console, "warn").mockImplementation(() => {});
  await expect(screen.write(new Uint8Array([97]))).rejects.toThrow("DELIDEV_TERMINAL_RENDERER_UNAVAILABLE");
  expect(unavailable).toHaveBeenCalledTimes(1); expect(log.mock.calls).toEqual([["delidev.terminal.renderer_unavailable", { stage: "write", classification: "renderer-unavailable" }]]);
  screen.dispose(); expect(terminal.destroy).toHaveBeenCalledTimes(1); expect(fixture.cores[0].dispose).toHaveBeenCalledTimes(1);
});
it("captures each paste once and updates accessibility without replacing the renderer", async () => {
  observer(); fixture.load.mockResolvedValue(core()); const element = host(), shortcut = vi.fn(() => true);
  const screen = openTerminalScreen(element, vi.fn(), vi.fn(), vi.fn(), shortcut); await screen.write(new Uint8Array()); screen.enabled(true);
  const terminal = fixture.terms[0], event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", { value: { getData: () => "a\r\nb\n" } }); element.dispatchEvent(event);
  expect(event.defaultPrevented).toBe(true); expect(terminal.paste).toHaveBeenCalledExactlyOnceWith("a\r\nb\n");
  element.dispatchEvent(new KeyboardEvent("keydown", { key: "t", bubbles: true, cancelable: true })); expect(shortcut).toHaveBeenCalledTimes(1);
  await i18n.changeLanguage("ko"); expect(terminal.setAccessibilityStrings).toHaveBeenLastCalledWith(expect.objectContaining({ output: "터미널 출력" }));
  expect(fixture.terms).toHaveLength(1); screen.dispose();
});

it("reports invalid or missing WASM without exposing the loader diagnostic", async () => {
  observer(); fixture.load.mockRejectedValue(new Error("missing /private/fixture/path.wasm"));
  const unavailable=vi.fn(), log=vi.spyOn(console,"warn").mockImplementation(()=>{});
  const screen=openTerminalScreen(host(),vi.fn(),vi.fn(),unavailable);
  await expect(screen.write(new Uint8Array([97]))).rejects.toThrow("DELIDEV_TERMINAL_RENDERER_UNAVAILABLE");
  expect(unavailable).toHaveBeenCalledTimes(1); expect(fixture.terms).toHaveLength(0);
  expect(log.mock.calls).toEqual([["delidev.terminal.renderer_unavailable",{stage:"initialize",classification:"renderer-unavailable"}]]);
  screen.focus(); screen.dispose();
});
