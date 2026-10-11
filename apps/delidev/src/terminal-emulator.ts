// SPDX-License-Identifier: Apache-2.0
import { WTerm } from "@wterm/dom";
import { GhosttyCore } from "@wterm/ghostty";
import ghosttyWasm from "@wterm/ghostty/ghostty-vt.wasm?url";
import { copy, i18n } from "./localization";
import "@wterm/dom/css";
import "./terminal-dock.css";

export interface TerminalScreen {
  write(bytes: Uint8Array, gap?: boolean): Promise<void>;
  enabled(value: boolean): void;
  focus(): void;
  dispose(): void;
}
const palette = [0x222a33, 0xef8181, 0x8bd49c, 0xe8c77a, 0x80b7ef, 0xc4a0e8, 0x7fd0d0, 0xe5e9f0, 0x778390, 0xffa0a0, 0xa9e9b5, 0xffe1a2, 0xacd2ff, 0xe2baff, 0xa5eeee, 0xffffff];
/** The native process and original input/resize queues belong to the caller. */
export function openTerminalScreen(host: HTMLElement, input: (bytes: Uint8Array) => void, resized: (rows: number, columns: number) => void, unavailable: () => void, tabShortcut?: (event: KeyboardEvent) => boolean): TerminalScreen {
  let alive = true, writable = false, failed = false, focusPending = false, initialized = false;
  let terminal: WTerm | undefined, core: GhosttyCore | undefined;
  let dimensions = "", writes = Promise.resolve();
  let resolveDisposed!: () => void;
  const disposed = new Promise<void>(resolve => { resolveDisposed = resolve; });
  const encoder = new TextEncoder();
  const observer = new ResizeObserver(measure);
  const strings = () => ({ terminal: copy("terminal-emulator.input"), exitHint: copy("terminal-emulator.exitHint"), output: copy("terminal-emulator.output"), limit: copy("terminal-emulator.announcementLimit") });
  const localize = () => { if (alive) terminal?.setAccessibilityStrings(strings()); };
  const release = () => {
    // WTerm owns DOM observers/listeners; its supplied Ghostty core is separately owned.
    const rendered = terminal, owned = core; terminal = undefined; core = undefined;
    try { rendered?.destroy(); }
    finally { owned?.dispose(); }
  };
  const fail = (stage: "initialize" | "write" | "render" | "resize") => {
    if (!alive || failed) return;
    failed = true; writable = false; focusPending = false;
    terminal?.setInputEnabled(false); observer.disconnect();
    console.warn("delidev.terminal.renderer_unavailable", { stage, classification: "renderer-unavailable" });
    release(); unavailable();
  };
  function measure() {
    if (!alive || failed || !terminal || !host.clientWidth || !host.clientHeight) return;
    try {
      terminal.fit();
      const key = `${terminal.rows}:${terminal.cols}`;
      if (key !== dimensions) { dimensions = key; resized(terminal.rows, terminal.cols); }
    } catch { fail("resize"); }
  }
  // One capture owner avoids textarea paste plus browser input double admission.
  const paste = (event: ClipboardEvent) => {
    event.preventDefault(); event.stopImmediatePropagation();
    if (alive && writable && !failed) terminal?.paste(event.clipboardData?.getData("text/plain") ?? "");
  };
  const key = (event: KeyboardEvent) => {
    if (tabShortcut?.(event)) { event.preventDefault(); event.stopImmediatePropagation(); }
  };
  host.addEventListener("paste", paste, true);
  host.addEventListener("keydown", key, true);
  i18n.on("languageChanged", localize);
  // Keep the synchronous factory boundary. Every write joins this one task.
  const ready = (async () => {
    let loaded: GhosttyCore | undefined;
    try {
      loaded = await GhosttyCore.load({ wasmPath: ghosttyWasm, scrollbackLimit: 5000, imageStorageLimit: 0 });
      if (!alive) { loaded.dispose(); return; }
      core = loaded;
      terminal = new WTerm(host, { core, autoFocus: false, autoResize: false, inputEnabled: false, debug: false, announceOutput: true,
        accessibilityStrings: strings(), onRenderError: () => fail("render"),
        onData: value => { if (alive && writable && !failed) input(encoder.encode(value)); },
        onBinary: value => { if (alive && writable && !failed) input(value); } });
      terminal.setThemeColors({ foreground: 0xe5e9f0, background: 0x14191f, cursor: 0xe5e9f0, palette });
      await terminal.init();
      if (!alive || failed) { release(); return; }
      initialized = true; terminal.setInputEnabled(writable);
      observer.observe(host); measure();
      if (alive && !failed && focusPending) { focusPending = false; terminal?.focus(); }
    } catch {
      if (core !== loaded) loaded?.dispose();
      fail("initialize");
      // Do not expose upstream messages containing asset paths or terminal content.
      throw new Error("DELIDEV_TERMINAL_RENDERER_UNAVAILABLE");
    }
  })();
  // Initialization may fail before the first observation, without an unhandled rejection.
  void ready.catch(() => {});
  return {
    write(bytes, gap) {
      const original = bytes.slice();
      const next = writes.then(async () => {
        await Promise.race([ready, disposed]);
        if (!alive) return;
        if (failed || !terminal) throw new Error("DELIDEV_TERMINAL_RENDERER_UNAVAILABLE");
        try { if (gap) terminal.reset(); terminal.write(original); }
        catch { fail("write"); throw new Error("DELIDEV_TERMINAL_RENDERER_UNAVAILABLE"); }
      });
      writes = next.catch(() => {});
      return next;
    },
    enabled(value) { writable = alive && !failed && value; terminal?.setInputEnabled(writable); },
    focus() { if (!alive || failed) return; if (initialized && terminal) terminal.focus(); else focusPending = true; },
    dispose() {
      if (!alive) return;
      alive = false; writable = false; focusPending = false; resolveDisposed();
      observer.disconnect(); host.removeEventListener("paste", paste, true); host.removeEventListener("keydown", key, true);
      i18n.off("languageChanged", localize); release();
    },
  };
}
