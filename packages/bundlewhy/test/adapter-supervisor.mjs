// SPDX-License-Identifier: Apache-2.0

import { spawn } from "node:child_process";
import fs from "node:fs";

const [adapterPath, requestPath] = process.argv.slice(2);
const { protocolToken } = JSON.parse(fs.readFileSync(requestPath, "utf8"));
const prefix = `BUNDLEWHY-FRAME ${protocolToken} `;
const child = spawn(process.execPath, [adapterPath, requestPath], {
  cwd: process.cwd(),
  env: process.env,
  stdio: ["pipe", "pipe", "inherit"],
});

let pending = "";
child.stdout.setEncoding("utf8");
child.stdout.on("data", (chunk) => {
  process.stdout.write(chunk);
  pending += chunk;
  let newline = pending.indexOf("\n");
  while (newline >= 0) {
    const line = pending.slice(0, newline);
    pending = pending.slice(newline + 1);
    if (line.startsWith(prefix)) {
      try {
        if (JSON.parse(line.slice(prefix.length)).event === "protection") {
          child.stdin.write(`BUNDLEWHY-PROTECTION-ACK ${protocolToken}\n`);
        }
      } catch (error) {
        console.error("Invalid adapter protocol frame:", error);
        child.kill();
      }
    }
    newline = pending.indexOf("\n");
  }
});
child.on("error", (error) => {
  console.error("Could not start adapter:", error);
  process.exitCode = 1;
});
child.on("close", (code, signal) => {
  process.exitCode = signal ? 1 : (code ?? 1);
});
