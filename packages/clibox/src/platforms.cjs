"use strict";

const Platform = Object.freeze({ Darwin: "darwin", Windows: "win32", Linux: "linux" });
const Architecture = Object.freeze({ X64: "x64", Arm64: "arm64" });
const Libc = Object.freeze({ Glibc: "glibc", Musl: "musl" });
const targets = Object.freeze([
  { suffix: "darwin-x64", os: Platform.Darwin, cpu: Architecture.X64, rust: "x86_64-apple-darwin" },
  { suffix: "darwin-arm64", os: Platform.Darwin, cpu: Architecture.Arm64, rust: "aarch64-apple-darwin" },
  { suffix: "win32-x64-msvc", os: Platform.Windows, cpu: Architecture.X64, rust: "x86_64-pc-windows-msvc" },
  { suffix: "win32-arm64-msvc", os: Platform.Windows, cpu: Architecture.Arm64, rust: "aarch64-pc-windows-msvc" },
  { suffix: "linux-x64-gnu", os: Platform.Linux, cpu: Architecture.X64, libc: Libc.Glibc, rust: "x86_64-unknown-linux-gnu" },
  { suffix: "linux-arm64-gnu", os: Platform.Linux, cpu: Architecture.Arm64, libc: Libc.Glibc, rust: "aarch64-unknown-linux-gnu" },
  { suffix: "linux-x64-musl", os: Platform.Linux, cpu: Architecture.X64, libc: Libc.Musl, rust: "x86_64-unknown-linux-musl" },
  { suffix: "linux-arm64-musl", os: Platform.Linux, cpu: Architecture.Arm64, libc: Libc.Musl, rust: "aarch64-unknown-linux-musl" },
].map((target) => Object.freeze({ ...target, name: `@delino/clibox-${target.suffix}`, binary: target.os === Platform.Windows ? "clibox.exe" : "clibox" })));

function currentLibc() {
  // Only inspect the header; the full diagnostic report contains environment
  // values and must never be logged or persisted by this launcher.
  return process.report.getReport().header.glibcVersionRuntime ? Libc.Glibc : Libc.Musl;
}

function selectTarget(os = process.platform, cpu = process.arch, libc = os === Platform.Linux ? currentLibc() : undefined) {
  return targets.find((target) => target.os === os && target.cpu === cpu && target.libc === libc);
}

module.exports = { Platform, Architecture, Libc, targets, selectTarget };
