type ConsoleMethod = "log" | "debug" | "info" | "warn" | "error";
type LogLevel = "trace" | "debug" | "info" | "warn" | "error";
type ConsoleMethodFn = (...args: unknown[]) => void;
type LogWriter = (message: string) => Promise<void>;

interface LogPluginApi {
  trace: LogWriter;
  debug: LogWriter;
  info: LogWriter;
  warn: LogWriter;
  error: LogWriter;
}

interface DevConsoleBridgeState {
  attached: boolean;
  attachPromise: Promise<void> | null;
  originalConsoleMethods: Partial<Record<ConsoleMethod, ConsoleMethodFn>>;
}

interface DevConsoleTerminalBridgeOptions {
  isDevelopment?: boolean;
  hasTauriRuntime?: boolean;
  loadPluginApi?: () => Promise<LogPluginApi | null>;
}

const BRIDGE_STATE_KEY = "__DEXDEX_DEV_CONSOLE_TERMINAL_BRIDGE_STATE__";
const CONSOLE_METHODS: ConsoleMethod[] = ["log", "debug", "info", "warn", "error"];
const METHOD_TO_LEVEL: Record<ConsoleMethod, LogLevel> = {
  log: "trace",
  debug: "debug",
  info: "info",
  warn: "warn",
  error: "error",
};

type GlobalWithBridgeState = typeof globalThis & {
  [BRIDGE_STATE_KEY]?: DevConsoleBridgeState;
};

function getBridgeState(): DevConsoleBridgeState {
  const globalWithState = globalThis as GlobalWithBridgeState;
  if (!globalWithState[BRIDGE_STATE_KEY]) {
    globalWithState[BRIDGE_STATE_KEY] = {
      attached: false,
      attachPromise: null,
      originalConsoleMethods: {},
    };
  }
  return globalWithState[BRIDGE_STATE_KEY] as DevConsoleBridgeState;
}

function isTauriRuntimeAvailable(): boolean {
  return typeof window !== "undefined" && "__TAURI__" in window;
}

function serializeError(error: Error): string {
  return error.stack ?? `${error.name}: ${error.message}`;
}

function safeJsonStringify(value: unknown): string {
  const seen = new WeakSet<object>();
  return JSON.stringify(value, (_key, current) => {
    if (typeof current === "bigint") {
      return current.toString();
    }
    if (current instanceof Error) {
      return serializeError(current);
    }
    if (typeof current === "function") {
      return `[Function ${current.name || "anonymous"}]`;
    }
    if (typeof current === "symbol") {
      return current.toString();
    }
    if (typeof current === "object" && current !== null) {
      if (seen.has(current)) {
        return "[Circular]";
      }
      seen.add(current);
    }
    return current;
  });
}

function formatConsoleArgument(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  if (value instanceof Error) {
    return serializeError(value);
  }
  if (value === null) {
    return "null";
  }
  if (typeof value === "undefined" || typeof value === "number" || typeof value === "boolean" || typeof value === "bigint") {
    return String(value);
  }
  if (typeof value === "symbol") {
    return value.toString();
  }
  try {
    return safeJsonStringify(value);
  } catch {
    return String(value);
  }
}

function formatConsoleArguments(values: unknown[]): string {
  return values.map(formatConsoleArgument).join(" ");
}

function isLogPluginApi(module: unknown): module is LogPluginApi {
  if (typeof module !== "object" || module === null) {
    return false;
  }

  const candidate = module as Partial<Record<LogLevel, unknown>>;
  return (
    typeof candidate.trace === "function" &&
    typeof candidate.debug === "function" &&
    typeof candidate.info === "function" &&
    typeof candidate.warn === "function" &&
    typeof candidate.error === "function"
  );
}

async function loadLogPluginApi(): Promise<LogPluginApi | null> {
  try {
    const module = await import("@tauri-apps/plugin-log");
    return isLogPluginApi(module) ? module : null;
  } catch {
    return null;
  }
}

function patchConsoleMethods(logPlugin: LogPluginApi, state: DevConsoleBridgeState): void {
  const consoleMethods = console as unknown as Record<ConsoleMethod, ConsoleMethodFn>;

  for (const method of CONSOLE_METHODS) {
    if (state.originalConsoleMethods[method]) {
      continue;
    }

    const original = consoleMethods[method].bind(console) as ConsoleMethodFn;
    state.originalConsoleMethods[method] = original;

    const level = METHOD_TO_LEVEL[method];
    consoleMethods[method] = (...args: unknown[]) => {
      original(...args);

      const message = formatConsoleArguments(args);
      if (message.length === 0) {
        return;
      }

      void logPlugin[level](message).catch(() => {
        // Keep the bridge failure-safe to avoid recursive logging loops.
      });
    };
  }

  state.attached = true;
}

/**
 * Workaround: WebView console logs do not reach terminal output by default in Tauri dev mode.
 * Scope: DexDex desktop development runtime only.
 * Removal: delete after DexDex adopts a built-in Tauri console-to-terminal bridge.
 */
export function initDevConsoleTerminalBridge(options: DevConsoleTerminalBridgeOptions = {}): void {
  const isDevelopment = options.isDevelopment ?? import.meta.env.DEV;
  if (!isDevelopment) {
    return;
  }

  const hasTauriRuntime = options.hasTauriRuntime ?? isTauriRuntimeAvailable();
  if (!hasTauriRuntime) {
    return;
  }

  const state = getBridgeState();
  if (state.attached || state.attachPromise) {
    return;
  }

  const loadPluginApi = options.loadPluginApi ?? loadLogPluginApi;
  state.attachPromise = loadPluginApi()
    .then((logPlugin) => {
      if (!logPlugin || state.attached) {
        return;
      }
      patchConsoleMethods(logPlugin, state);
    })
    .finally(() => {
      state.attachPromise = null;
    });
}

export function resetDevConsoleTerminalBridgeForTests(): void {
  const state = getBridgeState();
  const consoleMethods = console as unknown as Record<ConsoleMethod, ConsoleMethodFn>;

  for (const method of CONSOLE_METHODS) {
    const original = state.originalConsoleMethods[method];
    if (original) {
      consoleMethods[method] = original;
    }
  }

  state.attached = false;
  state.attachPromise = null;
  state.originalConsoleMethods = {};
}
