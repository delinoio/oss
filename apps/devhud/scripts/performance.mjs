/**
 * Non-gating release instrumentation. Results deliberately contain no host
 * names, device identifiers, paths, command output, environment values, or
 * diagnostic records. `unavailable` means the requested executable target was
 * not present; `failed` means an attempted measurement did not complete.
 */
import { spawn, spawnSync } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { extname, resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const outputDirectory = resolve(appRoot, "performance/results");
const revision = "f49ebda2fdba5755456b0f049e32593ca0ea331a";
const gitRevision = run("git", ["rev-parse", "HEAD"]);
const gitWorktree = run("git", ["status", "--porcelain", "--untracked-files=all"]);
const cleanWorktree = gitWorktree.status === 0 && !gitWorktree.stdout.trim();
const buildRevision = cleanWorktree && gitRevision.status === 0 && /^[0-9a-f]{40}$/u.test(gitRevision.stdout.trim()) ? gitRevision.stdout.trim() : null;
const schemaVersion = "devhud.performance.result.v1";
const application = { version: "0.1.0", tauriRevision: revision, cefRevision: `tauri-runtime-cef@${revision}`, buildRevision };
const hostPlatform = { darwin: "macos", win32: "windows", linux: "linux" }[process.platform];
const hostArchitecture = { x64: "x86_64", arm64: "arm64", arm: "armv7" }[process.arch];
const desktopNames = ["desktop-cold-startup", "desktop-warm-startup", "desktop-hud-display", "desktop-package-size", "desktop-idle-memory"];
const startupTimeoutMs = 30_000;
const buildTimeoutMs = 15 * 60_000;
const processTerminationGraceMs = 1_000;
const expectedNotes = { "desktop-cold-startup": "cold-process", "desktop-warm-startup": "warm-process", "desktop-hud-display": "explicit-hud-invocation", "desktop-package-size": "packaged-artifact", "desktop-idle-memory": "post-ready-idle", "mobile-startup": "cold-process" };

function result(targets) { return { schemaVersion, application, targets }; }
function writeResult(value, name) { mkdirSync(outputDirectory, { recursive: true }); const path = resolve(outputDirectory, name); writeFileSync(path, `${JSON.stringify(canonicalize(value), null, 2)}\n`); return path; }
function canonicalize(value) {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}
function unavailable(platform, architecture, reason, names = [], targetKind) { return { platform, architecture, ...(targetKind ? { targetKind } : {}), status: "unavailable", unavailableReason: reason, measurements: names.map((name) => ({ name, status: "unavailable", method: methodFor(name, platform, targetKind), samples: [] })) }; }
function methodFor(name, platform, targetKind) { return name === "desktop-hud-display" ? "process-hud-marker" : name === "desktop-package-size" ? "artifact-byte-count" : name === "desktop-idle-memory" ? "resident-set-sampling" : name === "mobile-startup" ? platform === "ios" ? targetKind === "ios-device" ? "devicectl-launch-wall-clock" : "simctl-launch-wall-clock" : "adb-am-start-w" : "process-ready-marker"; }
function run(command, args, timeout = 30_000, options = {}) { return spawnSync(command, args, { encoding: "utf8", timeout, ...options }); }
function requireCleanWorktree() {
  if (!cleanWorktree) throw new Error("performance evidence requires a clean Git worktree");
}
function executable(command) { return run(process.platform === "win32" ? "where" : "which", [command]).status === 0; }
function median(values) {
  const sorted = [...values].sort((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? (sorted[middle] ?? 0) : ((sorted[middle - 1] ?? 0) + (sorted[middle] ?? 0)) / 2;
}
function artifactBytes(path) { if (!path || !existsSync(path)) return null; const stat = statSync(path); if (stat.isFile()) return stat.size; return readdirSync(path, { withFileTypes: true }).reduce((total, entry) => total + (artifactBytes(resolve(path, entry.name)) ?? 0), 0); }
function artifactDigest(path) { return !path || !existsSync(path) || !statSync(path).isFile() ? null : createHash("sha256").update(readFileSync(path)).digest("hex"); }
function packageProvenancePath(path) { return `${path}.devhud-performance.json`; }
function matchesApplicationVersion(path) { return new RegExp(`(?:^|[^0-9])${application.version.replaceAll(".", "\\.")}(?:$|[^0-9])`, "u").test(path); }
function matchesHostArchitecture(name) {
  const tokens = { x86_64: ["x86_64", "x64", "amd64"], arm64: ["arm64", "aarch64"], armv7: ["armv7", "armeabi-v7a"] }[hostArchitecture] ?? [];
  return tokens.some((token) => new RegExp(`(?:^|[^a-z0-9])${token}(?:$|[^a-z0-9])`, "iu").test(name));
}
function findDesktopExecutable() {
  const target = resolve(repositoryRoot, "target", "release");
  if (process.platform === "darwin") {
    const binary = resolve(target, "devhud");
    return existsSync(binary) ? binary : null;
  }
  const binary = resolve(target, process.platform === "win32" ? "devhud.exe" : "devhud");
  return existsSync(binary) ? binary : null;
}
function findPackagedArtifact() {
  const bundle = resolve(repositoryRoot, "target", "release", "bundle");
  const candidates = process.platform === "darwin"
    ? [resolve(bundle, "dmg")]
    : process.platform === "win32"
      ? [resolve(bundle, "nsis"), resolve(bundle, "msi")]
      : [resolve(bundle, "appimage"), resolve(bundle, "deb")];
  for (const candidate of candidates) {
    if (!existsSync(candidate)) continue;
    if ((statSync(candidate).isFile() || candidate.endsWith(".app")) && matchesHostArchitecture(candidate) && matchesApplicationVersion(candidate)) return candidate;
    const artifact = readdirSync(candidate).sort().find((entry) => ([".appimage", ".deb", ".dmg", ".exe", ".msi"].includes(extname(entry).toLowerCase()) || entry.endsWith(".AppImage")) && matchesHostArchitecture(entry) && matchesApplicationVersion(entry));
    if (artifact) return resolve(candidate, artifact);
  }
  return null;
}
function packageMeasurement(artifact = findPackagedArtifact()) {
  if (!artifact) return { measurement: { name: "desktop-package-size", status: "unavailable", method: "artifact-byte-count", samples: [] }, unavailableReason: "artifact-not-found" };
  try {
    const provenance = JSON.parse(readFileSync(packageProvenancePath(artifact), "utf8"));
    if (canonicalText(provenance.application) !== canonicalText(application) || provenance.sha256 !== artifactDigest(artifact)) throw new Error("package provenance mismatch");
  } catch {
    return { measurement: { name: "desktop-package-size", status: "unavailable", method: "artifact-byte-count", samples: [] }, unavailableReason: "build-provenance-unverified" };
  }
  return { measurement: { name: "desktop-package-size", status: "available", method: "artifact-byte-count", samples: [artifactBytes(artifact)], unit: "bytes", note: "packaged-artifact" } };
}
function recordPackageProvenance(artifact = findPackagedArtifact()) {
  const sha256 = artifactDigest(artifact);
  if (!artifact || !sha256 || !matchesApplicationVersion(artifact)) throw new Error("a current host-architecture release artifact is required");
  const provenance = { application, sha256 };
  writeFileSync(packageProvenancePath(artifact), `${JSON.stringify(canonicalize(provenance), null, 2)}\n`);
  return artifact;
}
function clearPackageArtifacts() { rmSync(resolve(repositoryRoot, "target", "release", "bundle"), { recursive: true, force: true }); }
function processTreeRssBytes(pid) {
  if (process.platform === "win32") {
    const outcome = run("powershell.exe", ["-NoProfile", "-Command", "Get-CimInstance Win32_Process | ForEach-Object { $process = Get-Process -Id $_.ProcessId -ErrorAction SilentlyContinue; if ($process) { [PSCustomObject]@{ ProcessId = $_.ProcessId; ParentProcessId = $_.ParentProcessId; WorkingSet64 = $process.WorkingSet64 } } } | ConvertTo-Json -Compress"]);
    if (outcome.status !== 0 || !outcome.stdout.trim()) return null;
    const rows = JSON.parse(outcome.stdout);
    return rssForProcessTree(pid, (Array.isArray(rows) ? rows : [rows]).map((row) => ({ pid: Number(row.ProcessId), parentPid: Number(row.ParentProcessId), rss: Number(row.WorkingSet64) })));
  }
  const outcome = run("ps", ["-axo", "pid=,ppid=,rss="]);
  if (outcome.status !== 0) return null;
  const rows = outcome.stdout.trim().split(/\n/u).flatMap((line) => {
    const match = line.trim().match(/^(\d+)\s+(\d+)\s+(\d+)$/u);
    return match ? [{ pid: Number(match[1]), parentPid: Number(match[2]), rss: Number(match[3]) * 1024 }] : [];
  });
  return rssForProcessTree(pid, rows);
}
function rssForProcessTree(rootPid, rows) {
  const descendants = new Set([rootPid]);
  let found = false;
  let changed = true;
  while (changed) {
    changed = false;
    for (const row of rows) {
      if (row.pid === rootPid) found = true;
      if (descendants.has(row.parentPid) && !descendants.has(row.pid)) {
        descendants.add(row.pid);
        changed = true;
      }
    }
  }
  if (!found) return null;
  const total = rows.filter((row) => descendants.has(row.pid)).reduce((sum, row) => sum + row.rss, 0);
  return Number.isFinite(total) ? total : null;
}
function waitForClose(closed, timeoutMs) {
  let timeout;
  return Promise.race([closed, new Promise((resolveTimeout) => { timeout = setTimeout(resolveTimeout, timeoutMs); })]).finally(() => clearTimeout(timeout));
}
async function terminateDesktopProcess(child, closed) {
  if (child.exitCode !== null) return;
  child.kill();
  if (await waitForClose(closed, processTerminationGraceMs)) return;
  if (process.platform === "win32") run("taskkill", ["/pid", String(child.pid), "/t", "/f"]);
  else {
    try { process.kill(-child.pid, "SIGKILL"); }
    catch { child.kill("SIGKILL"); }
  }
  await waitForClose(closed, processTerminationGraceMs);
}
async function profileDesktop(binary, startupNote) {
  const markers = [];
  let protocolFailure = false;
  const start = performance.now();
  const child = spawn(binary, [], { cwd: appRoot, detached: process.platform !== "win32", env: { ...process.env, DEVHUD_PERF: "1" }, stdio: ["ignore", "pipe", "pipe"] });
  const closed = new Promise((resolveClosed) => child.once("close", () => resolveClosed("startup-exited")));
  const spawnFailed = new Promise((resolveFailed) => child.once("error", () => resolveFailed(true)));
  let markReady;
  const ready = new Promise((resolveReady) => { markReady = resolveReady; });
  let reportProtocolFailure;
  const protocolFailed = new Promise((resolveFailed) => { reportProtocolFailure = resolveFailed; });
  for (const stream of [child.stdout, child.stderr]) {
    let pending = "";
    stream.setEncoding("utf8");
    stream.on("data", (chunk) => {
      const lines = `${pending}${chunk}`.split(/\r?\n/u);
      pending = lines.pop();
      for (const line of lines) if (line.startsWith("DEVHUD_PERF ")) {
        try {
          const marker = { ...JSON.parse(line.slice(12)), at: performance.now() - start };
          markers.push(marker);
          if (marker.event === "ready") markReady(marker);
        } catch {
          protocolFailure = true;
          reportProtocolFailure("measurement-protocol-failed");
        }
      }
    });
  }
  let timeout;
  const timeoutReached = new Promise((resolveTimeout) => { timeout = setTimeout(() => resolveTimeout("startup-timeout"), startupTimeoutMs); });
  const readyMarker = await Promise.race([ready, protocolFailed, spawnFailed, closed, timeoutReached]);
  clearTimeout(timeout);
  if (readyMarker === true) return { failure: "launch-failed", measurements: [] };
  if (readyMarker === "startup-exited") return { failure: "startup-exited", measurements: [] };
  if (readyMarker === "startup-timeout") {
    await terminateDesktopProcess(child, closed);
    return { failure: "startup-timeout", measurements: [] };
  }
  if (readyMarker === "measurement-protocol-failed") {
    await terminateDesktopProcess(child, closed);
    return { failure: "measurement-protocol-failed", measurements: [] };
  }
  if (!readyMarker.application || canonicalText(readyMarker.application) !== canonicalText(application)) {
    await terminateDesktopProcess(child, closed);
    return { unavailableReason: "build-provenance-unverified", measurements: [] };
  }
  if (readyMarker) await new Promise((resolveDone) => setTimeout(resolveDone, 200));
  const memory = readyMarker ? [processTreeRssBytes(child.pid), processTreeRssBytes(child.pid), processTreeRssBytes(child.pid)].filter((sample) => sample !== null) : [];
  await terminateDesktopProcess(child, closed);
  if (protocolFailure) return { failure: "measurement-protocol-failed", measurements: [] };
  const readyEvent = markers.find((marker) => marker.event === "ready"); const hud = markers.find((marker) => marker.event === "hud-shown");
  if (!readyEvent || !hud || !Number.isFinite(hud.durationMs) || hud.durationMs < 0 || !memory.length) return { failure: "measurement-protocol-failed", measurements: [] };
  return { measurements: [
    { name: startupNote === "cold-process" ? "desktop-cold-startup" : "desktop-warm-startup", status: "available", method: "process-ready-marker", samples: [Math.round(readyEvent.at)], unit: "milliseconds", note: startupNote },
    { name: "desktop-hud-display", status: "available", method: "process-hud-marker", samples: [Math.round(hud.durationMs)], unit: "milliseconds", note: "explicit-hud-invocation" },
    { name: "desktop-idle-memory", status: "available", method: "resident-set-sampling", samples: memory, unit: "bytes", note: "post-ready-idle" }
  ] };
}
async function desktop() {
  if (!hostPlatform || !hostArchitecture) throw new Error("desktop performance profiling is unsupported on this host");
  const unavailableDesktop = (reason) => {
    const runtime = unavailable(hostPlatform, hostArchitecture, reason, desktopNames.filter((name) => name !== "desktop-package-size"));
    return result([{ ...runtime, measurements: [...runtime.measurements, packageMeasurement().measurement] }]);
  };
  if (process.platform === "linux" && !process.env.DISPLAY) return unavailableDesktop("no-display-server");
  const executable = findDesktopExecutable();
  if (!executable) return unavailableDesktop("artifact-not-found");
  const cold = await profileDesktop(executable, "cold-process");
  const warm = await profileDesktop(executable, "warm-process");
  const profiled = cold.failure ? cold : warm.failure ? warm : null;
  const unavailableReason = cold.unavailableReason ?? warm.unavailableReason;
  const profileMeasurement = (name) => {
    const compatible = [cold, warm].flatMap((profile) => profile.measurements).filter((measurement) => measurement.name === name);
    return compatible.length ? { ...compatible[0], samples: compatible.flatMap((measurement) => measurement.samples) } : undefined;
  };
  const measurements = [
    cold.measurements.find((measurement) => measurement.name === "desktop-cold-startup"),
    warm.measurements.find((measurement) => measurement.name === "desktop-warm-startup"),
    profileMeasurement("desktop-hud-display"),
    profileMeasurement("desktop-idle-memory")
  ].filter(Boolean);
  const target = { platform: hostPlatform, architecture: hostArchitecture, status: unavailableReason ? "unavailable" : profiled ? "failed" : "available", ...(unavailableReason ? { unavailableReason } : profiled ? { failure: profiled.failure } : {}), measurements };
  const size = packageMeasurement();
  target.measurements.push(size.measurement);
  if (!profiled && size.measurement.status === "unavailable") {
    target.status = "unavailable";
    target.unavailableReason = size.unavailableReason;
  }
  return result([target]);
}
function architectureFrom(value) { const normalized = value.toLowerCase(); return normalized.includes("arm64") || normalized.includes("aarch64") ? "arm64" : normalized.includes("armv7") || normalized.includes("armeabi-v7a") ? "armv7" : normalized.includes("x86_64") ? "x86_64" : null; }
function installedAndroidArchitecture(packageInfo) { return architectureFrom(packageInfo.match(/primaryCpuAbi=([^\s]+)/u)?.[1] ?? ""); }
function mobileArchitecture(platform, targetKind, selector) {
  const probe = platform === "android"
    ? run("adb", [...(selector ? ["-s", selector] : []), "shell", "dumpsys", "package", "dev.deli.devhud"])
    : targetKind === "ios-device"
      ? run("xcrun", ["devicectl", "device", "info", "details", "--device", selector, "--json-output", "-"])
      : run("xcrun", ["simctl", "getenv", "booted", "SIMULATOR_ARCHS"]);
  return probe.status === 0 ? platform === "android" ? installedAndroidArchitecture(probe.stdout) : architectureFrom(probe.stdout) : null;
}
function mobileBuildProvenance(platform, targetKind, selector) {
  if (platform === "android") {
    const outcome = run("adb", [...(selector ? ["-s", selector] : []), "shell", "dumpsys", "package", "dev.deli.devhud"]);
    return outcome.status === 0 ? outcome.stdout.match(/versionName=([^\s]+)/u)?.[1] ?? null : null;
  }
  if (targetKind === "ios-simulator") {
    const container = run("xcrun", ["simctl", "get_app_container", "booted", "dev.deli.devhud", "app"]);
    if (container.status !== 0 || !container.stdout.trim()) return null;
    const info = resolve(container.stdout.trim(), "Info.plist");
    const version = run("/usr/libexec/PlistBuddy", ["-c", "Print :CFBundleShortVersionString", info]);
    const revision = run("/usr/libexec/PlistBuddy", ["-c", "Print :DevHudTauriRevision", info]);
    const build = run("/usr/libexec/PlistBuddy", ["-c", "Print :DevHudBuildRevision", info]);
    return version.status === 0 && revision.status === 0 && build.status === 0 ? `${version.stdout.trim()}+${revision.stdout.trim()}+${build.stdout.trim()}` : null;
  }
  if (targetKind === "ios-device") {
    const outcome = run("xcrun", ["devicectl", "device", "info", "apps", "--device", selector, "--json-output", "-"]);
    if (outcome.status !== 0) return null;
    try {
      const findVersion = (value) => {
        if (Array.isArray(value)) return value.map(findVersion).find(Boolean) ?? null;
        if (!value || typeof value !== "object") return null;
        if (value.bundleIdentifier === "dev.deli.devhud") {
          const version = value.version ?? value.shortVersion ?? value.CFBundleShortVersionString;
          const revision = value.DevHudTauriRevision ?? value.tauriRevision;
          const build = value.DevHudBuildRevision ?? value.buildRevision;
          return version && revision && build ? `${version}+${revision}+${build}` : null;
        }
        return Object.values(value).map(findVersion).find(Boolean) ?? null;
      };
      return findVersion(JSON.parse(outcome.stdout));
    } catch { return null; }
  }
  return null;
}
function terminateMobileApp(platform, targetKind, selector) {
  if (platform === "android") return run("adb", [...(selector ? ["-s", selector] : []), "shell", "am", "force-stop", "dev.deli.devhud"]);
  if (targetKind === "ios-device") return run("xcrun", ["devicectl", "device", "process", "terminate", "--device", selector, "dev.deli.devhud"]);
  return run("xcrun", ["simctl", "terminate", "booted", "dev.deli.devhud"]);
}
function iosAppWasAlreadyAbsent(outcome) {
  return outcome.status !== 0 && /(?:not (?:currently )?running|not found|no such process|process.*(?:does not|isn't) running)/iu.test(`${outcome.stdout}\n${outcome.stderr}`);
}
function androidTargetIsEmulator(selector) {
  const outcome = run("adb", [...(selector ? ["-s", selector] : []), "shell", "getprop", "ro.kernel.qemu"]);
  return outcome.status === 0 ? outcome.stdout.trim() === "1" : null;
}
function mobile(platform, targetKind) {
  const unknownArchitecture = "unknown";
  if (platform === "ios" && process.platform !== "darwin") return result([unavailable("ios", unknownArchitecture, "unsupported-host", ["mobile-startup"], targetKind)]);
  const tool = platform === "android" ? "adb" : "xcrun";
  if (!executable(tool)) return result([unavailable(platform, unknownArchitecture, "tool-not-installed", ["mobile-startup"], targetKind)]);
  // The selector is consumed only for this launch and is never written to a result.
  const selector = process.env.DEVHUD_PERF_DEVICE;
  if (targetKind === "ios-device" && !selector) return result([unavailable(platform, unknownArchitecture, "no-supported-target", ["mobile-startup"], targetKind)]);
  if (targetKind === "android-device" && !selector) return result([unavailable(platform, unknownArchitecture, "no-supported-target", ["mobile-startup"], targetKind)]);
  const selected = selector ? ["-s", selector] : [];
  const probe = platform === "android" ? run("adb", [...selected, "get-state"]) : targetKind === "ios-simulator" ? run("xcrun", ["simctl", "bootstatus", "booted", "-b"]) : null;
  if (probe && probe.status !== 0) return result([unavailable(platform, unknownArchitecture, "no-supported-target", ["mobile-startup"], targetKind)]);
  if (platform === "android") {
    const isEmulator = androidTargetIsEmulator(selector);
    if (isEmulator === null || isEmulator !== (targetKind === "android-emulator")) return result([unavailable(platform, unknownArchitecture, "no-supported-target", ["mobile-startup"], targetKind)]);
  }
  const architecture = mobileArchitecture(platform, targetKind, selector);
  if (!architecture) return result([unavailable(platform, unknownArchitecture, "no-supported-target", ["mobile-startup"], targetKind)]);
  if (!buildRevision || mobileBuildProvenance(platform, targetKind, selector) !== `${application.version}+${revision}+${buildRevision}`) return result([unavailable(platform, architecture, "build-provenance-unverified", ["mobile-startup"], targetKind)]);
  const termination = terminateMobileApp(platform, targetKind, selector);
  // iOS reports a nonzero status when the process was already absent. Any
  // other termination error could turn the following launch into a warm activation.
  if (termination.status !== 0 && (platform !== "ios" || !iosAppWasAlreadyAbsent(termination))) return result([{ platform, architecture, targetKind, status: "failed", failure: "launch-failed", measurements: [] }]);
  const command = platform === "android" ? [...selected, "shell", "am", "start", "-W", "-n", "dev.deli.devhud/.MainActivity"] : targetKind === "ios-device" ? ["devicectl", "device", "process", "launch", "--device", selector, "dev.deli.devhud"] : ["simctl", "launch", "booted", "dev.deli.devhud"];
  const start = performance.now(); const outcome = run(tool, command); const elapsed = Math.round(performance.now() - start);
  if (outcome.status !== 0) return result([{ platform, architecture, targetKind, status: "failed", failure: "launch-failed", measurements: [] }]);
  return result([{ platform, architecture, targetKind, status: "available", measurements: [{ name: "mobile-startup", status: "available", method: platform === "android" ? "adb-am-start-w" : targetKind === "ios-device" ? "devicectl-launch-wall-clock" : "simctl-launch-wall-clock", samples: [elapsed], unit: "milliseconds", note: "cold-process" }] }]);
}
function validate(value) {
  const exactKeys = (candidate, keys) => Object.keys(candidate).every((key) => keys.includes(key));
  const allowedArchitectures = { linux: ["x86_64", "arm64"], macos: ["x86_64", "arm64"], windows: ["x86_64", "arm64"], android: ["x86_64", "arm64", "armv7", "unknown"], ios: ["x86_64", "arm64", "unknown"] };
  const expectedMethods = { "desktop-cold-startup": ["process-ready-marker"], "desktop-warm-startup": ["process-ready-marker"], "desktop-hud-display": ["process-hud-marker"], "desktop-package-size": ["artifact-byte-count"], "desktop-idle-memory": ["resident-set-sampling"], "mobile-startup": ["adb-am-start-w", "simctl-launch-wall-clock", "devicectl-launch-wall-clock"] };
  if (!value || !exactKeys(value, ["schemaVersion", "application", "targets"]) || value.schemaVersion !== schemaVersion || !Array.isArray(value.targets) || !value.targets.length) throw new Error("invalid performance result envelope");
  if (!value.application || !exactKeys(value.application, ["version", "tauriRevision", "cefRevision", "buildRevision"]) || value.application.version !== application.version || value.application.tauriRevision !== revision || value.application.cefRevision !== application.cefRevision || value.application.buildRevision !== buildRevision) throw new Error("invalid application provenance");
  const targetIdentities = new Set();
  for (const target of value.targets) {
    const allowedTargetKinds = { android: ["android-device", "android-emulator"], ios: ["ios-device", "ios-simulator"] };
    if (!target || !exactKeys(target, ["platform", "architecture", "targetKind", "status", "unavailableReason", "failure", "measurements"]) || !allowedArchitectures[target.platform]?.includes(target.architecture) || !["available", "unavailable", "failed"].includes(target.status) || !Array.isArray(target.measurements) || (["android", "ios"].includes(target.platform) ? !allowedTargetKinds[target.platform].includes(target.targetKind) : target.targetKind !== undefined)) throw new Error("invalid target");
    const targetIdentity = `${target.platform}/${target.architecture}/${target.targetKind ?? ""}`;
    if (targetIdentities.has(targetIdentity)) throw new Error("duplicate target identity");
    targetIdentities.add(targetIdentity);
    if (target.status === "unavailable" && (!["unsupported-host", "no-display-server", "tool-not-installed", "no-supported-target", "artifact-not-found", "build-provenance-unverified"].includes(target.unavailableReason) || target.failure !== undefined)) throw new Error("unavailable target missing reason");
    if (target.status === "failed" && (!["build-failed", "launch-failed", "measurement-protocol-failed", "startup-timeout", "startup-exited"].includes(target.failure) || target.unavailableReason !== undefined)) throw new Error("failed target missing failure");
    if (target.status === "available" && (target.unavailableReason !== undefined || target.failure !== undefined)) throw new Error("available target has status-specific fields");
    if (target.architecture === "unknown" && target.status !== "unavailable") throw new Error("unknown architecture must be unavailable");
    const measurementNames = new Set();
    for (const measurement of target.measurements) {
      const expectedUnit = ["desktop-package-size", "desktop-idle-memory"].includes(measurement?.name) ? "bytes" : "milliseconds";
      const validMobileMethod = measurement?.name !== "mobile-startup" || (target.platform === "android" ? measurement.method === "adb-am-start-w" : target.platform === "ios" && (target.targetKind === "ios-device" ? measurement.method === "devicectl-launch-wall-clock" : measurement.method === "simctl-launch-wall-clock"));
      const validPlatformMeasurement = ["linux", "macos", "windows"].includes(target.platform) ? measurement?.name !== "mobile-startup" : measurement?.name === "mobile-startup";
      if (!measurement || !exactKeys(measurement, ["name", "status", "method", "samples", "unit", "note"]) || !expectedMethods[measurement.name]?.includes(measurement.method) || !validMobileMethod || !validPlatformMeasurement || !["available", "unavailable", "failed"].includes(measurement.status) || !Array.isArray(measurement.samples) || !measurement.samples.every((sample) => Number.isFinite(sample) && sample >= 0) || (measurement.unit !== undefined && !["milliseconds", "bytes"].includes(measurement.unit)) || (measurement.status === "available" ? (!measurement.samples.length || measurement.unit !== expectedUnit) : (measurement.samples.length || measurement.unit !== undefined || measurement.note !== undefined)) || (measurement.note !== undefined && measurement.note !== expectedNotes[measurement.name])) throw new Error("invalid measurement");
      if (measurementNames.has(measurement.name)) throw new Error("duplicate measurement name");
      measurementNames.add(measurement.name);
    }
    const requiredMeasurements = ["linux", "macos", "windows"].includes(target.platform) ? desktopNames : ["mobile-startup"];
    if (target.status === "available" && !requiredMeasurements.every((name) => target.measurements.some((measurement) => measurement.name === name && measurement.status === "available"))) throw new Error("available target missing required measurements");
    if (target.status === "unavailable" && requiredMeasurements.every((name) => target.measurements.some((measurement) => measurement.name === name && measurement.status === "available"))) throw new Error("unavailable target has complete measurements");
    if (target.status === "failed" && requiredMeasurements.every((name) => target.measurements.some((measurement) => measurement.name === name && measurement.status === "available"))) throw new Error("failed target has complete measurements");
  }
  return true;
}
function canonicalText(value) { return JSON.stringify(canonicalize(value)); }
function targetIdentity(target) { return `${target.platform}/${target.architecture}/${target.targetKind ?? ""}`; }
function targetBase(target) { return { platform: target.platform, architecture: target.architecture, ...(target.targetKind ? { targetKind: target.targetKind } : {}) }; }
function mergeMeasurements(left, right) {
  const measurements = new Map();
  for (const measurement of [...left.measurements, ...right.measurements]) {
    const current = measurements.get(measurement.name);
    if (!current) measurements.set(measurement.name, measurement);
    else if (measurement.status === "available" && current.status !== "available") measurements.set(measurement.name, measurement);
    else if (canonicalText({ ...current, samples: [] }) === canonicalText({ ...measurement, samples: [] })) measurements.set(measurement.name, { ...current, samples: [...current.samples, ...measurement.samples].sort((a, b) => a - b) });
    else if (canonicalText(measurement).localeCompare(canonicalText(current)) < 0) measurements.set(measurement.name, measurement);
  }
  return [...measurements.values()].sort((a, b) => (desktopNames.indexOf(a.name) - desktopNames.indexOf(b.name)) || canonicalText(a).localeCompare(canonicalText(b)));
}
function mergeTargets(targets) {
  const ordered = [...targets].sort((left, right) => canonicalText(left).localeCompare(canonicalText(right)));
  const measurements = ordered.slice(1).reduce((merged, target) => mergeMeasurements({ measurements: merged }, target), ordered[0].measurements);
  const required = ["linux", "macos", "windows"].includes(ordered[0].platform) ? desktopNames : ["mobile-startup"];
  if (required.every((name) => measurements.some((measurement) => measurement.name === name && measurement.status === "available"))) return { ...targetBase(ordered[0]), status: "available", measurements };
  const failures = ordered.map((target) => target.failure).filter(Boolean).sort();
  if (failures.length) return { ...targetBase(ordered[0]), status: "failed", failure: failures[0], measurements };
  return { ...targetBase(ordered[0]), status: "unavailable", unavailableReason: ordered.map((target) => target.unavailableReason).filter(Boolean).sort()[0], measurements };
}
function aggregate(files) {
  if (!files.length) throw new Error("at least one performance result is required");
  const targets = [...new Set(files.map((file) => resolve(file)))].flatMap((file) => { const value = JSON.parse(readFileSync(file, "utf8")); validate(value); return value.targets; });
  if (!targets.length) throw new Error("at least one performance target is required");
  const grouped = new Map();
  for (const target of targets) {
    const key = targetIdentity(target);
    grouped.set(key, [...(grouped.get(key) ?? []), target]);
  }
  return result([...grouped.values()].map(mergeTargets).sort((a, b) => targetIdentity(a).localeCompare(targetIdentity(b))));
}
function summary(value) {
  const lines = ["# DevHud 0.1.0 performance summary", "", `Pinned Tauri/CEF revision: \`${revision}\``, "", "| Platform | Architecture | Target | Availability | Measurements |", "| --- | --- | --- | --- | --- |"];
  for (const target of value.targets) lines.push(`| ${target.platform} | ${target.architecture} | ${target.targetKind ?? "desktop"} | ${target.status}${target.unavailableReason ? ` (${target.unavailableReason})` : target.failure ? ` (${target.failure})` : ""} | ${target.measurements.map((item) => item.status === "available" ? `${item.name}: ${median(item.samples)} ${item.unit}` : `${item.name}: ${item.status}`).join("; ")} |`);
  return `${lines.join("\n")}\n`;
}
function desktopBuildFailed(packageSize = packageMeasurement().measurement) {
  if (!hostPlatform || !hostArchitecture) throw new Error("desktop performance profiling is unsupported on this host");
  return result([{ platform: hostPlatform, architecture: hostArchitecture, status: "failed", failure: "build-failed", measurements: [packageSize] }]);
}
function reportBuildFailure(build) {
  const detail = build.error?.code ? ` (${build.error.code})` : build.signal ? ` (signal ${build.signal})` : Number.isInteger(build.status) ? ` (exit ${build.status})` : "";
  console.error(`DevHud desktop performance build failed${detail}; rerun pnpm run build:desktop:performance to inspect build output.`);
}
async function main() {
  const [command, ...rawArgs] = process.argv.slice(2);
  const args = rawArgs.filter((argument) => argument !== "--");
  if (command === "desktop") { requireCleanWorktree(); if (!buildRevision) throw new Error("desktop performance profiling requires a Git commit revision"); const build = args.includes("--build") ? run(process.platform === "win32" ? "pnpm.cmd" : "pnpm", ["run", "build:desktop:performance"], buildTimeoutMs, { stdio: "inherit", env: { ...process.env, DEVHUD_BUILD_REVISION: buildRevision } }) : null; if (build && build.status !== 0) reportBuildFailure(build); const value = args.includes("--build-failed") || (build && build.status !== 0) ? desktopBuildFailed() : await desktop(); validate(value); console.log(writeResult(value, `desktop-${hostPlatform ?? "unsupported"}-${hostArchitecture}-${randomUUID()}.json`)); return; }
  if (command === "package") { requireCleanWorktree(); if (args.includes("--build")) { clearPackageArtifacts(); const build = run(process.platform === "win32" ? "pnpm.cmd" : "pnpm", ["run", "build:preview"], buildTimeoutMs, { stdio: "inherit" }); if (build.status !== 0) { reportBuildFailure(build); throw new Error("package build failed"); } } console.log(recordPackageProvenance()); return; }
  if (command === "mobile") { const platform = args[0]; const target = args[1] ?? (platform === "ios" ? "ios-simulator" : "android-emulator"); const allowedTargets = { android: ["android-device", "android-emulator"], ios: ["ios-device", "ios-simulator"] }; if (!allowedTargets[platform]?.includes(target)) throw new Error("Usage: perf:mobile -- <android|ios> <android-device|android-emulator|ios-device|ios-simulator>"); const value = mobile(platform, target); validate(value); console.log(writeResult(value, `${platform}-${target}-${randomUUID()}.json`)); return; }
  if (command === "aggregate") { const files = args.length ? args.map((file) => resolve(file)) : existsSync(outputDirectory) ? readdirSync(outputDirectory).filter((file) => file.endsWith(".json") && file !== "release-performance.json").map((file) => resolve(outputDirectory, file)) : []; const value = aggregate(files); validate(value); const json = writeResult(value, "release-performance.json"); const markdown = resolve(outputDirectory, "release-performance.md"); writeFileSync(markdown, summary(value)); console.log(`${json}\n${markdown}`); return; }
  if (command === "validate") { if (!args.length) throw new Error("Usage: perf:validate -- <result.json...>"); for (const file of args) validate(JSON.parse(readFileSync(resolve(file), "utf8"))); return; }
  throw new Error("Usage: performance.mjs <desktop|mobile|package|aggregate|validate>");
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main();
export { aggregate, canonicalize, desktopBuildFailed, installedAndroidArchitecture, packageMeasurement, profileDesktop, recordPackageProvenance, result, summary, validate };
