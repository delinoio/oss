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
async function create(discardClipboard = true) {
  const core = await GhosttyCore.load({
    wasmPath: "http://localhost/fixture-ghostty.wasm",
    scrollbackLimit: 5000,
    imageStorageLimit: 0,
    discardClipboard,
  });
  cores.push(core);
  core.init(80, 24);
  return core;
}
function row(core: GhosttyCore, index: number) {
  return Array.from({ length: core.getCols() }, (_, col) => {
    const cell = core.getCell(index, col);
    return cell.width === 0
      ? ""
      : (cell.chars ?? (cell.char ? String.fromCodePoint(cell.char) : ""));
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
  expect(core.getScrollbackCount()).toBeGreaterThan(0);
  expect(core.getScrollbackDiscardedCount()).toBeGreaterThan(0);
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

function cells(core: GhosttyCore, index: number) {
  return Array.from({ length: core.getCols() }, (_, col) =>
    JSON.stringify(core.getCell(index, col)),
  );
}
function splitAt(text: string, offsets: number[]) {
  const parts: string[] = [];
  let start = 0;
  for (const offset of [...offsets, text.length]) {
    parts.push(text.slice(start, offset));
    start = offset;
  }
  return parts.filter((part) => part.length > 0);
}

it("lets ESC, CAN and SUB abort a discarded OSC52 string, whole or split across writes", async () => {
  const cases = [
    { name: "ESC CSI", abort: "\x1b[31m", escape: true },
    { name: "CAN", abort: "\x18", escape: false },
    { name: "SUB", abort: "\x1a", escape: false },
    { name: "ESC ESC CSI", abort: "\x1b\x1b[31m", escape: true },
  ];
  for (const { name, abort, escape } of cases) {
    for (const query of ["c;QQ==", "c;?"]) {
      const input = `\x1b]52;${query}${abort}VISIBLEFOLLOWUP`;
      const expected = await create(false);
      expected.writeRaw(encoder.encode(input));
      const splits: number[][] = [[], [input.indexOf("VISIBLE")]];
      splits.push(Array.from({ length: input.length - 1 }, (_, i) => i + 1));
      const escapeIndex = input.indexOf("\x1b", 1);
      if (escape && escapeIndex > 0)
        splits.push([escapeIndex + 1], [escapeIndex, escapeIndex + 1]);
      for (const offsets of splits) {
        const core = await create();
        const { terminal, onData } = await screen(core);
        for (const part of splitAt(input, offsets))
          terminal.write(encoder.encode(part));
        const label = `${name} ${query} ${offsets.join(",")}`;
        expect(row(core, 0), label).toBe("VISIBLEFOLLOWUP");
        expect(cells(core, 0), label).toEqual(cells(expected, 0));
        expect(core.getClipboardWrite(), label).toBeNull();
        expect(onData, label).not.toHaveBeenCalled();
      }
    }
  }
});

it("keeps no orphan ESC pending when repeated ESC bytes restart a discarded OSC52", async () => {
  const cases = [
    { name: "abort then restart", lead: "\x1b]52;c;QQ==", abort: "\x1b\x1b" },
    { name: "triple ESC", lead: "\x1b]52;c;QQ==", abort: "\x1b\x1b\x1b" },
    { name: "ground ESC ESC", lead: "ok", abort: "\x1b\x1b" },
    { name: "query abort", lead: "\x1b]52;c;?", abort: "\x1b\x1b" },
  ];
  for (const { name, lead, abort } of cases) {
    for (const [terminator, restart] of [
      ["\x07", "]52;c;c2VjcmV0"],
      ["\x1b\\", "]52;c;?"],
    ]) {
      // The letters after the terminator would be eaten as an escape final if an ESC leaked.
      const input = `${lead}${abort}${restart}${terminator}after`;
      const expected = await create(false);
      expected.writeRaw(encoder.encode(input));
      const splits: number[][] = [
        [],
        Array.from({ length: input.length - 1 }, (_, i) => i + 1),
      ];
      const first = input.indexOf("\x1b", lead.length);
      splits.push([first + 1], [first, first + 1], [first + abort.length]);
      for (const offsets of splits) {
        const core = await create();
        const { terminal, onData } = await screen(core);
        for (const part of splitAt(input, offsets))
          terminal.write(encoder.encode(part));
        const label = `${name} ${JSON.stringify(terminator)} ${offsets.join(",")}`;
        expect(row(core, 0), label).toBe(lead === "ok" ? "okafter" : "after");
        expect(cells(core, 0), label).toEqual(cells(expected, 0));
        expect(core.getClipboardWrite(), label).toBeNull();
        expect(onData, label).not.toHaveBeenCalled();
      }
    }
  }
});

it("starts a new discard after ESC aborts an OSC52 and keeps BEL, ESC backslash and C1 ST termination", async () => {
  const core = await create();
  const feed = (text: string) => {
    for (const part of splitAt(
      text,
      Array.from({ length: text.length - 1 }, (_, i) => i + 1),
    ))
      core.writeRaw(encoder.encode(part));
  };
  // ESC ] 52 restarts the discard instead of leaking the second payload.
  feed("\x1b]52;c;QQ==\x1b]52;c;c2VjcmV0\x07after");
  expect(row(core, 0)).toBe("after");
  expect(core.getClipboardWrite()).toBeNull();
  feed("\r\n\x1b]52;c;QQ==\x07bel\x1b]52;c;QQ==\x1b\\st");
  core.writeRaw(
    new Uint8Array([
      0x1b, 0x5d, 0x35, 0x32, 0x3b, 0x63, 0x3b, 0x51, 0x51, 0x3d, 0x3d, 0x9c,
    ]),
  );
  core.writeRaw(encoder.encode("c1"));
  expect(row(core, 1)).toBe("belstc1");
  // A continuation byte with the C1 value inside UTF-8 text does not end the discard.
  core.writeRaw(encoder.encode("\r\n\x1b]52;c;"));
  core.writeRaw(encoder.encode("\u00dc\u0153"));
  core.writeRaw(encoder.encode("hidden"));
  expect(row(core, 2)).toBe("");
  core.writeRaw(encoder.encode("\x07shown"));
  expect(row(core, 2)).toBe("shown");
  expect(core.getClipboardWrite()).toBeNull();
});

it("retains paste normalization/framing and Korean IME deduplication; disabled sends stop without removing copy support", async () => {
  const core = await create(),
    { terminal, input, onData } = await screen(core);
  terminal.paste("a\r\nb\nc");
  expect(onData).toHaveBeenLastCalledWith("a\rb\rc");
  terminal.write(encoder.encode("\x1b[?2004h"));
  terminal.paste("a\n\x1b[201~b");
  expect(onData).toHaveBeenLastCalledWith("\x1b[200~a\r[201~b\x1b[201~");
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
  terminal.write(
    encoder.encode("\x1b[1;3;4;9;38;2;100;120;140m<safe styled>\x1b[0m"),
  );
  await new Promise((resolve) => setTimeout(resolve, 20));
  terminal.write(encoder.encode("\rupdate"));
  await new Promise((resolve) => setTimeout(resolve, 20));
  expect(html).not.toHaveBeenCalled();
  expect(host.querySelector(".term-row")?.textContent).toContain("update");
  expect(
    Array.from(host.querySelectorAll<HTMLElement>(".term-row span")).some(
      (span) => !!span.style.color,
    ),
  ).toBeTruthy();
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

it("localizes terminal input/output and keyboard escape guidance without changing the core", async () => {
  const core = await create(),
    { terminal, host, input } = await screen(core);
  terminal.setAccessibilityLabels({
    terminal: "터미널",
    keyboardExit: "Esc를 누른 다음 Tab을 누르세요.",
    output: "터미널 출력",
    announcementLimit: "출력 알림 일시 중지",
    selecting: "텍스트 선택 중",
    selectionFailed: "선택 실패",
    selectionChanged: "터미널 변경",
  });
  expect(input.getAttribute("aria-label")).toBe("터미널");
  expect(input.getAttribute("aria-description")).toContain(
    "Esc를 누른 다음 Tab",
  );
  expect(
    host.querySelector(".term-announcements")?.getAttribute("aria-label"),
  ).toBe("터미널 출력");
  expect(terminal.bridge).toBe(core);
});

it("disables automatic focus while preserving the public explicit focus call", async () => {
  const core = await create(),
    { terminal, input } = await screen(core);
  expect(document.activeElement).not.toBe(input);
  terminal.focus();
  expect(document.activeElement).toBe(input);
  terminal.reset();
  expect(document.activeElement).toBe(input);
});

it("requires the explicitly emitted WASM URL instead of creating an unused default asset path", async () => {
  await expect(GhosttyCore.load()).rejects.toThrow(
    "An explicit emitted Ghostty WASM URL is required",
  );
});
