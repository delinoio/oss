import { fork } from "node:child_process";

// The worker owns cleanup. IPC disconnect remains observable when Windows
// forcibly terminates this launcher without delivering a catchable signal.
const worker = fork(new URL("native-react-worker.mjs", import.meta.url), [], {
  stdio: ["ignore", "inherit", "inherit", "ipc"],
});
const interrupt = () => {
  if (worker.connected) worker.send({ type: "abort" }, () => {});
};
process.on("SIGINT", interrupt);
process.on("SIGTERM", interrupt);
worker.on("error", () => {
  process.exitCode = 1;
});
worker.on("exit", (code) => {
  process.removeListener("SIGINT", interrupt);
  process.removeListener("SIGTERM", interrupt);
  process.exitCode = code ?? 1;
});
