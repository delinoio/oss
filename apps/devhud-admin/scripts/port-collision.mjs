import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

const blocker = createServer();
await new Promise((resolve, reject) => {
  blocker.once("error", reject);
  blocker.listen(46306, "localhost", resolve);
});

const child = spawn(
  process.execPath,
  ["../../scripts/run-rsbuild-dev.mjs", "devhud-admin", "46306"],
  {
    cwd: fileURLToPath(new URL("..", import.meta.url)),
    env: {
      ...process.env,
      DEVHUD_LOGTO_ISSUER: "https://auth.example.test",
    },
    stdio: ["ignore", "pipe", "pipe"],
  },
);
let output = "";
child.stdout.setEncoding("utf8");
child.stderr.setEncoding("utf8");
child.stdout.on("data", (chunk) => {
  output += chunk;
});
child.stderr.on("data", (chunk) => {
  output += chunk;
});
const result = await Promise.race([
  new Promise((resolve) => child.once("close", (code) => resolve(code))),
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
const portConflictPattern =
  /(?:EADDRINUSE|port\s+46306\b[^\n]*(?:(?:already\s+)?in\s+use|occupied))/iu;
if (!portConflictPattern.test(output)) {
  throw new Error(`Development server failed before detecting the port collision:\n${output}`);
}
