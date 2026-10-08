// SPDX-License-Identifier: Apache-2.0
import { createRequire } from "node:module";
import { expect, it, vi } from "vitest";
import { Terminal } from "@xterm/xterm";
vi.hoisted(() => { HTMLCanvasElement.prototype.getContext = () => null; });

it("uses the same patched ESM distributed entrypoint for Node24 require and import", () => {
  const require = createRequire(import.meta.url), common = require("@xterm/xterm");
  expect(require.resolve("@xterm/xterm")).toMatch(/lib\/xterm\.mjs$/);
  expect(Object.keys(common)).toEqual(["Terminal"]);
  expect(common.Terminal).toBe(Terminal);
  const terminal = new Terminal();
  expect(() => (terminal as unknown as { _core: { _createRenderer: () => void } })._core._createRenderer()).toThrow("DELIDEV_TERMINAL_RENDERER_UNAVAILABLE");
  terminal.dispose();
});
it("parses original split UTF8, split escape sequences, cursor movement and alternate screen", async () => {
  const terminal = new Terminal({ cols: 80, rows: 24, allowProposedApi: true, scrollback: 5000 });
  terminal.parser.registerOscHandler(52,()=>true); terminal.parser.registerOscHandler(8,()=>true);
  const write = (bytes: Uint8Array) => new Promise<void>(resolve => terminal.write(bytes, resolve));
  await write(new Uint8Array([0xe2,0x82])); await write(new Uint8Array([0xac]));
  await write(new TextEncoder().encode("\x1b[3"));await write(new TextEncoder().encode("1mRED\x1b[0m\r\nSecond\x1b[1A\x1b[1GFirst"));
  expect(terminal.buffer.active.getLine(0)?.translateToString(true)).toBe("First");
  expect(terminal.buffer.active.getLine(1)?.translateToString(true)).toBe("Second");
  await write(new TextEncoder().encode("\x1b[?1049h\x1b[HAlternate"));expect(terminal.buffer.active.type).toBe("alternate");expect(terminal.buffer.active.getLine(0)?.translateToString(true)).toBe("Alternate");
  await write(new TextEncoder().encode("\x1b[?1049l"));expect(terminal.buffer.active.type).toBe("normal");
  await write(new Uint8Array([0xe2]));terminal.reset();await write(new TextEncoder().encode("suffix"));expect(terminal.buffer.active.getLine(0)?.translateToString(true)).toBe("suffix");
  terminal.dispose();
});
