import { spawn } from "node:child_process";
import {
  terminatePosixProcessGroup,
  terminateWindowsProcessTree,
} from "../spawn-dev-server.mjs";

export const commandName = (name, platform = process.platform) =>
  platform === "win32" && name === "pnpm" ? "pnpm.cmd" : name;

export function safeBaseEnvironment(source = process.env) {
  const names = [
    "PATH",
    "Path",
    "HOME",
    "USER",
    "LOGNAME",
    "USERPROFILE",
    "APPDATA",
    "LOCALAPPDATA",
    "PROGRAMDATA",
    "PROGRAMFILES",
    "PROGRAMFILES(X86)",
    "SYSTEMDRIVE",
    "SYSTEMROOT",
    "WINDIR",
    "COMSPEC",
    "PATHEXT",
    "TEMP",
    "TMP",
    "TMPDIR",
    "LANG",
    "LC_ALL",
    "LC_CTYPE",
    "TERM",
    "COLORTERM",
    "NO_COLOR",
    "FORCE_COLOR",
    "DISPLAY",
    "WAYLAND_DISPLAY",
    "XDG_RUNTIME_DIR",
    "CI",
  ];
  return Object.fromEntries(
    names.filter((name) => source[name] !== undefined).map((name) => [name, source[name]]),
  );
}

export function dockerClientEnvironment(source = process.env) {
  const names = ["DOCKER_HOST", "DOCKER_CONTEXT"];
  return Object.fromEntries(
    names.filter((name) => source[name] !== undefined).map((name) => [name, source[name]]),
  );
}

export function spawnResult(command, args, options = {}) {
  const child = spawn(command, args, { shell: false, ...options });
  const completion = new Promise((resolve, reject) => {
    child.once("error", reject);
    child.once("exit", (code, signal) => resolve({ code, signal }));
  });
  return { child, completion };
}

export async function collect(command, args, options = {}) {
  const { child, completion } = spawnResult(command, args, {
    ...options,
    stdio: [options.stdin ?? "ignore", "pipe", "pipe"],
  });
  const stdout = [];
  const stderr = [];
  child.stdout.on("data", (chunk) => stdout.push(chunk));
  child.stderr.on("data", (chunk) => stderr.push(chunk));
  const result = await completion;
  return {
    ...result,
    stdout: Buffer.concat(stdout).toString("utf8"),
    stderr: Buffer.concat(stderr).toString("utf8"),
  };
}

export async function inherited(command, args, options = {}) {
  const { completion } = spawnResult(command, args, {
    ...options,
    stdio: options.stdio ?? "inherit",
  });
  return completion;
}

export async function terminateTree(child, signal = "SIGTERM") {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  if (process.platform === "win32") {
    await terminateWindowsProcessTree(child, signal);
    return;
  }
  await terminatePosixProcessGroup(child, signal);
}
