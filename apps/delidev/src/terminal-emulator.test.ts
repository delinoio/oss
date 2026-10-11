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
