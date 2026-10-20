// SPDX-License-Identifier: Apache-2.0
import { spawn } from "node:child_process";

export function runBuildTool(
  command,
  args,
  { cwd, signal, timeoutMs = 120_000, killDelayMs = 1000 },
) {
  if (signal?.aborted)
    return Promise.resolve({
      status: null,
      signal: null,
      stdout: "",
      stderr: "",
      aborted: true,
      timedOut: false,
    });
  return new Promise((fulfill, reject) => {
    const grouped = process.platform !== "win32";
    const child = spawn(command, args, {
      cwd,
      detached: grouped,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stdout = "";
    let stderr = "";
    let aborted = false;
    let timedOut = false;
    let killTimer;
    let stopping = false;
    function killGroup(kind) {
      if (!child.pid) return;
      try {
        if (grouped) process.kill(-child.pid, kind);
        else child.kill(kind);
      } catch (error) {
        if (error.code !== "ESRCH") reject(error);
      }
    }
    function terminate() {
      if (stopping) return;
      stopping = true;
      killGroup("SIGTERM");
      // Descendants may ignore SIGTERM or retain the captured pipes after the
      // direct child exits. Keep escalation until the whole tool closes them.
      killTimer = setTimeout(() => killGroup("SIGKILL"), killDelayMs);
    }
    const abort = () => {
      aborted = true;
      terminate();
    };
    const timer = setTimeout(() => {
      timedOut = true;
      terminate();
    }, timeoutMs);
    function cleanup() {
      clearTimeout(timer);
      clearTimeout(killTimer);
      signal?.removeEventListener("abort", abort);
    }
    child.stdout.on("data", (data) => {
      stdout += data;
    });
    child.stderr.on("data", (data) => {
      stderr += data;
    });
    child.once("error", (error) => {
      cleanup();
      reject(error);
    });
    child.once("close", (status, exitSignal) => {
      if (stopping) killGroup("SIGKILL");
      cleanup();
      fulfill({
        status,
        signal: exitSignal,
        stdout,
        stderr,
        aborted,
        timedOut,
      });
    });
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) abort();
  });
}
