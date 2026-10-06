import { spawn } from "node:child_process";
import { globSync } from "node:fs";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../../", import.meta.url));
const [command, ...patterns] = process.argv.slice(2);
if (!command) throw new Error("Expected a command");
const args = patterns.flatMap((arg) => arg.includes("*") ? globSync(arg, { cwd: root }).sort() : [arg]);
console.log(JSON.stringify({ event: "ci_task_start", command, args }));
const child = spawn(command === "node" ? process.execPath : command, args, { cwd: root, stdio: "inherit", shell: false });
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => child.kill(signal));
child.on("error", (error) => { console.error(error); process.exitCode = 1; });
child.on("exit", (code, signal) => {
  console.log(JSON.stringify({ event: "ci_task_exit", command, code, signal }));
  process.exitCode = code ?? 1;
});
