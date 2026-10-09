// SPDX-License-Identifier: Apache-2.0
import { spawn } from "node:child_process";
import { terminatePosixProcessGroup, terminateWindowsProcessTree } from "../../../../scripts/spawn-dev-server.mjs";

export class QaError extends Error {
  constructor(code) { super(code); this.code = code; }
}
export const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
export async function until(read, timeout = 30_000) {
  const deadline = Date.now() + timeout;
  do { const value = await read(); if (value) return value; await delay(100); } while (Date.now() < deadline);
  throw new QaError("readiness-timeout");
}

// Do not copy provider tokens or executable injection settings from the caller.
// Native secure storage still belongs to the product's exact server references.
export function childEnvironment(source = process.env) {
  const names = new Set(["PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "WINDIR", "PATHEXT", "LOCALAPPDATA", "APPDATA", "USERPROFILE", "LANG", "LC_ALL", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "CARGO_HOME", "RUSTUP_HOME", "GOPATH", "GOCACHE", "GOMODCACHE", "GOPROXY", "GOSUMDB"]);
  return Object.fromEntries(Object.entries(source).filter(([name]) => names.has(name.toUpperCase())));
}

export class Processes {
  children = new Set();
  spawn(command, args, options = {}) {
    const child = spawn(command, args, { cwd: options.cwd, env: childEnvironment(), shell: false, windowsHide: true, detached: process.platform !== "win32", stdio: ["pipe", "pipe", "pipe"] });
    const entry = { child, lines: [], output: "", failed: false };
    this.children.add(entry);
    entry.done = new Promise(resolve => {
      child.once("error", () => { entry.failed = true; resolve({ code: null, error: true }); });
      child.once("close", (code, signal) => resolve({ code, signal }));
    });
    let pending = "";
    child.stdout.on("data", chunk => {
      // Bounded private output is parsed, never forwarded to logs or artifacts.
      if (entry.output.length + chunk.length > 2 ** 20) { entry.failed = true; return; }
      entry.output += chunk.toString(); pending += chunk.toString();
      while (pending.includes("\n")) {
        const index = pending.indexOf("\n"), line = pending.slice(0, index); pending = pending.slice(index + 1);
        try { const value = JSON.parse(line); if (entry.lines.length < 32) entry.lines.push(value); } catch { /* Build tools need not emit JSON. */ }
      }
    });
    child.stderr.on("data", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(options.input);
    return entry;
  }
  async stop(entry) {
    if (!entry.child.pid) { await entry.done; return; }
    if (process.platform === "win32") {
      if (entry.child.exitCode === null && entry.child.signalCode === null) await terminateWindowsProcessTree(entry.child, "SIGTERM");
    } else await terminatePosixProcessGroup(entry.child, "SIGTERM");
    await entry.done;
    this.children.delete(entry);
  }
  async run(command, args, options = {}) {
    const entry = this.spawn(command, args, options);
    let timer;
    try {
      const result = await Promise.race([entry.done, new Promise((_, reject) => { timer = setTimeout(() => reject(new QaError("command-timeout")), options.timeout ?? 30_000); })]);
      if (result.code !== 0 || entry.failed) throw new QaError("command-failed");
      if (!options.json) return entry.output;
      const envelope = JSON.parse(entry.output);
      if (envelope.version !== 1 || !envelope.result) throw new QaError("command-response-invalid");
      return envelope.result;
    } finally { clearTimeout(timer); await this.stop(entry); }
  }
  async close() {
    const results = await Promise.allSettled([...this.children].map(entry => this.stop(entry)));
    const failures = results.filter(result => result.status === "rejected");
    for (const failure of failures) {
      // Keep the cleanup proof uncertain, but retain a closed diagnostic cause.
      // Never print child output, commands, paths or caller configuration.
      const codes = new Set(["ENOENT", "ESRCH", "EACCES", "EPERM"]);
      console.error(JSON.stringify({ event: "delidev.qa.process_cleanup_failed", phase: "joined-exit", classification: codes.has(failure.reason?.code) ? failure.reason.code : "unconfirmed" }));
    }
    if (failures.length) throw new QaError("process-exit-unconfirmed");
  }
}
