import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { initDevConsoleTerminalBridge, resetDevConsoleTerminalBridgeForTests } from "./dev-console-terminal-bridge";

type ConsoleMethod = "log" | "debug" | "info" | "warn" | "error";
type ConsoleMethodFn = (...args: unknown[]) => void;

type PluginLogMocks = {
  trace: ReturnType<typeof vi.fn>;
  debug: ReturnType<typeof vi.fn>;
  info: ReturnType<typeof vi.fn>;
  warn: ReturnType<typeof vi.fn>;
  error: ReturnType<typeof vi.fn>;
};

const consoleMethods: ConsoleMethod[] = ["log", "debug", "info", "warn", "error"];
const originalConsoleMethods: Record<ConsoleMethod, ConsoleMethodFn> = {
  log: console.log.bind(console),
  debug: console.debug.bind(console),
  info: console.info.bind(console),
  warn: console.warn.bind(console),
  error: console.error.bind(console),
};

function createPluginLogMocks(): PluginLogMocks {
  return {
    trace: vi.fn(async (_message: string) => {}),
    debug: vi.fn(async (_message: string) => {}),
    info: vi.fn(async (_message: string) => {}),
    warn: vi.fn(async (_message: string) => {}),
    error: vi.fn(async (_message: string) => {}),
  };
}

function assignConsoleSpies(): Record<ConsoleMethod, ReturnType<typeof vi.fn>> {
  const spies = {
    log: vi.fn(),
    debug: vi.fn(),
    info: vi.fn(),
    warn: vi.fn(),
    error: vi.fn(),
  };

  for (const method of consoleMethods) {
    (console as unknown as Record<ConsoleMethod, ConsoleMethodFn>)[method] = spies[method] as ConsoleMethodFn;
  }

  return spies;
}

async function flushBridgeInstall(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

describe("initDevConsoleTerminalBridge", () => {
  let pluginLogMocks: PluginLogMocks;

  beforeEach(() => {
    resetDevConsoleTerminalBridgeForTests();
    vi.clearAllMocks();
    pluginLogMocks = createPluginLogMocks();
  });

  afterEach(() => {
    resetDevConsoleTerminalBridgeForTests();

    for (const method of consoleMethods) {
      (console as unknown as Record<ConsoleMethod, ConsoleMethodFn>)[method] = originalConsoleMethods[method];
    }
  });

  it("forwards console methods to matching tauri log levels", async () => {
    const spies = assignConsoleSpies();

    initDevConsoleTerminalBridge({
      isDevelopment: true,
      hasTauriRuntime: true,
      loadPluginApi: async () => pluginLogMocks,
    });
    await flushBridgeInstall();

    console.log("stream connected", { workspaceId: "ws-default" });
    console.debug("debug message");
    console.info("info message");
    console.warn("warn message");
    console.error("error message");

    expect(spies.log).toHaveBeenCalledTimes(1);
    expect(spies.debug).toHaveBeenCalledTimes(1);
    expect(spies.info).toHaveBeenCalledTimes(1);
    expect(spies.warn).toHaveBeenCalledTimes(1);
    expect(spies.error).toHaveBeenCalledTimes(1);

    expect(pluginLogMocks.trace).toHaveBeenCalledTimes(1);
    expect(pluginLogMocks.debug).toHaveBeenCalledTimes(1);
    expect(pluginLogMocks.info).toHaveBeenCalledTimes(1);
    expect(pluginLogMocks.warn).toHaveBeenCalledTimes(1);
    expect(pluginLogMocks.error).toHaveBeenCalledTimes(1);
    expect(pluginLogMocks.trace).toHaveBeenCalledWith(expect.stringContaining("\"workspaceId\":\"ws-default\""));
  });

  it("is a no-op when tauri runtime is not available", async () => {
    const spies = assignConsoleSpies();
    const loadPluginApi = vi.fn(async () => pluginLogMocks);

    initDevConsoleTerminalBridge({
      isDevelopment: true,
      hasTauriRuntime: false,
      loadPluginApi,
    });
    await flushBridgeInstall();
    console.log("plain browser mode");

    expect(spies.log).toHaveBeenCalledTimes(1);
    expect(loadPluginApi).not.toHaveBeenCalled();
    expect(pluginLogMocks.trace).not.toHaveBeenCalled();
    expect(pluginLogMocks.debug).not.toHaveBeenCalled();
    expect(pluginLogMocks.info).not.toHaveBeenCalled();
    expect(pluginLogMocks.warn).not.toHaveBeenCalled();
    expect(pluginLogMocks.error).not.toHaveBeenCalled();
  });

  it("attaches only once even when initialized repeatedly", async () => {
    const spies = assignConsoleSpies();
    const loadPluginApi = vi.fn(async () => pluginLogMocks);

    initDevConsoleTerminalBridge({
      isDevelopment: true,
      hasTauriRuntime: true,
      loadPluginApi,
    });
    initDevConsoleTerminalBridge({
      isDevelopment: true,
      hasTauriRuntime: true,
      loadPluginApi,
    });
    await flushBridgeInstall();

    console.log("single bridge");

    expect(spies.log).toHaveBeenCalledTimes(1);
    expect(loadPluginApi).toHaveBeenCalledTimes(1);
    expect(pluginLogMocks.trace).toHaveBeenCalledTimes(1);
  });
});
