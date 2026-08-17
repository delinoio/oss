import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

const blocker = createServer();
await new Promise((resolve, reject) => {
  blocker.once("error", reject);
  blocker.listen(46306, "127.0.0.1", resolve);
});

const child = spawn(
  process.execPath,
  ["../../scripts/run-rsbuild-dev.mjs", "devhud-admin", "46306"],
  {
    cwd: fileURLToPath(new URL("..", import.meta.url)),
    stdio: ["ignore", "pipe", "pipe"],
  },
);
const result = await Promise.race([
  new Promise((resolve) => child.once("exit", (code) => resolve(code))),
  new Promise((resolve) => setTimeout(() => resolve("timeout"), 10000)),
]);
blocker.close();
if (result === "timeout") {
  child.kill("SIGTERM");
  throw new Error("Strict-port development server did not reject a collision.");
}
if (result === 0) {
  throw new Error("Development server accepted a port collision.");
}
