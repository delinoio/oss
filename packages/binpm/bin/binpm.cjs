#!/usr/bin/env node
// SPDX-License-Identifier: Apache-2.0
"use strict";
const { resolveBinary, launch } = require("../src/launcher.cjs");

async function main() {
  try {
    const { code, signal } = await launch(
      resolveBinary(),
      process.argv.slice(2),
    );
    if (signal) {
      process.exitCode = 1;
      process.kill(process.pid, signal);
    } else process.exitCode = code ?? 1;
  } catch (error) {
    console.error(
      JSON.stringify({
        event: "binpm_npm_launcher_error",
        message: error.message,
      }),
    );
    process.exitCode = 1;
  }
}
void main();
