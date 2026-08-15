import { spawn } from "node:child_process";

const terminationSignals = ["SIGINT", "SIGTERM"];

export function terminateWindowsProcessTree(child, signal) {
  const taskkill = spawn(
    "taskkill.exe",
    ["/pid", String(child.pid), "/t", "/f"],
    {
      shell: false,
      stdio: "ignore",
      windowsHide: true,
    },
  );

  return new Promise((resolve, reject) => {
    taskkill.once("error", (error) => {
      if (child.exitCode === null && child.signalCode === null) {
        child.kill(signal);
      }
      reject(new Error(`failed to terminate Windows process tree for PID ${child.pid}: ${error.message}`));
    });

    taskkill.once("close", (code, taskkillSignal) => {
      if (code === 0) {
        resolve();
        return;
      }

      if (child.exitCode === null && child.signalCode === null) {
        child.kill(signal);
      }
      const outcome = taskkillSignal ? `signal ${taskkillSignal}` : `exit code ${code}`;
      reject(new Error(`failed to terminate Windows process tree for PID ${child.pid}: taskkill ${outcome}`));
    });
  });
}

export async function spawnDevServer(
  command,
  args,
  options,
  { terminateProcessTreeOnWindows = false } = {},
) {
  const child = spawn(command, args, options);
  const signalHandlers = new Map();
  let forwardedSignal = null;
  let terminationPromise = null;

  const removeSignalHandlers = () => {
    for (const [signal, handler] of signalHandlers) {
      process.off(signal, handler);
    }
  };

  for (const signal of terminationSignals) {
    const handler = () => {
      if (
        terminationPromise ||
        child.exitCode !== null ||
        child.signalCode !== null
      ) {
        return;
      }

      forwardedSignal = signal;
      terminationPromise =
        process.platform === "win32" && terminateProcessTreeOnWindows
          ? terminateWindowsProcessTree(child, signal)
          : Promise.resolve(child.kill(signal));
      // The child may take longer to exit than the termination command takes to
      // fail. Attach a handler now and rethrow when both operations are awaited.
      void terminationPromise.catch(() => {});
    };

    signalHandlers.set(signal, handler);
    process.on(signal, handler);
  }

  const childResult = new Promise((resolve, reject) => {
    child.once("error", (error) => {
      reject(error);
    });

    child.once("exit", (code, signal) => {
      resolve({ code, signal });
    });
  });

  try {
    const result = await childResult;
    await terminationPromise;
    return forwardedSignal ? { code: null, signal: forwardedSignal } : result;
  } finally {
    removeSignalHandlers();
  }
}

export function exitLikeChild({ code, signal }) {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code ?? 0);
}
