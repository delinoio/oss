import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import net from "node:net";
import { fileURLToPath } from "node:url";
import test from "node:test";

for (const [command, port] of [["dev", 46310], ["preview", 46281]]) {
  test(`${command} fails on its fixed port instead of remapping`, async () => {
    const server = net.createServer();
    await new Promise((resolve, reject) => { server.once("error", reject); server.listen(port, "127.0.0.1", resolve); });
    try {
      const child = spawn(process.execPath, [fileURLToPath(new URL("../../../scripts/run-rspress-port.mjs", import.meta.url)), "async-commit-hook-docs", command, String(port), "-"], { stdio: ["ignore", "pipe", "pipe"] });
      let output = "";
      child.stderr.on("data", (value) => { output += value; });
      const code = await new Promise((resolve, reject) => { child.once("error", reject); child.once("close", resolve); });
      assert.equal(code, 1);
      assert.match(output, new RegExp(`port ${port} is already in use`));
    } finally { await new Promise((resolve) => server.close(resolve)); }
  });
}
