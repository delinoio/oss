// SPDX-License-Identifier: Apache-2.0
import { Terminal } from "@xterm/xterm";
import { WebglAddon } from "@xterm/addon-webgl";
import { FitAddon } from "@xterm/addon-fit";
import { binaryInput } from "./terminal-input-queue";
import "@xterm/xterm/css/xterm.css";
import "./terminal-dock.css";

export interface TerminalScreen {
  write(bytes: Uint8Array, gap?: boolean): Promise<void>;
  enabled(value: boolean): void;
  focus(): void;
  dispose(): void;
}
export function openTerminalScreen(host: HTMLElement, input: (bytes: Uint8Array) => void, resized: (rows: number, columns: number) => void, unavailable: () => void, tabShortcut?: (event: KeyboardEvent) => boolean): TerminalScreen {
  const terminal = new Terminal({ allowProposedApi: true, scrollback: 5000, fontSize: 14, fontFamily: "ui-monospace, SFMono-Regular, Consolas, monospace", screenReaderMode: true, logLevel: "off", disableStdin: true, linkHandler: { activate: () => {}, hover: () => {}, leave: () => {} }, theme: { background: "#14191f", foreground: "#e5e9f0", cursor: "#e5e9f0", black: "#222a33", red: "#ef8181", green: "#8bd49c", yellow: "#e8c77a", blue: "#80b7ef", magenta: "#c4a0e8", cyan: "#7fd0d0", white: "#e5e9f0", brightBlack: "#778390", brightRed: "#ffa0a0", brightGreen: "#a9e9b5", brightYellow: "#ffe1a2", brightBlue: "#acd2ff", brightMagenta: "#e2baff", brightCyan: "#a5eeee", brightWhite: "#ffffff" } });
  terminal.attachCustomKeyEventHandler(event => !tabShortcut?.(event));
  const webgl = new WebglAddon(), fit = new FitAddon();
  let alive = true, writable = false;
  const pending = new Set<() => void>();
  const subscriptions = [terminal.parser.registerOscHandler(52, () => true), terminal.parser.registerOscHandler(8, () => true), terminal.onData(value => { if (writable) input(new TextEncoder().encode(value)); }), terminal.onBinary(value => { if (writable) input(binaryInput(value)); }), webgl.onContextLoss(() => { writable = false; terminal.options.disableStdin = true; unavailable(); })];
  const measure = () => {
    if (!alive || !host.clientWidth || !host.clientHeight) return;
    try { const dimensions = fit.proposeDimensions(); if (!dimensions) return;
      const rows = Math.min(500, Math.max(1, dimensions.rows)), columns = Math.min(1000, Math.max(1, dimensions.cols));
      if (terminal.rows !== rows || terminal.cols !== columns) terminal.resize(columns, rows);
      resized(rows, columns);
    } catch { writable = false; terminal.options.disableStdin = true; unavailable(); }
  };
  // Intercept the browser paste once. xterm owns bracketed-paste framing and
  // the callback atomically admits or rejects the complete emitted byte string.
  const paste = (event: ClipboardEvent) => { event.preventDefault(); event.stopImmediatePropagation(); if (writable) terminal.paste(event.clipboardData?.getData("text/plain") ?? ""); };
  host.addEventListener("paste", paste, true);
  const observer = new ResizeObserver(measure);
  try { terminal.loadAddon(webgl); terminal.loadAddon(fit); terminal.open(host); observer.observe(host); measure(); }
  catch (error) { alive = false; observer.disconnect(); host.removeEventListener("paste", paste, true); terminal.dispose(); for (const subscription of subscriptions) subscription.dispose(); throw error; }
  return {
    write(bytes, gap) { return new Promise(resolve => { if (!alive) { resolve(); return; } if (gap) terminal.reset(); const settled = () => { pending.delete(settled); resolve(); }; pending.add(settled); terminal.write(bytes, settled); }); },
    enabled(value) { writable = alive && value; terminal.options.disableStdin = !writable; },
    focus() { if (alive) terminal.focus(); },
    dispose() { if (!alive) return; alive = false; writable = false; observer.disconnect(); host.removeEventListener("paste", paste, true); for (const subscription of subscriptions) subscription.dispose(); /* Dispose the terminal first: addon disposal must never install the patched DOM fallback. */ terminal.dispose(); for (const settled of pending) settled(); }
  };
}
