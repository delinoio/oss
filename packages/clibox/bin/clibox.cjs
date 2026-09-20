#!/usr/bin/env node
"use strict";

const { resolveBinary, launch } = require("../src/launcher.cjs");

async function main() {
  try {
    const { code, signal } = await launch(resolveBinary(), process.argv.slice(2));
    if (signal) {
      // Remove launcher signal handlers before reproducing native termination.
      // Windows cannot reproduce POSIX wait statuses, but still exits nonzero.
      process.exitCode = 1;
      process.kill(process.pid, signal);
    } else process.exitCode = code ?? 1;
  } catch (error) {
    console.error(JSON.stringify({ event: "clibox_launcher_error", code: error.code ?? "launcher-failed", message: error.code ? error.message : "Unable to launch clibox. Reinstall the package for this platform." }));
    process.exitCode = 1;
  }
}

void main();
