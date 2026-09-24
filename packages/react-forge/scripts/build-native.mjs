import { spawn } from "node:child_process";
import { mkdir, copyFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

if (process.platform !== "darwin" || process.arch !== "arm64" || process.versions.node.split(".")[0] !== "24") {
  throw new Error("React Forge requires Node.js 24 on macOS arm64.");
}
const root = fileURLToPath(new URL("../../../", import.meta.url));
const target = fileURLToPath(new URL("../../../target/react-forge", import.meta.url));
const child = spawn("cargo", ["build", "--manifest-path", `${root}/Cargo.toml`, "-p", "react-forge-node", "--target-dir", target], { stdio: "inherit" });
const forward = signal => child.kill(signal);
const interrupt = () => forward("SIGINT");
const terminate = () => forward("SIGTERM");
process.on("SIGINT", interrupt);
process.on("SIGTERM", terminate);
try {
  const code = await new Promise((resolve, reject) => { child.once("error", reject); child.once("exit", code => resolve(code)); });
  if (code !== 0) throw new Error("React Forge native build failed.");
  const output = new URL("../dist/", import.meta.url);
  await mkdir(output, { recursive: true });
  await copyFile(`${target}/debug/libreact_forge_node.dylib`, new URL("react-forge.node", output));
} finally {
  process.off("SIGINT", interrupt);
  process.off("SIGTERM", terminate);
}
