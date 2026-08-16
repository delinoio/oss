import { spawn } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";

const terminationSignals = ["SIGINT", "SIGTERM"];
const posixProcessGroupExitTimeoutMs = 10_000;

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

// A container PID 1 may not promptly reap group-killed descendants. Those
// zombies hold no resources but keep kill(-pgid, 0) successful, so Linux uses
// /proc to wait only for live group members. Remove this when every supported
// Linux host guarantees a reaping init or Node exposes a live-group query.
function linuxProcessGroupHasLiveMembers(processGroupId) {
  for (const entry of readdirSync("/proc", { withFileTypes: true })) {
    if (!entry.isDirectory() || !/^\d+$/u.test(entry.name)) {
      continue;
    }
    try {
      const stat = readFileSync(`/proc/${entry.name}/stat`, "utf8");
      const [state, , groupId] = stat.slice(stat.lastIndexOf(")") + 1).trim().split(/\s+/u);
      if (groupId === String(processGroupId) && state !== "Z" && state !== "X") {
        return true;
      }
    } catch (error) {
      if (error.code !== "ENOENT") {
        throw error;
      }
    }
  }
  return false;
}

function waitForPosixProcessGroupExit(processGroupId) {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + posixProcessGroupExitTimeoutMs;
    const checkProcessGroup = () => {
      try {
        process.kill(processGroupId, 0);
      } catch (error) {
        if (error.code === "ESRCH") {
          resolve();
          return;
        }
        // macOS can report EPERM after the group-wide signal succeeded when no
        // remaining group member can be probed by this process.
        if (error.code === "EPERM" && process.platform === "darwin") {
          resolve();
          return;
        }
        if (error.code !== "EPERM" || process.platform !== "linux") {
          reject(
            new Error(`failed to await POSIX process group ${-processGroupId}: ${error.message}`),
          );
          return;
        }
        // Linux can report EPERM while an unprobeable SUID process remains in
        // the group, so continue to the /proc live-member check below.
      }
      try {
        if (
          process.platform === "linux" &&
          !linuxProcessGroupHasLiveMembers(-processGroupId)
        ) {
          resolve();
          return;
        }
      } catch (error) {
        reject(error);
        return;
      }
      if (Date.now() >= deadline) {
        reject(new Error(`timed out awaiting POSIX process group ${-processGroupId}`));
        return;
      }
      setTimeout(checkProcessGroup, 25);
    };

    checkProcessGroup();
  });
}

export function terminatePosixProcessGroup(child, signal) {
  const processGroupId = -child.pid;
  try {
    process.kill(processGroupId, signal);
  } catch (error) {
    if (error.code === "ESRCH") {
      return Promise.resolve();
    }
    if (child.exitCode === null && child.signalCode === null) {
      child.kill(signal);
    }
    return Promise.reject(
      new Error(`failed to terminate POSIX process group ${child.pid}: ${error.message}`),
    );
  }
  return waitForPosixProcessGroupExit(processGroupId);
}

export async function spawnDevServer(
  command,
  args,
  options,
  { terminateProcessTree = false } = {},
) {
  const managePosixProcessGroup = process.platform !== "win32" && terminateProcessTree;
  const child = spawn(
    command,
    args,
    managePosixProcessGroup ? { ...options, detached: true } : options,
  );
  const signalHandlers = new Map();
  let forwardedSignal = null;
  const terminationPromises = [];

  const removeSignalHandlers = () => {
    for (const [signal, handler] of signalHandlers) {
      process.off(signal, handler);
    }
  };

  for (const signal of terminationSignals) {
    const handler = () => {
      if (
        !managePosixProcessGroup &&
        (child.exitCode !== null || child.signalCode !== null)
      ) {
        return;
      }

      forwardedSignal = signal;
      const terminationPromise =
        terminateProcessTree
          ? process.platform === "win32"
            ? terminateWindowsProcessTree(child, signal)
            : terminatePosixProcessGroup(child, signal)
          : Promise.resolve(child.kill(signal));
      terminationPromises.push(terminationPromise);
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
    await Promise.all(terminationPromises);
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
