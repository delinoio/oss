type LogLevel = "info" | "warn" | "error";

type LogFields = Record<string, string | number | boolean | null | undefined>;

export interface DexDexLogger {
  info(event: string, fields?: LogFields): void;
  warn(event: string, fields?: LogFields): void;
  error(event: string, fields?: LogFields): void;
}

function writeLog(level: LogLevel, event: string, fields: LogFields = {}): void {
  const payload = {
    event,
    ...fields,
  };

  if (level === "error") {
    console.error("[dexdex]", payload);
    return;
  }

  if (level === "warn") {
    console.warn("[dexdex]", payload);
    return;
  }

  console.info("[dexdex]", payload);
}

export const defaultLogger: DexDexLogger = {
  info(event, fields) {
    writeLog("info", event, fields);
  },
  warn(event, fields) {
    writeLog("warn", event, fields);
  },
  error(event, fields) {
    writeLog("error", event, fields);
  },
};
