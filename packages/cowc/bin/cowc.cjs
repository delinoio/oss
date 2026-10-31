#!/usr/bin/env node
"use strict";
const { resolveBinary, launch } = require("../src/launcher.cjs");
(async () => {
  try {
    const result = await launch(resolveBinary(), process.argv.slice(2));
    process.exitCode = result.code;
  } catch {
    console.error(
      "cowc: unable to verify or launch the native executable. Use Node.js 24+ and reinstall @delino/cowc with optional dependencies enabled for this platform.",
    );
    process.exitCode = 126;
  }
})();
