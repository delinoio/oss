// SPDX-License-Identifier: Apache-2.0
import { WTerm } from "@wterm/dom";
import { GhosttyCore } from "@wterm/ghostty";
import { copy, i18n } from "./localization";
import "@wterm/dom/css";
import "./terminal-dock.css";

export interface TerminalScreen {
  write(bytes: Uint8Array, gap?: boolean): Promise<void>;
  enabled(value: boolean): void;
  focus(): void;
  dispose(): void;
}
export function openTerminalScreen(host: HTMLElement, input: (bytes: Uint8Array) => void, resized: (rows: number, columns: number) => void, unavailable: () => void, tabShortcut?: (event: KeyboardEvent) => boolean): TerminalScreen {
  let disposed = false, failed = false, writable = false, focusPending = false;
  let terminal: WTerm | undefined, core: GhosttyCore | undefined;
  let cancel!: () => void;
  const cancelled = new Promise<void>(resolve => { cancel = resolve; });
  const localize = () => {
    host.setAttribute("data-terminal-keyboard-hint", copy("terminal.keyboardExitHint"));
    host.setAttribute("data-terminal-output-label", copy("terminal.outputAnnouncement"));
    host.setAttribute("data-terminal-output-limit", copy("terminal.outputAnnouncementLimit"));
    terminal?.localize();
  };
  const measure = () => {
    if (disposed || failed || !terminal) return;
    try {
      const size = terminal.measureDimensions();
      if (!size) return;
      if (size.rows !== terminal.rows || size.cols !== terminal.cols) terminal.resize(size.cols, size.rows);
      resized(size.rows, size.cols);
    } catch { fail(); }
  };
  const paste = (event: ClipboardEvent) => {
    event.preventDefault(); event.stopImmediatePropagation();
    if (!disposed && !failed && writable) terminal?.paste(event.clipboardData?.getData("text/plain") ?? "");
  };
  const shortcut = (event: KeyboardEvent) => { if (!disposed && tabShortcut?.(event)) { event.preventDefault(); event.stopImmediatePropagation(); } };
  const observer = new ResizeObserver(measure);
  const cleanup = () => {
    observer.disconnect(); host.removeEventListener("paste", paste, true); host.removeEventListener("keydown", shortcut, true); i18n.off("languageChanged", localize);
    const renderer = terminal; terminal = undefined;
    const ownedCore = core; core = undefined;
    try { renderer?.destroy(); } catch { /* Keep the independent core disposal owned. */ }
    try { ownedCore?.dispose(); } catch { /* Renderer remains unavailable. */ }
  };
  const fail = () => {
    if (disposed || failed) return;
    failed = true; writable = false; focusPending = false; cancel();
    cleanup(); console.warn("terminal-renderer", { stage: "unavailable" }); unavailable();
  };
  localize(); i18n.on("languageChanged", localize);
  host.addEventListener("paste", paste, true); host.addEventListener("keydown", shortcut, true);
  const ready = (async () => {
    // CI explicitly regenerates this asset from pinned source; the bundler emits
    // a hashed same-origin URL. Never use a CDN or a runtime compiler.
    const loaded = await GhosttyCore.load({ wasmPath: new URL("../.terminal-assets/ghostty-vt.wasm", import.meta.url).href, scrollbackLimit: 5000, imageStorageLimit: 0 });
    if (disposed || failed) { loaded.dispose(); return; }
    core = loaded;
    terminal = new WTerm(host, { core, autoResize: false, autoFocus: false, inputEnabled: false, announceOutput: true, debug: false,
      onData: value => { if (!disposed && !failed && writable) input(new TextEncoder().encode(value)); },
      onBinary: bytes => { if (!disposed && !failed && writable) input(bytes); }, onError: fail,
    });
    await terminal.init();
    if (disposed || failed) { cleanup(); return; }
    terminal.setInputEnabled(writable); observer.observe(host); measure();
    if (focusPending && writable) { focusPending = false; terminal.focus(); }
  })().catch(() => { fail(); });
  let writes = Promise.resolve();
  return {
    write(bytes, gap) {
      const output = bytes.slice();
      writes = writes.then(async () => {
        await Promise.race([ready, cancelled]);
        if (disposed || failed || !terminal) return;
        try { if (gap) terminal.reset(); terminal.write(output); } catch { fail(); }
      });
      return writes;
    },
    enabled(value) { writable = !disposed && !failed && value; if (!writable) focusPending = false; terminal?.setInputEnabled(writable); },
    focus() { if (disposed || failed) return; if (terminal && writable) terminal.focus(); else focusPending = true; },
    dispose() { if (disposed) return; disposed = true; writable = false; focusPending = false; cancel(); cleanup(); },
  };
}
