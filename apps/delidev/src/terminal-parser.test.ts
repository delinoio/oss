// SPDX-License-Identifier: Apache-2.0
import { readFile } from "node:fs/promises";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { GhosttyCore } from "@wterm/ghostty";
import { WTerm } from "@wterm/dom";
const encoder = new TextEncoder();
beforeEach(() => { vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} }); });
afterEach(() => { vi.unstubAllGlobals(); });
async function core() {
  const bytes = await readFile(new URL("../.terminal-assets/ghostty-vt.wasm", import.meta.url));
  vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array(bytes).buffer, { headers: { "Content-Type": "application/wasm" } })));
  return GhosttyCore.load({ wasmPath: "http://terminal.fixture/ghostty-vt.wasm", scrollbackLimit: 5000, imageStorageLimit: 0 });
}
function text(value: GhosttyCore, row: number) { return Array.from({ length: value.getCols() }, (_, col) => { const cell = value.getCell(row, col); return cell.chars ?? String.fromCodePoint(cell.char || 32); }).join("").trimEnd(); }
it("parses split UTF8/ANSI and restores the original normal screen", async () => {
  const value = await core(); value.init(80,24);
  try {
    value.writeRaw(new Uint8Array([0xe2,0x82])); value.writeRaw(new Uint8Array([0xac])); expect(text(value,0)).toBe("€");
    value.writeRaw(encoder.encode("\x1b[3")); value.writeRaw(encoder.encode("1mRED\x1b[0m\r\nSecond\x1b[1A\x1b[1GFirst")); expect(text(value,0)).toBe("First"); expect(text(value,1)).toBe("Second");
    value.writeRaw(encoder.encode("\x1b[?1049h\x1b[HAlternate")); expect(value.usingAltScreen()).toBe(true); expect(text(value,0)).toBe("Alternate");
    value.writeRaw(encoder.encode("\x1b[?1049l")); expect(value.usingAltScreen()).toBe(false); expect(text(value,0)).toBe("First");
  } finally { value.dispose(); }
});
it("coordinated gap reset clears both screens/history/parser and pending responses", async () => {
  const value = await core(), host = document.createElement("div"); document.body.append(host);
  const sends: string[] = []; const terminal = new WTerm(host,{core:value,autoResize:false,autoFocus:false,inputEnabled:false,debug:false,onData: s=>sends.push(s)});
  try {
    await terminal.init(); terminal.write(encoder.encode("old\r\n\x1b[?1049hAlternate\x1b[6n")); terminal.write(new Uint8Array([0xe2])); terminal.write(encoder.encode("\x1b[")); terminal.reset(); terminal.write(encoder.encode("suffix"));
    expect(value.usingAltScreen()).toBe(false); expect(text(value,0)).toBe("suffix"); expect(value.getScrollbackCount()).toBe(0); expect(sends).toEqual([]);
    expect(document.activeElement).not.toBe(host.querySelector("textarea"));
  } finally { terminal.destroy(); value.dispose(); host.remove(); }
});
it("bounds history to5000 and makes OSC52/OSC8 inert without raw logging", async () => {
  const value=await core(); value.init(80,24); const log=vi.spyOn(console,"log").mockImplementation(()=>{});
  try {
    value.writeRaw(encoder.encode(Array.from({length:5100},(_,i)=>`line${i}\r\n`).join(""))); expect(value.getScrollbackCount()).toBeLessThanOrEqual(5000); expect(value.getScrollbackCount()).toBeGreaterThan(4900);
    value.writeRaw(encoder.encode("\x1b]52;c;?\x07\x1b]52;c;c2VjcmV0\x07\x1b]8;;https://private.example/\x07inert\x1b]8;;\x07"));
    expect(value.getResponse()).toBeNull(); expect(log).not.toHaveBeenCalled();
    const host=document.createElement("div");document.body.append(host);const terminal=new WTerm(host,{core:value,autoResize:false,autoFocus:false,inputEnabled:false});await terminal.init();terminal.write(encoder.encode("\x1b]8;;https://private.example/\x07inert\x1b]8;;\x07")); await new Promise(requestAnimationFrame);expect(host.querySelector("a")).toBeNull();terminal.destroy();host.remove();
  }finally{value.dispose();log.mockRestore();}
});
it("gates IME, paste, mouse/focus reports and generated replies while preserving inspection", async () => {
  const value=await core(),host=document.createElement("div"),sends:string[]=[];document.body.append(host);
  const terminal=new WTerm(host,{core:value,autoResize:false,autoFocus:false,inputEnabled:false,onData:s=>sends.push(s)});
  try {
    await terminal.init();const textarea=host.querySelector("textarea")!;
    terminal.write(encoder.encode("inspect\x1b[?1004h\x1b[?1000h\x1b[6n\x1b[14t"));
    textarea.dispatchEvent(new KeyboardEvent("keydown",{key:"a",bubbles:true}));textarea.dispatchEvent(new CompositionEvent("compositionstart",{data:"한"}));textarea.dispatchEvent(new CompositionEvent("compositionend",{data:"한글"}));textarea.dispatchEvent(new FocusEvent("focus"));host.dispatchEvent(new MouseEvent("mousedown",{button:0,bubbles:true}));terminal.paste("blocked");
    expect(sends).toEqual([]);expect(text(value,0)).toBe("inspect");expect(textarea.readOnly).toBe(true);
    terminal.setInputEnabled(true);textarea.dispatchEvent(new CompositionEvent("compositionstart",{data:"한"}));textarea.dispatchEvent(new CompositionEvent("compositionend",{data:"한글"}));textarea.value="한글";textarea.dispatchEvent(new InputEvent("input",{data:"한글",inputType:"insertCompositionText"}));expect(sends).toEqual(["한글"]);
    terminal.paste("one\r\ntwo\n\x1b[201~");expect(sends.at(-1)).toBe("one\rtwo\r\x1b[201~");terminal.write(encoder.encode("\x1b[?2004h"));terminal.paste("one\n\x1b[201~");expect(sends.at(-1)).toBe("\x1b[200~one\r\x1b[201~\x1b[201~");
    terminal.setInputEnabled(false);const count=sends.length;terminal.write(encoder.encode("\x1b[6n\x1b[16t"));textarea.dispatchEvent(new CompositionEvent("compositionend",{data:"ignored"}));expect(sends).toHaveLength(count);
  } finally { terminal.destroy();value.dispose();host.remove(); }
});
it("constructs full and incremental styled rows without parsing HTML or stylesheets", async () => {
  const value=await core(),host=document.createElement("div");document.body.append(host);const terminal=new WTerm(host,{core:value,autoResize:false,autoFocus:false,inputEnabled:false});
  try {
    await terminal.init();terminal.write(encoder.encode("\x1b[31;44;4;9m<>&\x1b[0m"));await new Promise(requestAnimationFrame);expect(host.textContent).toContain("<>&");expect(host.querySelector("style,a")).toBeNull();expect(host.querySelector(".term-grid span")?.getAttribute("style")).toBeTruthy();
    terminal.write(encoder.encode("\r\x1b[32;4mchanged\x1b[0m"));await new Promise(requestAnimationFrame);expect(host.textContent).toContain("changed");expect(host.querySelector("style,a")).toBeNull();
  } finally {terminal.destroy();value.dispose();host.remove();}
});
it("rejects missing and invalid assets through the real core loader", async () => {
  vi.stubGlobal("fetch", vi.fn(async()=>new Response("missing",{status:404})));
  await expect(GhosttyCore.load({wasmPath:"http://terminal.fixture/missing.wasm",imageStorageLimit:0})).rejects.toThrow();
  vi.stubGlobal("fetch", vi.fn(async()=>new Response("not wasm",{headers:{"Content-Type":"application/wasm"}})));
  await expect(GhosttyCore.load({wasmPath:"http://terminal.fixture/invalid.wasm",imageStorageLimit:0})).rejects.toThrow();
});
it("bounds public measurement before engine and queue resize", async () => {
  const value=await core(),host=document.createElement("div");document.body.append(host);let pixels=100000;
  vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(){return {width:this===host?pixels:8,height:this===host?pixels:17,left:0,right:8,top:0,bottom:17,x:0,y:0,toJSON:()=>({})};});
  const terminal=new WTerm(host,{core:value,autoResize:false,autoFocus:false,inputEnabled:false});
  try {await terminal.init();expect(terminal.measureDimensions()).toEqual({cols:1000,rows:500});pixels=1;expect(terminal.measureDimensions()).toEqual({cols:1,rows:1});pixels=0;expect(terminal.measureDimensions()).toBeNull();}
  finally{terminal.destroy();value.dispose();host.remove();}
});
