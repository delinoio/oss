#!/usr/bin/env node

import { doctor, down, login, start } from "./orchestrator.mjs";

const [command, argument, ...extra] = process.argv.slice(2);

async function main() {
  if (extra.length > 0 || (command !== "start" && argument !== undefined)) {
    throw new Error("[arguments.invalid] development commands do not accept passthrough arguments");
  }
  if (command === "login") return login();
  if (command === "doctor") return doctor();
  if (command === "down") return down();
  if (command === "start") {
    const result = await start(argument);
    if (result.signal) {
      process.kill(process.pid, result.signal);
      return;
    }
    process.exitCode = result.code ?? 0;
    return;
  }
  throw new Error("[command.invalid] use login, doctor, start team, start oss, or down");
}

try {
  await main();
} catch (error) {
  process.stderr.write(`${error.message}\n`);
  process.exitCode = 1;
}
