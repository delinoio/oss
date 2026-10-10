// SPDX-License-Identifier: Apache-2.0
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import { afterEach, expect, it, vi } from "vitest";
import { GhosttyCore } from "@wterm/ghostty";
import { WTerm } from "@wterm/dom";

const wasm = readFileSync(createRequire(import.meta.url).resolve("@wterm/ghostty/ghostty-vt.wasm"));
const cores: GhosttyCore[] = [], terminals: WTerm[] = [];
afterEach(() => { terminals.splice(0).forEach(term => term.destroy()); cores.splice(0).forEach(core => core.dispose()); vi.unstubAllGlobals(); });
async function load() {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(wasm, { headers: { "Content-Type": "application/wasm" } })));
  const core = await GhosttyCore.load({ wasmPath: "/bundled.wasm", scrollbackLimit: 5000, imageStorageLimit: 0 });
  cores.push(core); core.init(80, 24); return core;
}
const text = (core: GhosttyCore, row = 0) => Array.from({ length: core.getCols() }, (_, col) => {
  const cell = core.getCell(row, col); return cell.chars ?? (cell.char ? String.fromCodePoint(cell.char) : " ");
}).join("").trimEnd();

it("parses split original UTF8/ANSI and resets both screens and partial sequences", async () => {
  const core = await load();
  core.writeRaw(new Uint8Array([0xe2, 0x82])); core.writeRaw(new Uint8Array([0xac]));
  expect(text(core)).toBe("€");
  core.writeString("\x1b[3"); core.writeString("1mRED\x1b[0m\r\nSecond\x1b[1A\x1b[1GFirst");
  expect(text(core)).toBe("First"); expect(text(core, 1)).toBe("Second");
  core.writeString("\x1b[?1049h\x1b[HAlternate"); expect(core.usingAltScreen()).toBe(true); expect(text(core)).toBe("Alternate");
  core.writeRaw(new Uint8Array([0xe2])); core.reset(); core.writeString("suffix");
  expect(core.usingAltScreen()).toBe(false); expect(text(core)).toBe("suffix"); expect(core.getScrollbackCount()).toBe(0);
  core.writeString("\x1b[?1049h"); expect(text(core)).toBe("");
  expect([core.getRows(), core.getCols()]).toEqual([24, 80]);
});
it("bounds retained history and discards clipboard effects without raw WASM logs", async () => {
  const log = vi.spyOn(console, "log");
  const core = await load(); core.writeString("line\r\n".repeat(5100));
  expect(core.getScrollbackCount()).toBeLessThanOrEqual(5000);
  core.writeString("\x1b]52;c;c2VjcmV0\x07");
  expect(log).not.toHaveBeenCalled();
  // The core exposes a request, but the selected adapter supplies no clipboard callback.
  const host = document.createElement("div"); document.body.append(host);
  const data = vi.fn(), binary = vi.fn(), clipboard = vi.fn();
  Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: clipboard } });
  const term = new WTerm(host, { core, autoResize: false, autoFocus: false, inputEnabled: false, debug: false, onData: data, onBinary: binary });
  terminals.push(term); await term.init();
  term.write("\x1b]52;c;c2VjcmV0\x07\x1b[6n");
  expect(clipboard).not.toHaveBeenCalled(); expect(data).not.toHaveBeenCalled();
});
it("constructs inert full/incremental colored DOM and gates paste, keyboard and IME", async () => {
  const core = await load(); const host = document.createElement("div"); document.body.append(host);
  const data = vi.fn(), binary = vi.fn();
  const term = new WTerm(host, { core, autoResize: false, autoFocus: false, inputEnabled: false, onData: data, onBinary: binary });
  terminals.push(term); await term.init();
  expect(document.activeElement).not.toBe(host.querySelector("textarea"));
  const write = async (value: string) => { term.write(value); await new Promise(resolve => requestAnimationFrame(resolve)); };
  await write("\x1b[38;2;10;20;30m\x1b[4;9mColor\x1b[0m\x1b]8;;https://example.test\x07Link\x1b]8;;\x07");
  expect(host.querySelectorAll("a")).toHaveLength(0); expect(host.textContent).toContain("ColorLink");
  expect([...host.querySelectorAll("span")].some(span => span.style.color === "rgb(10, 20, 30)")).toBe(true);
  await write("\r\x1b[32mChanged"); expect(host.textContent).toContain("Changed"); expect(host.querySelectorAll("a")).toHaveLength(0);
  term.setAccessibility({ label: "터미널 출력", exitHint: "Esc 키 다음 Tab", outputLabel: "출력 알림", limitMessage: "출력이 많아 일시 중지" });
  const textarea = host.querySelector("textarea")!;
  expect(textarea.getAttribute("aria-label")).toBe("터미널 출력");
  expect(textarea.getAttribute("aria-description")).toContain("Esc 키 다음 Tab");
  term.paste("blocked"); textarea.dispatchEvent(new KeyboardEvent("keydown", { key: "a", bubbles: true }));
  textarea.dispatchEvent(new CompositionEvent("compositionend", { data: "한", bubbles: true }));
  term.write("\x1b[6n"); expect(data).not.toHaveBeenCalled(); expect(binary).not.toHaveBeenCalled();
  term.setInputEnabled(true); term.write("\x1b[?2004h"); term.paste("a\r\nb\nc");
  expect(data).toHaveBeenLastCalledWith("\x1b[200~a\rb\rc\x1b[201~");
  data.mockClear(); textarea.dispatchEvent(new CompositionEvent("compositionstart", { bubbles: true }));
  textarea.dispatchEvent(new CompositionEvent("compositionend", { data: "한", bubbles: true }));
  expect(data).toHaveBeenCalledWith("한");
  term.reset(); expect(term.getSelectionText()).toBeNull(); expect(core.getScrollbackCount()).toBe(0);
  await write("suffix"); expect(text(core)).toBe("suffix");
});

it("reports asynchronous painting failure and disables input without exposing parser errors", async () => {
  const core = await load(), host = document.createElement("div"); document.body.append(host);
  const data = vi.fn(), error = vi.fn();
  const term = new WTerm(host, { core, autoResize: false, autoFocus: false, inputEnabled: true, onData: data, onError: error });
  terminals.push(term); await term.init();
  vi.spyOn(core, "getCell").mockImplementation(() => { throw new Error("untrusted contents"); });
  term.write("paint"); await new Promise(resolve => requestAnimationFrame(resolve));
  expect(error).toHaveBeenCalledOnce(); term.paste("blocked"); expect(data).not.toHaveBeenCalled();
});
