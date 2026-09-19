import net from "node:net";
import { spawn } from "node:child_process";
if (process.argv.length > 2) throw new Error("ach development has a fixed localhost:46308 binding; address overrides are not supported");
const probe = net.createServer();
probe.once("error", () => { console.error("Port 46308 is occupied. Stop the conflicting server before starting ach development."); process.exitCode = 1; });
probe.listen(46308, "localhost", () => probe.close(() => {
  const child = spawn(process.platform === "win32" ? "pnpm.cmd" : "pnpm", ["exec", "rsbuild", "dev"], { stdio: "inherit" });
  for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => child.kill(signal));
  child.once("error", (error) => { console.error(error.message); process.exitCode = 1; });
  child.once("exit", (code) => { process.exitCode = code || 0; });
}));
