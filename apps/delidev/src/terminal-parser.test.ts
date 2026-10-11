// SPDX-License-Identifier: Apache-2.0
// The published WASM parses only synthetic fixture bytes; this is not native acceptance.
import { createRequire } from "node:module";
import { readFile } from "node:fs/promises";
import { afterEach, expect, it, vi } from "vitest";
import { GhosttyCore } from "@wterm/ghostty";
const encode = (value: string) => new TextEncoder().encode(value);
async function load() {
  const bytes = await readFile(createRequire(import.meta.url).resolve("@wterm/ghostty/ghostty-vt.wasm"));
  vi.stubGlobal("fetch", vi.fn(async () => new Response(new Uint8Array(bytes), { headers: { "Content-Type": "application/wasm" } })));
  const core = await GhosttyCore.load({ wasmPath: "https://fixture.test/pinned-ghostty.wasm", scrollbackLimit: 5000, imageStorageLimit: 0 });
  core.init(80, 24);
  return core;
}
function line(core: GhosttyCore, row: number) {
  return Array.from({ length: core.getCols() }, (_, col) => { const cell = core.getCell(row, col); return cell.width === 0 ? "" : cell.chars ?? String.fromCodePoint(cell.char || 32); }).join("").trimEnd();
}
afterEach(() => vi.unstubAllGlobals());
it("parses split UTF-8/ANSI and restores the original alternate screen", async () => {
  const core = await load();
  try {
    core.writeRaw(new Uint8Array([0xe2, 0x82])); core.writeRaw(new Uint8Array([0xac]));
    expect(line(core, 0)).toBe("€");
    core.writeRaw(encode("\x1b[3")); core.writeRaw(encode("1mRED\x1b[0m\r\nSecond\x1b[1A\x1b[1GFirst"));
    expect(line(core, 0)).toBe("First"); expect(line(core, 1)).toBe("Second");
    core.writeRaw(encode("\x1b[?1049h\x1b[HAlternate")); expect(core.usingAltScreen()).toBe(true); expect(line(core, 0)).toBe("Alternate");
    core.writeRaw(encode("\x1b[?1049l")); expect(core.usingAltScreen()).toBe(false); expect(line(core, 0)).toBe("First");
  } finally { core.dispose(); }
});
it("fresh initialization clears partial UTF-8/escape, alternate screen and retained history", async () => {
  const core = await load();
  try {
    core.writeRaw(encode("history\r\n\x1b[?1049h")); core.writeRaw(new Uint8Array([0xe2])); core.writeRaw(encode("\x1b[3"));
    core.init(80, 24); core.writeRaw(encode("suffix"));
    expect(core.usingAltScreen()).toBe(false); expect(core.getScrollbackCount()).toBe(0); expect(line(core, 0)).toBe("suffix");
    expect(core.getCols()).toBe(80); expect(core.getRows()).toBe(24);
  } finally { core.dispose(); }
});
it("bounds history and disables image storage without logging synthetic output", async () => {
  const core = await load(), log = vi.spyOn(console, "log");
  try {
    for (let index = 0; index < 5100; index++) core.writeRaw(encode(`fixture-${index}\r\n`));
    expect(core.getScrollbackCount()).toBeLessThanOrEqual(5000); expect(core.getScrollbackCount()).toBeGreaterThan(0);
    expect(line(core, 22)).toBe("fixture-5099");
    expect(core.getGraphicsState()?.images ?? []).toEqual([]);
    expect(log).not.toHaveBeenCalled();
  } finally { core.dispose(); log.mockRestore(); }
});
