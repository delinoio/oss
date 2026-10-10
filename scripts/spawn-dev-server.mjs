import { spawn } from "node:child_process";
import { readFileSync, readdirSync } from "node:fs";

const terminationSignals = ["SIGINT", "SIGTERM"];
const posixProcessGroupExitTimeoutMs = 10_000;
const windowsTerminationEnvironmentNames = [
  "PATH",
  "PATHEXT",
  "SYSTEMROOT",
  "WINDIR",
];

class PosixProcessGroupExitTimeout extends Error {}

export function windowsTerminationEnvironment(source = process.env) {
  const normalizedSource = new Map(
    Object.entries(source).map(([name, value]) => [name.toUpperCase(), value]),
  );
  return Object.fromEntries(
    windowsTerminationEnvironmentNames
      .filter((name) => normalizedSource.has(name))
      .map((name) => [name, normalizedSource.get(name)]),
  );
}

export function terminateWindowsProcessTree(child, signal) {
  const taskkill = spawn(
    "taskkill.exe",
    ["/pid", String(child.pid), "/t", "/f"],
    {
      env: windowsTerminationEnvironment(),
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
  // /proc can report unknown entry types, causing Dirent enumeration to lstat
  // a process that exits before enumeration completes. Read names only so all
  // disappearing-process checks stay inside the per-entry ENOENT guard.
  for (const entry of readdirSync("/proc")) {
    if (!/^\d+$/u.test(entry)) {
      continue;
    }
    try {
      const stat = readFileSync(`/proc/${entry}/stat`, "utf8");
      const [state, , groupId] = stat.slice(stat.lastIndexOf(")") + 1).trim().split(/\s+/u);
      if (groupId === String(processGroupId) && state !== "Z" && state !== "X") {
        return true;
      }
    } catch (error) {
      // proc_single_show can return ESRCH after open succeeds if the task
      // exits before stat is read. Both errors prove this entry disappeared;
      // access failures and other unknown observations must still fail closed.
      if (error.code !== "ENOENT" && error.code !== "ESRCH") {
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
        reject(
          new PosixProcessGroupExitTimeout(
            `timed out awaiting POSIX process group ${-processGroupId}`,
          ),
        );
        return;
      }
      setTimeout(checkProcessGroup, 25);
    };

    checkProcessGroup();
  });
}

function signalPosixProcessGroup(child, processGroupId, signal) {
  try {
    process.kill(processGroupId, signal);
  } catch (error) {
    if (error.code === "ESRCH") {
      return false;
    }
    if (child.exitCode === null && child.signalCode === null) {
      child.kill(signal);
    }
    throw new Error(
      `failed to terminate POSIX process group ${child.pid}: ${error.message}`,
    );
  }
  return true;
}

export async function terminatePosixProcessGroup(child, signal) {
  const processGroupId = -child.pid;
  if (!signalPosixProcessGroup(child, processGroupId, signal)) return;
  try {
    await waitForPosixProcessGroupExit(processGroupId);
    return;
  } catch (error) {
    if (!(error instanceof PosixProcessGroupExitTimeout) || signal === "SIGKILL") {
      throw error;
    }
  }

  if (!signalPosixProcessGroup(child, processGroupId, "SIGKILL")) return;
  await waitForPosixProcessGroupExit(processGroupId);
}

export async function spawnDevServer(
  command,
  args,
  options,
  { terminateProcessTree = false, output } = {},
) {
  const managePosixProcessGroup = process.platform !== "win32" && terminateProcessTree;
  const spawnOptions = output ? { ...options, stdio: [Array.isArray(options.stdio) ? options.stdio[0] : "inherit", "pipe", "pipe"] } : options;
  const child = spawn(
    command,
    args,
    managePosixProcessGroup ? { ...spawnOptions, detached: true } : spawnOptions,
  );
  // Captured callers must consume both pipes; ordinary dev servers retain
  // their existing stdio and exit boundary.
  const drains = [];
  if (output) {
    for (const name of ["stdout", "stderr"]) {
      child[name].setEncoding("utf8");
      child[name].on("data", chunk => output[name](chunk));
      let flushed = false;
      const flush = () => { if (!flushed) { flushed = true; output[name](null); } };
      child[name].once("end", flush);
      drains.push(new Promise(resolve => child[name].once("close", () => { flush(); resolve(); })));
    }
  }
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
    if (output) {
      // Descendants can retain a pipe after the original child exits. Bound only
      // diagnostic draining; this does not signal or adopt those descendants.
      let deadline;
      const drained = await Promise.race([
        Promise.all(drains).then(() => true),
        new Promise(resolve => { deadline = setTimeout(() => resolve(false), 1000); }),
      ]);
      clearTimeout(deadline);
      if (!drained) {
        output.incomplete?.();
        child.stdout.destroy(); child.stderr.destroy();
        await Promise.all(drains);
      }
    }
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
