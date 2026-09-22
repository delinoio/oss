#!/usr/bin/env node
"use strict";

const { readFileSync, realpathSync } = require("node:fs");
const path = require("node:path");

// Resolve the published alias from this checkout, never the private source
// workspace or an ambient PATH executable. The official launcher owns platform
// selection, literal argv, stdio, and signal forwarding.
function resolveLauncher(root = path.resolve(__dirname, "..")) {
  const manifestPath = path.join(root, "package.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  const version = /^npm:@delino\/clibox@(\d+\.\d+\.\d+)$/u.exec(manifest.devDependencies?.["clibox-prebuilt"])?.[1];
  if (!version) throw new Error("The repository requires an exact clibox-prebuilt npm alias.");
  try {
    // The CLI intentionally exports no JavaScript API, including package.json.
    // Read the explicitly installed alias rather than using package subpath imports.
    const installedPath = realpathSync(path.join(root, "node_modules/clibox-prebuilt/package.json"));
    const installed = JSON.parse(readFileSync(installedPath, "utf8"));
    if (installed.name !== "@delino/clibox" || installed.version !== version || installed.private === true || installed.bin?.clibox !== "bin/clibox.cjs") {
      throw new Error("Unexpected clibox package.");
    }
    return { version, launcher: path.join(path.dirname(installedPath), "bin/clibox.cjs") };
  } catch {
    throw new Error("Install the pinned clibox prebuilt with pnpm install (including optional dependencies).");
  }
}

module.exports = { resolveLauncher };

if (require.main === module) {
  try {
    require(resolveLauncher().launcher);
  } catch (error) {
    console.error(JSON.stringify({ event: "repository_clibox_failed", message: error.message }));
    process.exitCode = 1;
  }
}
