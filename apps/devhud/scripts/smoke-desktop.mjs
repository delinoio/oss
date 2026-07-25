import { execFileSync, spawn } from "node:child_process";
import { homedir } from "node:os";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";

import { runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const supportedHosts = new Set(["darwin", "linux", "win32"]);
const applicationId = "dev.deli.devhud";

if (!supportedHosts.has(process.platform)) {
  console.log(
    JSON.stringify({
      check: "devhud-desktop-smoke",
      status: "skipped",
      reason: `unsupported-host-${process.platform}`,
    }),
  );
  process.exit(0);
}

if (
  process.platform === "linux" &&
  !process.env.DISPLAY
) {
  console.log(
    JSON.stringify({
      check: "devhud-desktop-smoke",
      status: "skipped",
      reason: "headless-linux-host-without-x11",
      compileValidation: "run pnpm build:desktop on this host",
    }),
  );
  process.exit(0);
}

let binaryPath;

if (process.platform === "darwin") {
  // CEF resolves its macOS framework relative to the .app bundle, so the raw
  // target/debug binary cannot represent a valid desktop startup.
  await runPackageManager(["run", "build"], { cwd: appRoot });
  await runPackageManager(
    [
      "exec",
      "tauri",
      "build",
      "--debug",
      "--bundles",
      "app",
      "--features",
      "desktop-cef",
      "--config",
      '{"bundle":{"active":true}}',
      "--no-sign",
    ],
    { cwd: appRoot },
  );
  binaryPath = resolve(
    repositoryRoot,
    "target",
    "debug",
    "bundle",
    "macos",
    "DevHud.app",
    "Contents",
    "MacOS",
    "devhud",
  );
} else {
  await runPackageManager(["run", "build:desktop"], { cwd: appRoot });
  binaryPath = resolve(
    repositoryRoot,
    "target",
    "debug",
    process.platform === "win32" ? "devhud.exe" : "devhud",
  );
}
function processTable() {
  if (process.platform === "win32") {
    const script =
      "Get-CimInstance Win32_Process | Select-Object ProcessId,ParentProcessId,Name,CommandLine | ConvertTo-Json -Compress";
    const raw = execFileSync(
      "powershell.exe",
      ["-NoProfile", "-NonInteractive", "-Command", script],
      { encoding: "utf8" },
    );
    const rows = JSON.parse(raw);
    return (Array.isArray(rows) ? rows : [rows]).map((row) => ({
      pid: Number(row.ProcessId),
      parentPid: Number(row.ParentProcessId),
      command: `${row.Name ?? ""} ${row.CommandLine ?? ""}`,
    }));
  }
  const raw = execFileSync(
    "ps",
    ["-axo", "pid=,ppid=,comm=,args="],
    { encoding: "utf8" },
  );
  return raw
    .trim()
    .split("\n")
    .map((line) => {
      const match = line.trim().match(/^(\d+)\s+(\d+)\s+(\S+)\s*(.*)$/u);
      return match === null
        ? null
        : {
            pid: Number(match[1]),
            parentPid: Number(match[2]),
            command: `${match[3]} ${match[4]}`,
          };
    })
    .filter((row) => row !== null);
}

function descendantHelpers(rootPid) {
  const table = processTable();
  const descendants = new Set([rootPid]);
  let changed = true;
  while (changed) {
    changed = false;
    for (const row of table) {
      if (descendants.has(row.parentPid) && !descendants.has(row.pid)) {
        descendants.add(row.pid);
        changed = true;
      }
    }
  }
  return table.filter(
    (row) =>
      row.pid !== rootPid &&
      descendants.has(row.pid) &&
      (/--type=/u.test(row.command) || /helper/iu.test(row.command)),
  );
}

function delay(milliseconds) {
  return new Promise((resolveDelay) => {
    setTimeout(resolveDelay, milliseconds);
  });
}

function localLogDirectory() {
  if (process.platform === "darwin") {
    return join(homedir(), "Library", "Logs", applicationId);
  }
  if (process.platform === "win32") {
    const localAppData = process.env.LOCALAPPDATA;
    if (localAppData === undefined) return null;
    return join(localAppData, applicationId, "logs");
  }
  const dataDirectory =
    process.env.XDG_DATA_HOME ?? join(homedir(), ".local", "share");
  return join(dataDirectory, applicationId, "logs");
}

function managedLogFiles() {
  const directory = localLogDirectory();
  if (directory === null || !existsSync(directory)) return new Map();
  return new Map(
    readdirSync(directory, { withFileTypes: true })
      .filter(
        (entry) =>
          entry.isFile() &&
          entry.name.startsWith("devhud-") &&
          entry.name.endsWith(".jsonl"),
      )
      .map((entry) => [entry.name, join(directory, entry.name)]),
  );
}

function newLogsContainReadyEvent(previousLogs) {
  for (const [name, path] of managedLogFiles()) {
    if (
      !previousLogs.has(name) &&
      readFileSync(path, "utf8").includes("devhud.runtime.ready")
    ) {
      return true;
    }
  }
  return false;
}

async function runSmokeIteration(iteration) {
  const output = [];
  const observedHelperPids = new Set();
  const previousLogs = managedLogFiles();
  const child = spawn(binaryPath, [], {
    cwd: appRoot,
    env: {
      ...process.env,
      DEVHUD_SMOKE: "1",
    },
    stdio: ["ignore", "pipe", "pipe"],
  });

  for (const stream of [child.stdout, child.stderr]) {
    stream.setEncoding("utf8");
    stream.on("data", (chunk) => {
      output.push(chunk);
      process.stderr.write(chunk);
    });
  }

  const observeHelpers = setInterval(() => {
    try {
      for (const helper of descendantHelpers(child.pid)) {
        observedHelperPids.add(helper.pid);
      }
    } catch {
      // The final lifecycle assertion below reports missing observations.
    }
  }, 50);
  const timeout = setTimeout(() => {
    child.kill();
  }, 2 * 60 * 1000);
  const exitCode = await new Promise((resolveExit, reject) => {
    child.once("error", reject);
    child.once("close", (code) => resolveExit(code));
  });
  clearInterval(observeHelpers);
  clearTimeout(timeout);

  const combinedOutput = output.join("");
  const observedReady =
    combinedOutput.includes("devhud.runtime.ready") ||
    newLogsContainReadyEvent(previousLogs);
  if (exitCode !== 0 || !observedReady) {
    throw new Error(
      `desktop smoke ${iteration} did not observe the ready runtime (exit ${exitCode ?? "signal"})`,
    );
  }
  if (observedHelperPids.size === 0) {
    throw new Error(
      `desktop smoke ${iteration} did not observe a CEF helper before shutdown`,
    );
  }

  await delay(250);
  const remaining = new Set(processTable().map((row) => row.pid));
  const orphaned = [...observedHelperPids].filter((pid) => remaining.has(pid));
  if (orphaned.length > 0) {
    throw new Error(
      `desktop smoke ${iteration} observed orphaned CEF helper processes`,
    );
  }
}

for (let iteration = 1; iteration <= 3; iteration += 1) {
  await runSmokeIteration(iteration);
}

console.log(
  JSON.stringify({
    check: "devhud-desktop-smoke",
    status: "passed",
    startupShutdownIterations: 3,
    helperLifecycle: "observed-before-zero-after",
  }),
);
