// SPDX-License-Identifier: Apache-2.0
import { WTerm } from "@wterm/dom";
import { GhosttyCore } from "@wterm/ghostty";
import ghosttyWasm from "@wterm/ghostty/ghostty-vt.wasm";
import { copy, i18n } from "./localization";
import "@wterm/dom/css";
import "./terminal-dock.css";

export interface TerminalScreen {
  write(bytes: Uint8Array, gap?: boolean): Promise<void>;
  enabled(value: boolean): void;
  focus(): void;
  dispose(): void;
}

const palette = [0x222a33, 0xef8181, 0x8bd49c, 0xe8c77a, 0x80b7ef, 0xc4a0e8, 0x7fd0d0, 0xe5e9f0,
  0x778390, 0xffa0a0, 0xa9e9b5, 0xffe1a2, 0xacd2ff, 0xe2baff, 0xa5eeee, 0xffffff];

export function openTerminalScreen(host: HTMLElement, input: (bytes: Uint8Array) => void, resized: (rows: number, columns: number) => void, unavailable: () => void, tabShortcut?: (event: KeyboardEvent) => boolean): TerminalScreen {
  const surface = document.createElement("div");
  surface.className = "delidev-terminal";
  host.append(surface);
  let alive = true, failed = false, requestedInput = false, requestedFocus = false;
  let terminal: WTerm | undefined, core: GhosttyCore | undefined;
  let stop!: () => void;
  const stopped = new Promise<void>(resolve => { stop = resolve; });
  const release = () => {
    try { terminal?.destroy(); } finally { core?.dispose(); terminal = undefined; core = undefined; }
  };
  const failure = (stage: "initialization" | "output" | "rendering" | "resize") => {
    if (!alive || failed) return;
    failed = true;
    terminal?.setInputEnabled(false);
    // Never include the exception: terminal parsers may include untrusted output.
    console.warn({ event: "delidev_terminal_renderer_unavailable", stage });
    release();
    unavailable();
  };
  const localized = () => terminal?.setAccessibility({
    label: copy("session-terminals.attribute.34ad7d49708a"),
    exitHint: copy("session-terminals.keyboardExitHint"),
    outputLabel: copy("session-terminals.outputAnnouncements"),
    limitMessage: copy("session-terminals.outputAnnouncementsPaused"),
    selectionPending: copy("session-terminals.selectionPending"),
    selectionFailed: copy("session-terminals.selectionFailed"),
    selectionChanged: copy("session-terminals.selectionChanged"),
  });
  const measure = () => {
    if (!alive || failed || !terminal || !host.clientWidth || !host.clientHeight) return;
    try {
      const dimensions = terminal.measureDimensions();
      if (!dimensions) return;
      const rows = Math.min(500, Math.max(1, dimensions.rows));
      const columns = Math.min(1000, Math.max(1, dimensions.cols));
      if (terminal.rows !== rows || terminal.cols !== columns) terminal.resize(columns, rows);
      resized(rows, columns);
    } catch { failure("resize"); }
  };
  // The capture boundary prevents a second browser/engine paste delivery. The
  // engine normalizes and frames one complete string for atomic queue admission.
  const paste = (event: ClipboardEvent) => {
    event.preventDefault(); event.stopImmediatePropagation();
    if (alive && !failed && requestedInput) terminal?.paste(event.clipboardData?.getData("text/plain") ?? "");
  };
  const shortcut = (event: KeyboardEvent) => {
    if (alive && tabShortcut?.(event)) { event.preventDefault(); event.stopImmediatePropagation(); }
  };
  host.addEventListener("paste", paste, true);
  host.addEventListener("keydown", shortcut, true);
  i18n.on("languageChanged", localized);
  const observer = new ResizeObserver(measure);
  observer.observe(host);
  const initialize = async () => {
    try {
      const loaded = await GhosttyCore.load({ wasmPath: ghosttyWasm, scrollbackLimit: 5000, imageStorageLimit: 0 });
      if (!alive) { loaded.dispose(); return; }
      core = loaded;
      const renderer = new WTerm(surface, { core, autoResize: false, autoFocus: false, inputEnabled: false,
        debug: false, announceOutput: true,
        onData: data => { if (alive && !failed && requestedInput) input(new TextEncoder().encode(data)); },
        onBinary: bytes => { if (alive && !failed && requestedInput) input(bytes); },
        onError: () => failure("rendering"),
      });
      terminal = renderer;
      renderer.setThemeColors({ foreground: 0xe5e9f0, background: 0x14191f, cursor: 0xe5e9f0, palette });
      localized();
      await renderer.init();
      if (!alive || failed) { renderer.destroy(); loaded.dispose(); return; }
      localized();
      renderer.setInputEnabled(requestedInput);
      measure();
      if (alive && !failed && requestedFocus) renderer.focus();
    } catch { failure("initialization"); }
  };
  // A disposed factory releases pending writes even if a WASM fetch is pending.
  const ready = Promise.race([initialize(), stopped]);
  let ordered = ready;
  return {
    write(bytes, gap) {
      ordered = ordered.then(() => {
        if (!alive || failed || !terminal) return;
        try { if (gap) terminal.reset(); terminal.write(bytes); }
        catch { failure("output"); }
      });
      return ordered;
    },
    enabled(value) { requestedInput = alive && !failed && value; terminal?.setInputEnabled(requestedInput); },
    focus() { if (!alive || failed) return; requestedFocus = true; terminal?.focus(); },
    dispose() {
      if (!alive) return;
      alive = false; requestedInput = false; requestedFocus = false; stop();
      observer.disconnect();
      host.removeEventListener("paste", paste, true);
      host.removeEventListener("keydown", shortcut, true);
      i18n.off("languageChanged", localized);
      release(); surface.remove();
    },
  };
}
