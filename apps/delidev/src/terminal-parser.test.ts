// SPDX-License-Identifier: Apache-2.0
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { GhosttyCore } from "@wterm/ghostty";
import { WTerm, InputHandler } from "@wterm/dom";

const encoder = new TextEncoder();
const cores: GhosttyCore[] = [],
  screens: WTerm[] = [];
beforeEach(async () => {
  const wasm = await readFile(
    fileURLToPath(import.meta.resolve("@wterm/ghostty/ghostty-vt.wasm")),
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(new Uint8Array(wasm), {
          headers: { "Content-Type": "application/wasm" },
        }),
    ),
  );
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) =>
    setTimeout(() => callback(performance.now()), 0),
  );
  vi.stubGlobal("cancelAnimationFrame", clearTimeout);
});
afterEach(() => {
  screens.splice(0).forEach((screen) => screen.destroy());
  cores.splice(0).forEach((core) => core.dispose());
  document.body.replaceChildren();
  vi.unstubAllGlobals();
});
async function create() {
  const core = await GhosttyCore.load({
    wasmPath: "http://localhost/fixture-ghostty.wasm",
    scrollbackLimit: 5000,
    imageStorageLimit: 0,
    discardClipboard: true,
  });
  cores.push(core);
  core.init(80, 24);
  return core;
}
function row(core: GhosttyCore, index: number) {
  return Array.from({ length: core.getCols() }, (_, col) => {
    const cell = core.getCell(index, col);
    return cell.chars ?? (cell.char ? String.fromCodePoint(cell.char) : "");
  })
    .join("")
    .trimEnd();
}
async function screen(core: GhosttyCore, onData = vi.fn(), onBinary = vi.fn()) {
  const host = document.createElement("div");
  document.body.append(host);
  const terminal = new WTerm(host, {
    core,
    autoFocus: false,
    autoResize: false,
    inputEnabled: true,
    linksEnabled: false,
    debug: false,
    announceOutput: true,
    onData,
    onBinary,
  });
  screens.push(terminal);
  await terminal.init();
  return {
    host,
    terminal,
    input: host.querySelector("textarea")!,
    onData,
    onBinary,
  };
}

it("parses split UTF8 and ANSI, preserves attributes, cursor motion and both screens", async () => {
  const core = await create();
  core.writeRaw(new Uint8Array([0xe2, 0x82]));
  core.writeRaw(new Uint8Array([0xac]));
  expect(row(core, 0)).toBe("€");
  core.writeRaw(encoder.encode("\x1b[3"));
  core.writeRaw(encoder.encode("1mRED\x1b[0m\r\nSecond\x1b[1A\x1b[1GFirst"));
  expect(row(core, 0)).toBe("First");
  expect(row(core, 1)).toBe("Second");
  core.writeRaw(encoder.encode("\x1b[?1049h\x1b[HAlternate"));
  expect(core.usingAltScreen()).toBe(true);
  expect(row(core, 0)).toBe("Alternate");
  core.writeRaw(encoder.encode("\x1b[?1049l"));
  expect(core.usingAltScreen()).toBe(false);
  expect(row(core, 0)).toBe("First");
});

it("bounds native scrollback and resets parser, screens, selection, queries and announcements before a suffix", async () => {
  const core = await create(),
    { terminal, host, onData } = await screen(core);
  terminal.write(
    encoder.encode(
      Array.from({ length: 5100 }, (_, i) => "line" + i + "\r\n").join(""),
    ),
  );
  expect(core.getScrollbackCount()).toBeLessThanOrEqual(5000);
  expect(core.getScrollbackCount()).toBeGreaterThan(4900);
  terminal.write(encoder.encode("\x1b[?1049h\x1b[?2004h\x1b[?1000h\x1b[14"));
  terminal.write(new Uint8Array([0xe2]));
  terminal.reset();
  terminal.write(encoder.encode("suffix"));
  expect(core.usingAltScreen()).toBe(false);
  expect(core.bracketedPaste()).toBe(false);
  expect(core.mouseTracking()).toBe(0);
  expect(core.getScrollbackCount()).toBe(0);
  expect([core.getRows(), core.getCols()]).toEqual([24, 80]);
  expect(row(core, 0)).toBe("suffix");
  expect(terminal.getSelectionText()).toBeNull();
  expect(host.querySelector(".term-announcements")?.textContent).toBe("");
  expect(onData).not.toHaveBeenCalled();
});

it("drops fragmented OSC52 writes and queries while retaining ordinary replies and inert OSC8 text", async () => {
  const core = await create(),
    { terminal, host, onData } = await screen(core);
  for (const part of [
    "\x1b]",
    "52;",
    "c;c2VjcmV0",
    "\x1b",
    "\\",
    "\x1b]52;c;?\x07",
  ])
    terminal.write(encoder.encode(part));
  expect(core.getClipboardWrite()).toBeNull();
  expect(onData).not.toHaveBeenCalled();
  terminal.write(
    encoder.encode(
      "\x1b[6n\x1b]8;;https://example.test\x1b\\visible\x1b]8;;\x1b\\",
    ),
  );
  expect(onData).toHaveBeenCalledWith("\x1b[1;1R");
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(host.querySelector("a")).toBeNull();
  expect(host.textContent).toContain("visible");
  terminal.setInputEnabled(false);
  onData.mockClear();
  terminal.write(encoder.encode("\x1b[6n\x1b[14t"));
  expect(onData).not.toHaveBeenCalled();
  terminal.setInputEnabled(true);
  terminal.write(encoder.encode("\x1b[6n"));
  expect(onData.mock.calls).toHaveLength(1);
});

it("retains paste normalization/framing and Korean IME deduplication; disabled sends stop without removing copy support", async () => {
  const core = await create(),
    { terminal, input, onData } = await screen(core);
  terminal.paste("a\r\nb\nc");
  expect(onData).toHaveBeenLastCalledWith("a\rb\rc");
  terminal.write(encoder.encode("\x1b[?2004h"));
  terminal.paste("a\n\x1b[201~b");
  expect(onData).toHaveBeenLastCalledWith("\x1b[200~a\r\x1b[201~b\x1b[201~");
  input.dispatchEvent(new CompositionEvent("compositionstart"));
  input.dispatchEvent(new CompositionEvent("compositionend", { data: "한글" }));
  input.value = "한글";
  input.dispatchEvent(
    new InputEvent("input", {
      data: "한글",
      inputType: "insertCompositionText",
    }),
  );
  expect(onData.mock.calls.filter(([value]) => value === "한글")).toHaveLength(
    1,
  );
  terminal.setInputEnabled(false);
  onData.mockClear();
  terminal.paste("blocked");
  input.dispatchEvent(
    new KeyboardEvent("keydown", { key: "x", bubbles: true, cancelable: true }),
  );
  input.dispatchEvent(new CompositionEvent("compositionend", { data: "차단" }));
  input.dispatchEvent(new FocusEvent("focus"));
  terminal.write(encoder.encode("\x1b[?1004h\x1b[6n"));
  input.dispatchEvent(new FocusEvent("blur"));
  expect(onData).not.toHaveBeenCalled();
  expect(input.readOnly).toBe(true);
  expect(await terminal.selectAll()).toBe(true);
});

it("constructs full and incremental cell DOM without HTML parsing and clamps dimensions before allocation", async () => {
  const core = await create(),
    { terminal, host } = await screen(core);
  const html = vi
    .spyOn(Element.prototype, "innerHTML", "set")
    .mockImplementation(() => {
      throw new Error("Unexpected HTML parser assignment");
    });
  terminal.write(encoder.encode("\x1b[1;3;4;9;38;2;100;120;140m<safe>\x1b[0m"));
  await new Promise((resolve) => setTimeout(resolve, 20));
  terminal.write(encoder.encode("\rupdate"));
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(html).not.toHaveBeenCalled();
  expect(host.querySelector(".term-row")?.textContent).toContain("update");
  expect(host.querySelector(".term-row span")?.style.color).toBeTruthy();
  html.mockRestore();
  terminal.resize(99999, 99999);
  expect([
    terminal.cols,
    terminal.rows,
    core.getCols(),
    core.getRows(),
  ]).toEqual([1000, 500, 1000, 500]);
  terminal.resize(0, 0);
  expect([terminal.cols, terminal.rows]).toEqual([1, 1]);
});

it("preserves binary X10 mouse reports and stops mouse capture while disabled", async () => {
  const core = await create();
  core.resize(250, 24);
  core.writeRaw(encoder.encode("\x1b[?1000h"));
  const host = document.createElement("div");
  document.body.append(host);
  vi.spyOn(host, "getBoundingClientRect").mockReturnValue({
    x: 0,
    y: 0,
    left: 0,
    top: 0,
    right: 2500,
    bottom: 480,
    width: 2500,
    height: 480,
    toJSON() {},
  });
  const sent = vi.fn(),
    binary = vi.fn();
  const input = new InputHandler(
    host,
    sent,
    () => core,
    () => ({ charWidth: 10, rowHeight: 20 }),
    undefined,
    binary,
  );
  try {
    host.dispatchEvent(
      new MouseEvent("mousedown", {
        clientX: 1500,
        clientY: 20,
        button: 0,
        buttons: 1,
        bubbles: true,
        cancelable: true,
      }),
    );
    expect(binary).toHaveBeenCalledWith(
      new Uint8Array([27, 91, 77, 32, 183, 34]),
    );
    input.setEnabled(false);
    binary.mockClear();
    host.dispatchEvent(
      new MouseEvent("mousedown", {
        clientX: 1500,
        clientY: 20,
        button: 0,
        buttons: 1,
        bubbles: true,
        cancelable: true,
      }),
    );
    document.dispatchEvent(
      new MouseEvent("mouseup", {
        clientX: 1500,
        clientY: 20,
        button: 0,
        buttons: 0,
        bubbles: true,
        cancelable: true,
      }),
    );
    expect(binary).not.toHaveBeenCalled();
    expect(sent).not.toHaveBeenCalled();
  } finally {
    input.destroy();
  }
});

it("keeps C1-valued UTF8 continuation bytes intact across clipboard filtering boundaries", async () => {
  const core = await create(),
    data = encoder.encode("ĝ52;한글");
  for (const byte of data) core.writeRaw(new Uint8Array([byte]));
  expect(row(core, 0)).toBe("ĝ52;한글");
});
