import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import net from "node:net";
import { fileURLToPath } from "node:url";
import test from "node:test";

for (const [command, port] of [["dev", 46311], ["preview", 46281]]) {
  test(`${command} fails on its fixed port instead of remapping`, async () => {
    const server = net.createServer();
    const owned = await new Promise((resolve, reject) => {
      // An already-running local preview is also a valid conflict. Do not stop
      // it or fail fixture setup before exercising the wrapper's rejection.
      server.once("error", (error) => error.code === "EADDRINUSE" ? resolve(false) : reject(error));
      server.listen(port, "127.0.0.1", () => resolve(true));
    });
    try {
      const child = spawn(process.execPath, [fileURLToPath(new URL("../../../scripts/run-rspress-port.mjs", import.meta.url)), "async-commit-hook-docs", command, String(port), "-"], { stdio: ["ignore", "pipe", "pipe"] });
      let output = "";
      child.stderr.on("data", (value) => { output += value; });
      const code = await new Promise((resolve, reject) => { child.once("error", reject); child.once("close", resolve); });
      assert.equal(code, 1);
      assert.match(output, new RegExp(`port ${port} is already in use`));
    } finally { if (owned) await new Promise((resolve) => server.close(resolve)); }
  });
}
