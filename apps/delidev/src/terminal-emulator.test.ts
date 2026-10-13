// SPDX-License-Identifier: Apache-2.0
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { openTerminalScreen } from "./terminal-emulator";
import { i18n, SupportedLanguage } from "./localization";

const fake = vi.hoisted(() => ({
  load: vi.fn(),
  instances: [] as any[],
  disposed: vi.fn(),
  initialization: Promise.resolve(),
}));
vi.mock("@wterm/ghostty/ghostty-vt.wasm?url", () => ({
  default: "/static/wasm/ghostty.hash.wasm",
}));
vi.mock("@wterm/ghostty", () => ({ GhosttyCore: { load: fake.load } }));
vi.mock("@wterm/dom", () => ({
  WTerm: class {
    rows = 24;
    cols = 80;
    write = vi.fn();
    reset = vi.fn();
    fit = vi.fn();
    focus = vi.fn();
    destroy = vi.fn();
    paste = vi.fn();
    setInputEnabled = vi.fn();
    setThemeColors = vi.fn();
    setAccessibilityLabels = vi.fn();
    constructor(
      public host: HTMLElement,
      public options: any,
    ) {
      fake.instances.push(this);
    }
    init() {
      return fake.initialization;
    }
  },
}));
const screens: ReturnType<typeof openTerminalScreen>[] = [];
beforeEach(() => {
  fake.instances.length = 0;
  fake.load.mockReset();
  fake.disposed.mockReset();
  fake.initialization = Promise.resolve();
  fake.load.mockResolvedValue({ dispose: fake.disposed });
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      disconnect() {}
    },
  );
});
afterEach(() => {
  screens.splice(0).forEach((screen) => screen.dispose());
  vi.unstubAllGlobals();
});
function open() {
  const host = document.createElement("div"),
    input = vi.fn(),
    unavailable = vi.fn(),
    shortcut = vi.fn(() => false);
  document.body.append(host);
  const screen = openTerminalScreen(
    host,
    input,
    vi.fn(),
    unavailable,
    shortcut,
  );
  screens.push(screen);
  return { screen, host, input, unavailable, shortcut };
}
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

it("keeps the facade synchronous, ordered raw writes wait once, gap reset precedes suffix, and focus waits for explicit attachment", async () => {
  let loaded!: (value: unknown) => void;
  fake.load.mockReturnValue(
    new Promise((resolve) => {
      loaded = resolve;
    }),
  );
  const { screen } = open();
  const bytes = new Uint8Array([0xe2, 0x82]);
  const first = screen.write(bytes),
    second = screen.write(new Uint8Array([0xac]), true);
  bytes[0] = 0;
  screen.enabled(true);
  screen.focus();
  expect(fake.instances).toHaveLength(0);
  expect(fake.load).toHaveBeenCalledTimes(1);
  expect(fake.load.mock.calls[0][0]).toMatchObject({
    wasmPath: "http://localhost/static/wasm/ghostty.hash.wasm",
    scrollbackLimit: 5000,
    imageStorageLimit: 0,
    discardClipboard: true,
  });
  loaded({ dispose: fake.disposed });
  await Promise.all([first, second]);
  const term = fake.instances[0];
  expect(term.options).toMatchObject({
    autoResize: false,
    autoFocus: false,
    inputEnabled: false,
    linksEnabled: false,
    debug: false,
    announceOutput: true,
  });
  expect(term.focus).toHaveBeenCalledTimes(1);
  expect(term.write.mock.calls).toEqual([
    [new Uint8Array([0xe2, 0x82])],
    [new Uint8Array([0xac])],
  ]);
  expect(term.reset.mock.invocationCallOrder[0]).toBeGreaterThan(
    term.write.mock.invocationCallOrder[0],
  );
  expect(term.reset.mock.invocationCallOrder[0]).toBeLessThan(
    term.write.mock.invocationCallOrder[1],
  );
});

it("settles writes immediately on dispose and releases a late loaded core without mounting or focusing", async () => {
  let loaded!: (value: unknown) => void;
  fake.load.mockReturnValue(
    new Promise((resolve) => {
      loaded = resolve;
    }),
  );
  const { screen, unavailable } = open();
  const write = screen.write(new Uint8Array([1]));
  screen.focus();
  screen.dispose();
  await write;
  loaded({ dispose: fake.disposed });
  await tick();
  expect(fake.instances).toHaveLength(0);
  expect(fake.disposed).toHaveBeenCalledTimes(1);
  expect(unavailable).not.toHaveBeenCalled();
});
it("destroys the renderer and supplied core after late init without delivering queued input or output", async () => {
  let initialized!: () => void;
  fake.initialization = new Promise<void>((resolve) => {
    initialized = resolve;
  });
  const { screen, input } = open();
  await tick();
  screen.focus();
  expect(fake.instances[0].focus).not.toHaveBeenCalled();
  const term = fake.instances[0],
    write = screen.write(new Uint8Array([2]));
  screen.enabled(true);
  screen.dispose();
  await write;
  term.options.onData("late");
  term.options.onBinary(new Uint8Array([0xff]));
  initialized();
  await tick();
  expect(term.write).not.toHaveBeenCalled();
  expect(term.focus).not.toHaveBeenCalled();
  expect(term.destroy).toHaveBeenCalled();
  expect(fake.disposed).toHaveBeenCalled();
  expect(input).not.toHaveBeenCalled();
});
it("fails closed with safe diagnostics and existing unavailable recovery, settling admitted writes", async () => {
  const log = vi.spyOn(console, "error").mockImplementation(() => {});
  fake.load.mockRejectedValue(new Error("secret native output/path"));
  const { screen, unavailable } = open();
  await screen.write(new Uint8Array([1]));
  expect(unavailable).toHaveBeenCalledTimes(1);
  expect(JSON.stringify(log.mock.calls)).not.toContain("secret");
  expect(log).toHaveBeenCalledWith({
    event: "terminal-renderer-unavailable",
    stage: "load",
    category: "renderer-unavailable",
  });
});
it("captures paste once, fences sends while disabled, keeps binary bytes, and delegates tab shortcuts inside the adapter", async () => {
  const { screen, host, input, shortcut } = open();
  await tick();
  const term = fake.instances[0],
    paste = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(paste, "clipboardData", {
    value: { getData: () => "한글\n" },
  });
  screen.enabled(true);
  host.dispatchEvent(paste);
  expect(term.paste).toHaveBeenCalledExactlyOnceWith("한글\n");
  expect(paste.defaultPrevented).toBe(true);
  term.options.onBinary(new Uint8Array([27, 91, 77, 255]));
  expect(input).toHaveBeenLastCalledWith(new Uint8Array([27, 91, 77, 255]));
  screen.enabled(false);
  term.options.onData("blocked");
  term.options.onBinary(new Uint8Array([255]));
  host.dispatchEvent(paste);
  expect(input).toHaveBeenCalledTimes(1);
  expect(term.paste).toHaveBeenCalledTimes(1);
  shortcut.mockReturnValue(true);
  const key = new KeyboardEvent("keydown", {
    key: "2",
    ctrlKey: true,
    bubbles: true,
    cancelable: true,
  });
  host.dispatchEvent(key);
  expect(shortcut).toHaveBeenCalledWith(key);
  expect(key.defaultPrevented).toBe(true);
  await i18n.changeLanguage(SupportedLanguage.Korean);
  expect(term.setAccessibilityLabels.mock.calls.at(-1)[0].terminal).toBe(
    "터미널",
  );
});

it("fences scheduled DOM renderer failures with the existing recovery and both cleanup owners", async () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  const { screen, input, unavailable } = open();
  await tick();
  const term = fake.instances[0];
  screen.enabled(true);
  term.options.onRenderError();
  term.options.onData("late");
  await screen.write(new Uint8Array([1]));
  expect(unavailable).toHaveBeenCalledTimes(1);
  expect(term.destroy).toHaveBeenCalledTimes(1);
  expect(fake.disposed).toHaveBeenCalledTimes(1);
  expect(input).not.toHaveBeenCalled();
});

function openWithLinks() {
  const host = document.createElement("div"),
    openLink = vi.fn();
  document.body.append(host);
  const screen = openTerminalScreen(
    host,
    vi.fn(),
    vi.fn(),
    vi.fn(),
    undefined,
    openLink,
  );
  screens.push(screen);
  return { screen, host, openLink };
}
function printRow(host: HTMLElement, text: string) {
  const row = document.createElement("div");
  row.className = "term-row";
  const span = document.createElement("span");
  span.textContent = text;
  row.append(span);
  host.append(row);
  return span;
}
function pointAt(span: HTMLElement, offset: number) {
  Object.defineProperty(document, "caretPositionFromPoint", {
    configurable: true,
    value: () => ({ offsetNode: span.firstChild, offset }),
  });
}

it("activates a plain printed URL under the pointer without consuming the event or enabling OSC 8 links", async () => {
  const { host, openLink } = openWithLinks();
  await tick();
  await tick();
  expect(fake.instances[0].options.linksEnabled).toBe(false);
  const span = printRow(host, "visit https://example.com/path?q=1. now");
  pointAt(span, 12);
  const event = new MouseEvent("click", { bubbles: true, cancelable: true, button: 0 });
  span.dispatchEvent(event);
  expect(openLink).toHaveBeenCalledTimes(1);
  expect(openLink).toHaveBeenCalledWith("https://example.com/path?q=1");
  // Terminal focus, selection and mouse handling keep receiving the click.
  expect(event.defaultPrevented).toBe(false);
  pointAt(span, 2);
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  expect(openLink).toHaveBeenCalledTimes(1);
  // Hover shows the pointer affordance only over a URL.
  pointAt(span, 12);
  span.dispatchEvent(new MouseEvent("mousemove", { bubbles: true }));
  expect(host.hasAttribute("data-link")).toBe(true);
  pointAt(span, 1);
  span.dispatchEvent(new MouseEvent("mousemove", { bubbles: true }));
  expect(host.hasAttribute("data-link")).toBe(false);
});

it("does not activate during a text selection, secondary buttons or non-http text", async () => {
  const { host, openLink } = openWithLinks();
  await tick();
  await tick();
  const span = printRow(host, "ftp://example.com javascript:alert(1) file:///etc/passwd https://ok.test/x");
  pointAt(span, 4);
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  pointAt(span, 25);
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  pointAt(span, 70);
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 2 }));
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0, shiftKey: true }));
  expect(openLink).not.toHaveBeenCalled();
  const selected = vi.spyOn(document, "getSelection").mockReturnValue({
    toString: () => "https",
  } as Selection);
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  expect(openLink).not.toHaveBeenCalled();
  selected.mockRestore();
  span.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  expect(openLink).toHaveBeenCalledWith("https://ok.test/x");
});

it("joins a URL wrapped across full-width rows and removes its callbacks on disposal", async () => {
  const { host, openLink, screen } = openWithLinks();
  await tick();
  await tick();
  // The mocked terminal is 80 columns wide.
  const first = printRow(host, `${"x".repeat(5)} https://example.com/${"a".repeat(54)}`);
  printRow(host, "bcd tail");
  pointAt(first, 10);
  first.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  expect(openLink).toHaveBeenCalledWith(`https://example.com/${"a".repeat(54)}bcd`);
  screen.dispose();
  first.dispatchEvent(new MouseEvent("click", { bubbles: true, button: 0 }));
  expect(openLink).toHaveBeenCalledTimes(1);
});

it("registers no pointer listeners when the host provides no link opener", async () => {
  const { host } = open();
  await tick();
  await tick();
  const span = printRow(host, "https://example.com/");
  pointAt(span, 3);
  span.dispatchEvent(new MouseEvent("mousemove", { bubbles: true }));
  expect(host.hasAttribute("data-link")).toBe(false);
});
