// SPDX-License-Identifier: Apache-2.0
import { beforeEach, expect, it, vi } from "vitest";
const engine = vi.hoisted(() => ({ load: vi.fn(), made: [] as any[], init: Promise.resolve(), error: false }));
vi.mock("@wterm/ghostty", () => ({ GhosttyCore: { load: engine.load } }));
vi.mock("@wterm/dom", () => ({ WTerm: class {
  cols = 80; rows = 24; options: any; writes: Uint8Array[] = []; calls: string[] = [];
  constructor(_host: HTMLElement, options: any) { this.options = options; engine.made.push(this); }
  init() { return engine.init; }
  localize() {}
  setInputEnabled(value: boolean) { this.calls.push(`enabled:${value}`); }
  measureDimensions() { return null; }
  resize() { throw new Error("Unexpected fixture resize"); }
  focus() { this.calls.push("focus"); }
  reset() { this.calls.push("reset"); }
  write(bytes: Uint8Array) { if (engine.error) throw new Error("private native data"); this.writes.push(bytes); this.calls.push("write"); }
  paste(text: string) { this.options.onData(text); }
  destroy() { this.calls.push("destroy"); }
} }));
import { openTerminalScreen } from "./terminal-emulator";
const flush = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); };
const deferred = <T,>() => { let resolve!: (value: T) => void; let reject!: (value: unknown) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
beforeEach(() => {
  engine.load.mockReset(); engine.made.length = 0; engine.init = Promise.resolve(); engine.error = false;
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
});
it("keeps the synchronous adapter and serializes original bytes after one initialization", async () => {
  const loaded = deferred<any>(), owned = { dispose: vi.fn() }; engine.load.mockReturnValue(loaded.promise);
  const host = document.createElement("div"), input = vi.fn(); const screen = openTerminalScreen(host,input,vi.fn(),vi.fn());
  screen.enabled(true); screen.focus(); const bytes = new Uint8Array([0xe2,0x82]); const first = screen.write(bytes); bytes.fill(0); const second = screen.write(new Uint8Array([0xac]),true);
  expect(engine.made).toHaveLength(0); loaded.resolve(owned); await Promise.all([first,second]);
  const made = engine.made[0]; expect(engine.load).toHaveBeenCalledTimes(1); expect(engine.load.mock.calls[0][0]).toMatchObject({scrollbackLimit:5000,imageStorageLimit:0});
  expect(made.options).toMatchObject({autoFocus:false,autoResize:false,inputEnabled:false,debug:false,announceOutput:true});
  expect(made.writes).toEqual([new Uint8Array([0xe2,0x82]),new Uint8Array([0xac])]); expect(made.calls.slice(-3)).toEqual(["write","reset","write"]);
  made.options.onData("한글"); made.options.onBinary(new Uint8Array([255,0])); expect(input.mock.calls).toEqual([[new TextEncoder().encode("한글")],[new Uint8Array([255,0])]]);
  screen.enabled(false); made.options.onData("ignored"); made.options.onBinary(new Uint8Array([1])); expect(input).toHaveBeenCalledTimes(2);
  screen.dispose(); expect(made.calls.filter((call: string) => call === "destroy")).toHaveLength(1); expect(owned.dispose).toHaveBeenCalledTimes(1);
});
it("retires pending writes and focus before late core completion", async () => {
  const loaded = deferred<any>(), owned = {dispose:vi.fn()}; engine.load.mockReturnValue(loaded.promise);
  const screen = openTerminalScreen(document.createElement("div"),vi.fn(),vi.fn(),vi.fn()); screen.enabled(true); screen.focus(); const write=screen.write(new Uint8Array([1]));screen.dispose();await write;
  loaded.resolve(owned);await flush();expect(engine.made).toHaveLength(0);expect(owned.dispose).toHaveBeenCalledTimes(1);
});
it("destroys a supplied core and renderer if disposal interrupts renderer readiness", async () => {
  const init=deferred<void>(),owned={dispose:vi.fn()};engine.init=init.promise;engine.load.mockResolvedValue(owned);
  const screen=openTerminalScreen(document.createElement("div"),vi.fn(),vi.fn(),vi.fn());screen.enabled(true);screen.focus();await flush();screen.dispose();init.resolve();await flush();
  expect(engine.made[0].calls).toEqual(["destroy"]);expect(owned.dispose).toHaveBeenCalledTimes(1);
});
it("fails closed on missing WASM and parse failure without logging raw errors", async () => {
  const warning=vi.spyOn(console,"warn").mockImplementation(()=>{}),unavailable=vi.fn();engine.load.mockRejectedValue(new Error("private path"));
  const missing=openTerminalScreen(document.createElement("div"),vi.fn(),vi.fn(),unavailable);await missing.write(new Uint8Array([1]));expect(unavailable).toHaveBeenCalledTimes(1);missing.dispose();
  const owned={dispose:vi.fn()};engine.load.mockResolvedValue(owned);engine.error=true;const input=vi.fn(),screen=openTerminalScreen(document.createElement("div"),input,vi.fn(),unavailable);screen.enabled(true);await screen.write(new Uint8Array([1]));engine.made[0].options.onData("after failure");
  expect(input).not.toHaveBeenCalled();expect(owned.dispose).toHaveBeenCalledTimes(1);expect(unavailable).toHaveBeenCalledTimes(2);expect(warning.mock.calls).toEqual([["terminal-renderer",{stage:"unavailable"}],["terminal-renderer",{stage:"unavailable"}]]);screen.dispose();
});
it("owns paste exactly once and permits the registered tab shortcut independently", async () => {
  engine.load.mockResolvedValue({dispose:vi.fn()});const host=document.createElement("div"),input=vi.fn(),shortcut=vi.fn(()=>true);const screen=openTerminalScreen(host,input,vi.fn(),vi.fn(),shortcut);await flush();
  const paste=()=>{const event=new Event("paste",{bubbles:true,cancelable:true});Object.defineProperty(event,"clipboardData",{value:{getData:()=>"one\ntwo"}});host.dispatchEvent(event);expect(event.defaultPrevented).toBe(true);};
  paste();expect(input).not.toHaveBeenCalled();screen.enabled(true);paste();expect(input).toHaveBeenCalledTimes(1);host.dispatchEvent(new KeyboardEvent("keydown",{key:"Tab",bubbles:true,cancelable:true}));expect(shortcut).toHaveBeenCalledTimes(1);screen.dispose();host.dispatchEvent(new Event("paste",{bubbles:true,cancelable:true}));expect(input).toHaveBeenCalledTimes(1);
});
