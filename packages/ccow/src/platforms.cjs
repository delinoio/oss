"use strict";
// Native target table shared by the build, packaging and verification scripts.
const targets = Object.freeze(
  [
    {
      suffix: "darwin-arm64",
      os: "darwin",
      cpu: "arm64",
      rust: "aarch64-apple-darwin",
      binary: "ccow",
    },
    {
      suffix: "linux-x64",
      os: "linux",
      cpu: "x64",
      rust: "x86_64-unknown-linux-gnu",
      binary: "ccow",
    },
    {
      suffix: "linux-arm64",
      os: "linux",
      cpu: "arm64",
      rust: "aarch64-unknown-linux-gnu",
      binary: "ccow",
    },
    {
      suffix: "windows-x64",
      os: "win32",
      cpu: "x64",
      rust: "x86_64-pc-windows-msvc",
      binary: "ccow.exe",
    },
  ].map((target) => Object.freeze(target)),
);
module.exports = { targets };
