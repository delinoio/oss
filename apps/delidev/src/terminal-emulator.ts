// SPDX-License-Identifier: Apache-2.0
import { WTerm, type TerminalLabels } from "@wterm/dom";
import { GhosttyCore } from "@wterm/ghostty";
import ghosttyWasm from "@wterm/ghostty/ghostty-vt.wasm?url";
import { copy, i18n } from "./localization";
import { urlAtTerminalPosition } from "./external-links";
import "@wterm/dom/css";
import "./terminal-dock.css";

export interface TerminalScreen {
  write(bytes: Uint8Array, gap?: boolean): Promise<void>;
  enabled(value: boolean): void;
  focus(): void;
  dispose(): void;
}

enum RendererStage {
  Load = "load",
  Initialize = "initialize",
  Parse = "parse",
  Measure = "measure",
}
const palette = [
  0x222a33, 0xef8181, 0x8bd49c, 0xe8c77a, 0x80b7ef, 0xc4a0e8, 0x7fd0d0,
  0xe5e9f0, 0x778390, 0xffa0a0, 0xa9e9b5, 0xffe1a2, 0xacd2ff, 0xe2baff,
  0xa5eeee, 0xffffff,
];
function labels(): TerminalLabels {
  return {
    terminal: copy("session-terminals.inputLabel"),
    keyboardExit: copy("session-terminals.keyboardExit"),
    output: copy("session-terminals.outputLabel"),
    announcementLimit: copy("session-terminals.announcementLimit"),
    selecting: copy("session-terminals.selecting"),
    selectionFailed: copy("session-terminals.selectionFailed"),
    selectionChanged: copy("session-terminals.selectionChanged"),
  };
}

// The synchronous facade retains the controller's attachment lifetime. Only
// this adapter waits for the emitted same-origin WASM and owns DOM listeners.
export function openTerminalScreen(
  host: HTMLElement,
  input: (bytes: Uint8Array) => void,
  resized: (rows: number, columns: number) => void,
  unavailable: () => void,
  tabShortcut?: (event: KeyboardEvent) => boolean,
  openLink?: (url: string) => void,
): TerminalScreen {
  let alive = true,
    writable = false,
    requestedFocus = false;
  let core: GhosttyCore | undefined, terminal: WTerm | undefined;
  let initialized = false;
  let stage = RendererStage.Load;
  let ordered = Promise.resolve();
  const pending = new Set<() => void>();
  const encoder = new TextEncoder();
  const observer = new ResizeObserver(measure);
  const translate = () => terminal?.setAccessibilityLabels(labels());
  const disposeResources = () => {
    observer.disconnect();
    host.removeEventListener("paste", paste, true);
    host.removeEventListener("keydown", shortcut, true);
    host.removeEventListener("click", activate, true);
    host.removeEventListener("mousemove", hover, true);
    host.removeEventListener("mouseleave", clearHover, true);
    host.removeAttribute("data-link");
    host.ownerDocument.fonts?.removeEventListener("loadingdone", measure);
    i18n.off("languageChanged", translate);
    // WTerm intentionally does not own supplied cores. Release both owners on
    // all paths, including failed init and a load that finishes after disposal.
    try {
      terminal?.destroy();
    } catch {
      console.error({
        event: "terminal-renderer-disposal",
        category: "renderer-disposal",
      });
    }
    try {
      core?.dispose();
    } catch {
      console.error({
        event: "terminal-core-disposal",
        category: "core-disposal",
      });
    }
    for (const settle of pending) settle();
  };
  const fail = (failureStage: RendererStage) => {
    if (!alive) return;
    alive = false;
    writable = false;
    console.error({
      event: "terminal-renderer-unavailable",
      stage: failureStage,
      category: "renderer-unavailable",
    });
    disposeResources();
    unavailable();
  };
  function measure() {
    if (!alive || !terminal || !host.clientWidth || !host.clientHeight) return;
    try {
      terminal.fit();
      // The patched fit/resize clamps before the core and DOM allocate a grid.
      resized(terminal.rows, terminal.cols);
    } catch {
      fail(RendererStage.Measure);
    }
  }
  function paste(event: ClipboardEvent) {
    event.preventDefault();
    event.stopImmediatePropagation();
    if (alive && writable)
      terminal?.paste(event.clipboardData?.getData("text/plain") ?? "");
  }
  function shortcut(event: KeyboardEvent) {
    if (alive && tabShortcut?.(event)) {
      event.preventDefault();
      event.stopImmediatePropagation();
    }
  }
  // Printed HTTP(S) URLs are detected from the rendered rows. This never
  // enables WTerm's OSC 8 anchors (linksEnabled stays false), never changes
  // terminal bytes or parser replies, and never consumes the pointer event.
  function urlAtPoint(event: MouseEvent): string | undefined {
    if (!openLink || !alive || !terminal) return;
    const target = event.target instanceof Element ? event.target : undefined;
    const row = target?.closest(".term-row");
    if (!row || !host.contains(row)) return;
    const document = host.ownerDocument;
    let node: Node | null | undefined,
      offset = 0;
    if (document.caretPositionFromPoint) {
      const position = document.caretPositionFromPoint(
        event.clientX,
        event.clientY,
      );
      node = position?.offsetNode;
      offset = position?.offset ?? 0;
    } else if (document.caretRangeFromPoint) {
      const range = document.caretRangeFromPoint(event.clientX, event.clientY);
      node = range?.startContainer;
      offset = range?.startOffset ?? 0;
    }
    if (!node || !row.contains(node)) return;
    let index = -1,
      before = 0;
    const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT);
    for (let text = walker.nextNode(); text; text = walker.nextNode()) {
      if (text === node) {
        index = before + offset;
        break;
      }
      before += text.textContent?.length ?? 0;
    }
    if (index < 0) return;
    // A boundary between two cells belongs to the character under the pointer.
    const covers = (position: number) => {
      const range = document.createRange();
      try {
        const [start, end] = locate(row, position);
        if (!start || !end) return true;
        range.setStart(start.node, start.offset);
        range.setEnd(end.node, end.offset);
        const box = range.getBoundingClientRect();
        return (
          box.width === 0 ||
          (event.clientX >= box.left &&
            event.clientX <= box.right &&
            event.clientY >= box.top &&
            event.clientY <= box.bottom)
        );
      } catch {
        return true;
      }
    };
    const rows = [...host.querySelectorAll(".term-row")];
    const rowIndex = rows.indexOf(row);
    const lines = rows.map((element) => element.textContent ?? "");
    for (const candidate of [index, index - 1]) {
      if (candidate < 0 || !covers(candidate)) continue;
      const url = urlAtTerminalPosition(
        lines,
        rowIndex,
        candidate,
        terminal.cols,
      );
      if (url) return url;
    }
    return undefined;
  }
  function locate(row: Element, position: number) {
    const document = host.ownerDocument;
    const walker = document.createTreeWalker(row, NodeFilter.SHOW_TEXT);
    let before = 0;
    for (let text = walker.nextNode(); text; text = walker.nextNode()) {
      const length = text.textContent?.length ?? 0;
      if (position < before + length)
        return [
          { node: text, offset: position - before },
          { node: text, offset: position - before + 1 },
        ];
      before += length;
    }
    return [];
  }
  function activate(event: MouseEvent) {
    if (
      event.button !== 0 ||
      event.shiftKey ||
      event.altKey ||
      host.ownerDocument.getSelection()?.toString()
    )
      return;
    const url = urlAtPoint(event);
    if (url) openLink?.(url);
  }
  function hover(event: MouseEvent) {
    if (urlAtPoint(event)) host.setAttribute("data-link", "");
    else host.removeAttribute("data-link");
  }
  function clearHover() {
    host.removeAttribute("data-link");
  }
  host.addEventListener("paste", paste, true);
  host.addEventListener("keydown", shortcut, true);
  if (openLink) {
    host.addEventListener("click", activate, true);
    host.addEventListener("mousemove", hover, true);
    host.addEventListener("mouseleave", clearHover, true);
  }
  i18n.on("languageChanged", translate);
  const ready = (async () => {
    try {
      core = await GhosttyCore.load({
        wasmPath: new URL(ghosttyWasm, host.ownerDocument.baseURI).href,
        scrollbackLimit: 5000,
        discardClipboard: true,
        imageStorageLimit: 0,
        foregroundColor: "#e5e9f0",
        backgroundColor: "#14191f",
      });
      if (!alive) {
        core.dispose();
        return;
      }
      stage = RendererStage.Initialize;
      terminal = new WTerm(host, {
        core,
        autoResize: false,
        autoFocus: false,
        inputEnabled: false,
        linksEnabled: false,
        debug: false,
        announceOutput: true,
        labels: labels(),
        onRenderError: () => fail(RendererStage.Parse),
        onData: (value) => {
          if (alive && writable) input(encoder.encode(value));
        },
        onBinary: (value) => {
          if (alive && writable) input(value);
        },
      });
      await terminal.init();
      if (!alive) {
        terminal.destroy();
        core.dispose();
        return;
      }
      initialized = true;
      terminal.setThemeColors({
        foreground: 0xe5e9f0,
        background: 0x14191f,
        cursor: 0xe5e9f0,
        palette,
      });
      terminal.setInputEnabled(writable);
      observer.observe(host);
      host.ownerDocument.fonts?.addEventListener("loadingdone", measure);
      measure();
      if (alive && requestedFocus) terminal.focus();
    } catch {
      fail(stage);
    }
  })();
  return {
    write(bytes, gap) {
      if (!alive) return Promise.resolve();
      // Copy admitted raw bytes before waiting: callers may reuse transport views.
      const admitted = bytes.slice();
      return new Promise<void>((resolve) => {
        const settle = () => {
          pending.delete(settle);
          resolve();
        };
        pending.add(settle);
        ordered = ordered
          .then(() => ready)
          .then(() => {
            if (!alive || !terminal) return;
            try {
              if (gap) terminal.reset();
              terminal.write(admitted);
            } catch {
              fail(RendererStage.Parse);
            }
          })
          .finally(settle);
      });
    },
    enabled(value) {
      writable = alive && value;
      terminal?.setInputEnabled(writable);
    },
    focus() {
      if (!alive) return;
      requestedFocus = true;
      if (initialized) terminal?.focus();
    },
    dispose() {
      if (!alive) return;
      alive = false;
      writable = false;
      disposeResources();
    },
  };
}
