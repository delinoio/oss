#!/usr/bin/env node
import { main } from "../dist/cli.js";
const code = await main(process.argv.slice(2));
process.exitCode = code;
// A trusted task may ignore cancellation and retain timers. The one-shot CLI
// exits only after main has disposed the returned session and flushed output.
if (code === 130 || code === 143) process.exit(code);
