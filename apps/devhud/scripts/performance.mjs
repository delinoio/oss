/**
 * Non-gating release instrumentation. Results deliberately contain no host
 * names, device identifiers, paths, command output, environment values, or
 * diagnostic records. `unavailable` means the requested executable target was
 * not present; `failed` means an attempted measurement did not complete.
 */
import { spawn, spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const outputDirectory = resolve(appRoot, "performance/results");
const revision = "f49ebda2fdba5755456b0f049e32593ca0ea331a";
const schemaVersion = "devhud.performance.result.v1";
const application = { version: "0.1.0", tauriRevision: revision, cefRevision: `tauri-runtime-cef@${revision}` };
const hostPlatform = { darwin: "macos", win32: "windows", linux: "linux" }[process.platform];
const hostArchitecture = { x64: "x86_64", arm64: "arm64", arm: "armv7" }[process.arch] ?? "x86_64";
const desktopNames = ["desktop-cold-startup", "desktop-warm-startup", "desktop-hud-display", "desktop-package-size", "desktop-idle-memory"];

function result(targets) { return { schemaVersion, application, targets }; }
function writeResult(value, name) { mkdirSync(outputDirectory, { recursive: true }); const path = resolve(outputDirectory, name); writeFileSync(path, `${JSON.stringify(canonicalize(value), null, 2)}\n`); return path; }
function canonicalize(value) {
  if (Array.isArray(value)) return value.map(canonicalize);
  if (value && typeof value === "object") return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonicalize(value[key])]));
  return value;
}
function unavailable(platform, architecture, reason, names = []) { return { platform, architecture, status: "unavailable", unavailableReason: reason, measurements: names.map((name) => ({ name, status: "unavailable", method: methodFor(name), samples: [] })) }; }
function methodFor(name) { return name === "desktop-hud-display" ? "process-hud-marker" : name === "desktop-package-size" ? "artifact-byte-count" : name === "desktop-idle-memory" ? "resident-set-sampling" : name === "mobile-startup" ? "adb-am-start-w" : "process-ready-marker"; }
function run(command, args) { return spawnSync(command, args, { encoding: "utf8", timeout: 30_000 }); }
function executable(command) { return run(process.platform === "win32" ? "where" : "which", [command]).status === 0; }
function median(values) { const sorted = [...values].sort((a, b) => a - b); return sorted[Math.floor(sorted.length / 2)] ?? 0; }
function artifactBytes(path) { if (!existsSync(path)) return null; const stat = statSync(path); if (stat.isFile()) return stat.size; return readdirSync(path, { withFileTypes: true }).reduce((total, entry) => total + (artifactBytes(resolve(path, entry.name)) ?? 0), 0); }
function findDesktopArtifact() {
  const target = resolve(repositoryRoot, "target", "debug");
  const binary = resolve(target, process.platform === "win32" ? "devhud.exe" : "devhud");
  if (existsSync(binary)) return binary;
  const app = resolve(target, "bundle", "macos", "DevHud.app");
  return existsSync(app) ? app : null;
}
function rssBytes(pid) {
  if (process.platform === "win32") {
    const outcome = run("powershell.exe", ["-NoProfile", "-Command", `(Get-Process -Id ${pid}).WorkingSet64`]);
    return outcome.status === 0 ? Number(outcome.stdout.trim()) : null;
  }
  const outcome = run("ps", ["-o", "rss=", "-p", String(pid)]);
  const kb = Number(outcome.stdout.trim()); return Number.isFinite(kb) ? kb * 1024 : null;
}
async function profileDesktop(binary, startupNote) {
  const markers = [];
  const start = performance.now();
  const child = spawn(binary, [], { cwd: appRoot, env: { ...process.env, DEVHUD_PERF: "1" }, stdio: ["ignore", "pipe", "pipe"] });
  const closed = new Promise((resolveClosed) => child.once("close", resolveClosed));
  for (const stream of [child.stdout, child.stderr]) { stream.setEncoding("utf8"); stream.on("data", (chunk) => { for (const line of chunk.split(/\r?\n/u)) if (line.startsWith("DEVHUD_PERF ")) { try { markers.push({ ...JSON.parse(line.slice(12)), at: performance.now() - start }); } catch { /* marker protocol failure is handled below */ } } }); }
  await new Promise((resolveDone) => setTimeout(resolveDone, 1400));
  const memory = [rssBytes(child.pid), rssBytes(child.pid), rssBytes(child.pid)].filter((sample) => sample !== null);
  child.kill();
  await closed;
  const ready = markers.find((marker) => marker.event === "ready"); const hud = markers.find((marker) => marker.event === "hud-shown");
  if (!ready || !hud) return { failure: "measurement-protocol-failed", measurements: [] };
  return { measurements: [
    { name: startupNote === "cold-process" ? "desktop-cold-startup" : "desktop-warm-startup", status: "available", method: "process-ready-marker", samples: [Math.round(ready.at)], unit: "milliseconds", note: startupNote },
    { name: "desktop-hud-display", status: "available", method: "process-hud-marker", samples: [Math.max(0, Math.round(hud.at - ready.at))], unit: "milliseconds", note: "explicit-hud-invocation" },
    { name: "desktop-idle-memory", status: "available", method: "resident-set-sampling", samples: memory, unit: "bytes", note: "post-ready-idle" }
  ] };
}
async function desktop() {
  if (!hostPlatform) return result([unavailable("linux", hostArchitecture, "unsupported-host", desktopNames)]);
  if (process.platform === "linux" && !process.env.DISPLAY) return result([unavailable("linux", hostArchitecture, "no-display-server", desktopNames)]);
  const artifact = findDesktopArtifact();
  if (!artifact) return result([unavailable(hostPlatform, hostArchitecture, "artifact-not-found", desktopNames)]);
  const cold = await profileDesktop(artifact, "cold-process");
  const warm = await profileDesktop(artifact, "warm-process");
  const profiled = cold.failure ? cold : warm.failure ? warm : null;
  const measurements = profiled ? [] : [
    cold.measurements.find((measurement) => measurement.name === "desktop-cold-startup"),
    warm.measurements.find((measurement) => measurement.name === "desktop-warm-startup"),
    cold.measurements.find((measurement) => measurement.name === "desktop-hud-display"),
    cold.measurements.find((measurement) => measurement.name === "desktop-idle-memory")
  ].filter(Boolean);
  const target = { platform: hostPlatform, architecture: hostArchitecture, status: profiled ? "failed" : "available", ...(profiled ? { failure: profiled.failure } : {}), measurements };
  const size = artifactBytes(artifact);
  target.measurements.push(size === null ? { name: "desktop-package-size", status: "unavailable", method: "artifact-byte-count", samples: [] } : { name: "desktop-package-size", status: "available", method: "artifact-byte-count", samples: [size], unit: "bytes", note: "packaged-artifact" });
  return result([target]);
}
function mobile(platform, targetKind) {
  const architecture = targetKind === "android-device" || targetKind === "ios-device" ? "arm64" : "x86_64";
  if (platform === "ios" && process.platform !== "darwin") return result([unavailable("ios", architecture, "unsupported-host", ["mobile-startup"])]);
  const tool = platform === "android" ? "adb" : "xcrun";
  if (!executable(tool)) return result([unavailable(platform, architecture, "tool-not-installed", ["mobile-startup"])]);
  // The selector is consumed only for this launch and is never written to a result.
  const selector = process.env.DEVHUD_PERF_DEVICE;
  if (targetKind === "ios-device" && !selector) return result([unavailable(platform, architecture, "no-supported-target", ["mobile-startup"])]);
  const selected = selector ? ["-s", selector] : [];
  const probe = platform === "android" ? run("adb", [...selected, "get-state"]) : targetKind === "ios-simulator" ? run("xcrun", ["simctl", "bootstatus", "booted", "-b"]) : null;
  if (probe && probe.status !== 0) return result([unavailable(platform, architecture, "no-supported-target", ["mobile-startup"])]);
  const command = platform === "android" ? [...selected, "shell", "am", "start", "-W", "-n", "dev.deli.devhud/.MainActivity"] : targetKind === "ios-device" ? ["devicectl", "device", "process", "launch", "--device", selector, "dev.deli.devhud"] : ["simctl", "launch", "booted", "dev.deli.devhud"];
  const start = performance.now(); const outcome = run(tool, command); const elapsed = Math.round(performance.now() - start);
  if (outcome.status !== 0) return result([{ platform, architecture, status: "failed", failure: "launch-failed", measurements: [] }]);
  return result([{ platform, architecture, status: "available", measurements: [{ name: "mobile-startup", status: "available", method: platform === "android" ? "adb-am-start-w" : targetKind === "ios-device" ? "devicectl-launch-wall-clock" : "simctl-launch-wall-clock", samples: [elapsed], unit: "milliseconds", note: "cold-process" }] }]);
}
function validate(value) {
  const exactKeys = (candidate, keys) => Object.keys(candidate).every((key) => keys.includes(key));
  if (!value || !exactKeys(value, ["schemaVersion", "application", "targets"]) || value.schemaVersion !== schemaVersion || !Array.isArray(value.targets)) throw new Error("invalid performance result envelope");
  if (!value.application || !exactKeys(value.application, ["version", "tauriRevision", "cefRevision"]) || value.application.version !== application.version || value.application.tauriRevision !== revision || value.application.cefRevision !== application.cefRevision) throw new Error("invalid application provenance");
  for (const target of value.targets) {
    if (!target || !exactKeys(target, ["platform", "architecture", "status", "unavailableReason", "failure", "measurements"]) || !["linux", "macos", "windows", "android", "ios"].includes(target.platform) || !["x86_64", "arm64", "armv7"].includes(target.architecture) || !["available", "unavailable", "failed"].includes(target.status) || !Array.isArray(target.measurements)) throw new Error("invalid target");
    if (target.status === "unavailable" && !["unsupported-host", "no-display-server", "tool-not-installed", "no-supported-target", "artifact-not-found"].includes(target.unavailableReason)) throw new Error("unavailable target missing reason");
    if (target.status === "failed" && !["build-failed", "launch-failed", "measurement-protocol-failed"].includes(target.failure)) throw new Error("failed target missing failure");
    for (const measurement of target.measurements) {
      if (!measurement || !exactKeys(measurement, ["name", "status", "method", "samples", "unit", "note"]) || ![...desktopNames, "mobile-startup"].includes(measurement.name) || !["available", "unavailable", "failed"].includes(measurement.status) || !["process-ready-marker", "process-hud-marker", "artifact-byte-count", "resident-set-sampling", "adb-am-start-w", "simctl-launch-wall-clock", "devicectl-launch-wall-clock"].includes(measurement.method) || !Array.isArray(measurement.samples) || !measurement.samples.every((sample) => Number.isFinite(sample) && sample >= 0)) throw new Error("invalid measurement");
    }
  }
  return true;
}
function aggregate(files) {
  const targets = files.flatMap((file) => JSON.parse(readFileSync(file, "utf8")).targets);
  const deduplicated = new Map();
  for (const target of targets.sort((a, b) => `${a.platform}/${a.architecture}/${a.status}`.localeCompare(`${b.platform}/${b.architecture}/${b.status}`))) deduplicated.set(`${target.platform}/${target.architecture}`, target);
  return result([...deduplicated.values()].sort((a, b) => `${a.platform}/${a.architecture}`.localeCompare(`${b.platform}/${b.architecture}`)));
}
function summary(value) {
  const lines = ["# DevHud 0.1.0 performance summary", "", `Pinned Tauri/CEF revision: \`${revision}\``, "", "| Platform | Architecture | Availability | Measurements |", "| --- | --- | --- | --- |"];
  for (const target of value.targets) lines.push(`| ${target.platform} | ${target.architecture} | ${target.status}${target.unavailableReason ? ` (${target.unavailableReason})` : target.failure ? ` (${target.failure})` : ""} | ${target.measurements.map((item) => item.status === "available" ? `${item.name}: ${median(item.samples)} ${item.unit}` : `${item.name}: ${item.status}`).join("; ")} |`);
  return `${lines.join("\n")}\n`;
}
async function main() {
  const [command, ...rawArgs] = process.argv.slice(2);
  const args = rawArgs.filter((argument) => argument !== "--");
  if (command === "desktop") { const value = await desktop(); validate(value); console.log(writeResult(value, `desktop-${hostPlatform ?? "unsupported"}-${hostArchitecture}.json`)); return; }
  if (command === "mobile") { const platform = args[0]; const target = args[1] ?? `${platform}-emulator`; if (!["android", "ios"].includes(platform) || ![`${platform}-device`, `${platform}-emulator`, "ios-simulator"].includes(target)) throw new Error("Usage: perf:mobile -- <android|ios> <android-device|android-emulator|ios-device|ios-simulator>"); const value = mobile(platform, target); validate(value); console.log(writeResult(value, `${platform}-${target}.json`)); return; }
  if (command === "aggregate") { const files = args.length ? args.map((file) => resolve(file)) : existsSync(outputDirectory) ? readdirSync(outputDirectory).filter((file) => file.endsWith(".json") && file !== "release-performance.json").map((file) => resolve(outputDirectory, file)) : []; const value = aggregate(files); validate(value); const json = writeResult(value, "release-performance.json"); const markdown = resolve(outputDirectory, "release-performance.md"); writeFileSync(markdown, summary(value)); console.log(`${json}\n${markdown}`); return; }
  if (command === "validate") { for (const file of args) validate(JSON.parse(readFileSync(resolve(file), "utf8"))); return; }
  throw new Error("Usage: performance.mjs <desktop|mobile|aggregate|validate>");
}
if (process.argv[1] === new URL(import.meta.url).pathname) await main();
export { aggregate, canonicalize, result, summary, validate };
