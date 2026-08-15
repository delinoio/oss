import { spawn } from "node:child_process";
import { writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { fileURLToPath } from "node:url";

const [mode, statusPath] = process.argv.slice(2);

if (mode === "manager") {
  spawn(process.execPath, [fileURLToPath(import.meta.url), "server", statusPath], {
    shell: false,
    stdio: "ignore",
    windowsHide: true,
  });
  setInterval(() => {}, 1_000);
} else if (mode === "server") {
  const server = createServer();
  server.listen(0, "127.0.0.1", () => {
    const address = server.address();
    writeFileSync(statusPath, JSON.stringify({ pid: process.pid, port: address.port }));
  });
} else {
  throw new Error(`unsupported process-tree child mode: ${mode}`);
}
