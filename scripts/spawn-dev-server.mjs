import { spawn } from "node:child_process";

const terminationSignals = ["SIGINT", "SIGTERM"];

export function spawnDevServer(command, args, options) {
  const child = spawn(command, args, options);
  const signalHandlers = new Map();

  const removeSignalHandlers = () => {
    for (const [signal, handler] of signalHandlers) {
      process.off(signal, handler);
    }
  };

  for (const signal of terminationSignals) {
    const handler = () => {
      if (child.exitCode === null && child.signalCode === null) {
        child.kill(signal);
      }
    };

    signalHandlers.set(signal, handler);
    process.on(signal, handler);
  }

  return new Promise((resolve, reject) => {
    child.once("error", (error) => {
      removeSignalHandlers();
      reject(error);
    });

    child.once("exit", (code, signal) => {
      removeSignalHandlers();
      resolve({ code, signal });
    });
  });
}

export function exitLikeChild({ code, signal }) {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code ?? 0);
}
