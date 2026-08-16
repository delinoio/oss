import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

const [mode, statusPath, managerPid] = process.argv.slice(2);

if (mode === "manager") {
  const child = spawn(process.execPath, [fileURLToPath(import.meta.url), "server", statusPath], {
    shell: false,
    stdio: "ignore",
    windowsHide: true,
  });

  if (process.platform !== "win32") {
    // Model the managed Cargo/Tauri tree by reaping the server before the
    // manager preserves its signal exit, including under a container PID 1.
    let forwardedSignal;
    const handlers = new Map();
    for (const signal of ["SIGINT", "SIGTERM"]) {
      const handler = () => {
        if (forwardedSignal) {
          return;
        }
        forwardedSignal = signal;
        const exitWithSignal = () => {
          for (const [registeredSignal, registeredHandler] of handlers) {
            process.off(registeredSignal, registeredHandler);
          }
          process.kill(process.pid, signal);
        };
        if (child.exitCode === null && child.signalCode === null) {
          child.once("exit", exitWithSignal);
          child.kill(signal);
        } else {
          exitWithSignal();
        }
      };
      handlers.set(signal, handler);
      process.on(signal, handler);
    }
  }

  setInterval(() => {}, 1_000);
} else if (mode === "exiting-manager") {
  spawn(
    process.execPath,
    [
      fileURLToPath(import.meta.url),
      "signal-resistant-server",
      statusPath,
      String(process.pid),
    ],
    {
      shell: false,
      stdio: "ignore",
      windowsHide: true,
    },
  );
  setInterval(() => {}, 1_000);
} else if (mode === "server" || mode === "signal-resistant-server") {
  if (mode === "signal-resistant-server") {
    process.on("SIGINT", () => {});
  }
  const server = createServer();
  server.listen(0, "127.0.0.1", () => {
    const address = server.address();
    writeFileSync(
      statusPath,
      JSON.stringify({
        pid: process.pid,
        port: address.port,
        ...(managerPid ? { managerPid: Number(managerPid) } : {}),
      }),
    );
  });
} else if (mode === "signal-escalation") {
  process.on("SIGINT", () => {});
  writeFileSync(statusPath, JSON.stringify({ pid: process.pid }));
  setInterval(() => {}, 1_000);
} else {
  throw new Error(`unsupported process-tree child mode: ${mode}`);
}
