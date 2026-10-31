"use strict";
const targets = Object.freeze(
  [
    {
      suffix: "darwin-arm64",
      os: "darwin",
      cpu: "arm64",
      goos: "darwin",
      goarch: "arm64",
      binary: "cowc",
    },
    {
      suffix: "linux-x64",
      os: "linux",
      cpu: "x64",
      goos: "linux",
      goarch: "amd64",
      binary: "cowc",
    },
    {
      suffix: "windows-x64",
      os: "win32",
      cpu: "x64",
      goos: "windows",
      goarch: "amd64",
      binary: "cowc.exe",
    },
  ].map((target) =>
    Object.freeze({ ...target, name: `@delino/cowc-${target.suffix}` }),
  ),
);
const selectTarget = (os = process.platform, cpu = process.arch) =>
  targets.find((target) => target.os === os && target.cpu === cpu);
module.exports = { targets, selectTarget };
