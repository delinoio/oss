import { openSync, readSync, closeSync, lstatSync, readdirSync, readFileSync, realpathSync, mkdirSync, rmSync, copyFileSync } from "node:fs";
import { homedir } from "node:os";
import { join, relative } from "node:path";

export const targets = Object.freeze([
  { target: "x86_64-apple-darwin", platform: "darwin", arch: "x64", runner: "macos-15-intel", cef: "cef_macos_x86_64" },
  { target: "aarch64-apple-darwin", platform: "darwin", arch: "arm64", runner: "macos-15", cef: "cef_macos_aarch64" },
  { target: "x86_64-pc-windows-msvc", platform: "win32", arch: "x64", runner: "windows-2022", cef: "cef_windows_x86_64" },
  { target: "aarch64-pc-windows-msvc", platform: "win32", arch: "arm64", runner: "windows-11-arm", cef: "cef_windows_aarch64" },
  { target: "x86_64-unknown-linux-gnu", platform: "linux", arch: "x64", runner: "ubuntu-22.04", cef: "cef_linux_x86_64" },
  { target: "aarch64-unknown-linux-gnu", platform: "linux", arch: "arm64", runner: "ubuntu-22.04-arm", cef: "cef_linux_aarch64" },
]);

export function selectTarget(target, platform, arch) {
  const selected = targets.find(item => item.target === target);
  if (!selected || selected.platform !== platform || selected.arch !== arch) throw new Error("A dry run requires the matching native target host.");
  return selected;
}

export function acquireNativeBuildLock(root) {
  const directory = join(root, "target/delidev-dry-run");
  mkdirSync(directory, { recursive: true });
  const lock = join(directory, ".build.lock");
  closeSync(openSync(lock, "wx", 0o600));
  let released = false;
  return () => { if (!released) { rmSync(lock); released = true; } };
}

export function verifyPackageRevision(expected, current, status) {
  if (!/^[a-f0-9]{40}$/.test(expected) || current.trim() !== expected || status.trim()) throw new Error("Source changed during packaging; no revision-bound result was published.");
}

// The pinned cef 150.0.0 crate resolves to distribution 150.0.10. Keep this
// pairing explicit; a runtime upgrade requires reviewing the bundled notices.
export function cefCredits(selected, environment, home = homedir()) {
  const cache = selected.platform === "darwin" ? join(home, "Library/Caches")
    : selected.platform === "win32" ? environment.LOCALAPPDATA : join(home, ".cache");
  if (!cache) throw new Error("The native CEF cache location is unavailable.");
  const directory = join(cache, "tauri-cef", "150.0.10", selected.cef);
  const archive = JSON.parse(readFileSync(join(directory, "archive.json"), "utf8"));
  const distribution = selected.platform === "darwin" ? `macos${selected.arch === "arm64" ? "arm64" : "x64"}`
    : selected.platform === "win32" ? `windows${selected.arch === "arm64" ? "arm64" : "64"}` : `linux${selected.arch === "arm64" ? "arm64" : "64"}`;
  if (archive.type !== "minimal" || archive.name !== `cef_binary_150.0.10+g8042e43+chromium-150.0.7871.101_${distribution}_minimal.tar.bz2`) throw new Error("The CEF notice source does not match the pinned distribution.");
  const credits = join(directory, "CREDITS.html");
  if (!lstatSync(credits).isFile() || lstatSync(credits).size === 0) throw new Error("The original Chromium notices are missing.");
  return credits;
}

// A bare Cargo build downloads CEF into OUT_DIR. The pinned Tauri CLI sets
// its own versioned cache for that build; prepare it before reading or bundling
// the original notices. Remove this extra build when upstream exposes an
// independently verified distribution-preparation command.
export function prepareCefCredits(selected, environment, build, home = homedir()) {
  const target = selected.platform === "darwin" ? [] : ["--target", selected.target];
  build("cargo", ["run", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli", "--bin", "delidev-tauri-cli", "--", "build", "--no-bundle", ...target, "--features", "desktop-host,custom-protocol,tauri/cef"]);
  return cefCredits(selected, environment, home);
}

// The pinned Windows resource resolver strips a drive prefix from absolute
// source keys. Stage the unchanged original notices on the checkout drive and
// pass a relative key until upstream preserves cross-drive source paths.
export function cefResourcePath(app, root, selected, credits) {
  const directory = join(root, "target/delidev-package-notices", selected.target);
  mkdirSync(directory, { recursive: true });
  const staged = join(directory, "Chromium-CREDITS.html");
  copyFileSync(credits, staged);
  return relative(join(app, "src-tauri"), staged);
}

export function packageResources(app, root, credits) {
  return {
    [join(root, "LICENSE")]: "notices/LICENSE",
    [join(root, "NOTICE")]: "notices/NOTICE",
    [join(app, "src-tauri/notices/CEF-LICENSE.txt")]: "notices/CEF-LICENSE.txt",
    [credits]: "notices/Chromium-CREDITS.html",
  };
}

export function verifyNotices(directory, resources) {
  for (const [source, destination] of Object.entries(resources)) {
    const file = join(directory, destination);
    if (!lstatSync(file).isFile() || !readFileSync(file).equals(readFileSync(source))) throw new Error("A required original license or notice is missing or changed.");
  }
}

// Read only bounded headers: libcef may exceed a gigabyte. Unsupported formats,
// truncation and an installer masquerading as a native binary fail closed.
export function binaryArchitecture(path, platform) {
  if (!lstatSync(path).isFile()) throw new Error("A required native binary is not a regular file.");
  const fd = openSync(path, "r");
  const bytes = Buffer.alloc(64);
  try {
    if (readSync(fd, bytes, 0, bytes.length, 0) !== bytes.length) throw new Error("Truncated native binary.");
    if (platform === "linux" && bytes.subarray(0, 4).equals(Buffer.from([0x7f, 0x45, 0x4c, 0x46])) && bytes[4] === 2 && bytes[5] === 1) {
      const machine = bytes.readUInt16LE(18);
      if (machine === 62) return "x64";
      if (machine === 183) return "arm64";
    }
    if (platform === "win32" && bytes.subarray(0, 2).toString() === "MZ") {
      const offset = bytes.readUInt32LE(60);
      if (offset < 64 || offset > 1 << 20 || readSync(fd, bytes, 0, 26, offset) !== 26 || bytes.readUInt32LE(0) !== 0x4550 || bytes.readUInt16LE(24) !== 0x20b) throw new Error("Invalid PE64 header.");
      if (bytes.readUInt16LE(4) === 0x8664) return "x64";
      if (bytes.readUInt16LE(4) === 0xaa64) return "arm64";
    }
    throw new Error("Unknown native binary architecture.");
  } finally { closeSync(fd); }
}

export function verifyNativePayload(directory, selected) {
  const windows = selected.platform === "win32";
  const main = windows ? join(directory, "delidev-desktop.exe") : join(directory, "usr/share/DeliDev/delidev-desktop");
  const sidecar = windows ? join(directory, "delidev.exe") : join(directory, "usr/bin/delidev");
  const cefRoot = windows ? directory : join(directory, "usr/share/DeliDev");
  for (const path of [main, sidecar, join(cefRoot, windows ? "libcef.dll" : "libcef.so")]) {
    if (binaryArchitecture(path, selected.platform) !== selected.arch) throw new Error("The package contains a foreign native architecture.");
  }
  if (!windows && realpathSync(join(directory, "usr/bin/delidev-desktop")) !== realpathSync(main)) throw new Error("The Debian CEF launcher does not point to its original executable.");
  const required = ["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak", "v8_context_snapshot.bin", "locales/en-US.pak"];
  for (const name of required) {
    const file = lstatSync(join(cefRoot, name));
    if (!file.isFile() || file.size === 0) throw new Error("A required CEF resource is missing or invalid.");
  }
}

export function verifyAppImagePayload(directory, selected) {
  if (selected.platform !== "linux") throw new Error("AppImage verification requires a Linux target.");
  // The pinned CEF AppImage packager uses sharun launchers in bin and real
  // executables in shared/bin. Debian retains its separate installed layout.
  for (const name of ["delidev-desktop", "delidev"]) {
    if (binaryArchitecture(join(directory, "shared/bin", name), "linux") !== selected.arch) throw new Error("The AppImage contains a foreign product architecture.");
    const launcher = join(directory, "bin", name);
    const sharun = join(directory, "sharun");
    const info = lstatSync(sharun);
    if (!info.isFile() || info.size > 8 * 1024 * 1024 || !lstatSync(launcher).isFile()
      || binaryArchitecture(launcher, "linux") !== selected.arch
      || !readFileSync(launcher).equals(readFileSync(sharun))) throw new Error("An AppImage product launcher is invalid.");
  }
  if (binaryArchitecture(join(directory, "bin/libcef.so"), "linux") !== selected.arch) throw new Error("The AppImage contains a foreign CEF architecture.");
  for (const name of ["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak", "v8_context_snapshot.bin", "locales/en-US.pak"]) {
    const info = lstatSync(join(directory, "bin", name));
    if (!info.isFile() || info.size === 0) throw new Error("A required AppImage CEF resource is missing.");
  }
}

export function findOneFile(root, name) {
  const found = [];
  const pending = [root];
  let count = 0;
  while (pending.length) {
    const directory = pending.pop();
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (++count > 10000 || entry.isSymbolicLink()) throw new Error("The extracted package inventory is invalid.");
      const path = join(directory, entry.name);
      if (entry.isDirectory()) pending.push(path);
      else if (entry.isFile() && entry.name === name) found.push(path);
    }
  }
  if (found.length !== 1) throw new Error("Expected exactly one original packaged executable.");
  return found[0];
}
