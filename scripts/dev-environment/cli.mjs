#!/usr/bin/env node

import {
  doctor,
  down,
  login,
  runWithLifecycle,
  start,
} from "./orchestrator.mjs";

const [command, argument, ...extra] = process.argv.slice(2);

function complete(result) {
  if (result?.signal) {
    process.kill(process.pid, result.signal);
    return;
  }
  if (result?.code !== undefined) process.exitCode = result.code ?? 0;
}

async function main() {
  if (extra.length > 0 || (command !== "start" && argument !== undefined)) {
    throw new Error("[arguments.invalid] development commands do not accept passthrough arguments");
  }
  if (command === "login") {
    return complete(await runWithLifecycle((lifecycle) => login(lifecycle)));
  }
  if (command === "doctor") return doctor();
  if (command === "down") {
    return complete(
      await runWithLifecycle((lifecycle) => down({ lifecycle })),
    );
  }
  if (command === "start") {
    return complete(await start(argument));
  }
  throw new Error("[command.invalid] use login, doctor, start team, start oss, or down");
}

try {
  await main();
} catch (error) {
  process.stderr.write(`${error.message}\n`);
  process.exitCode = 1;
}
