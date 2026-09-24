import { spawn } from "node:child_process";
import { mkdir, copyFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import platforms from "../src/native-platforms.json" with { type: "json" };

const host = platforms.find(host => host.platform === process.platform && host.architecture === process.arch);
const glibc = process.platform !== "linux" || !!process.report.getReport().header.glibcVersionRuntime;
if (!host || !glibc || process.versions.node.split(".")[0] !== "24") {
  throw new Error("React Forge requires Node.js 24 on macOS, Windows or glibc Linux, using x64 or arm64.");
}
const root = fileURLToPath(new URL("../../../", import.meta.url));
const target = fileURLToPath(new URL("../../../target/react-forge", import.meta.url));
const release = process.env.REACT_FORGE_NATIVE_PROFILE === "release";
if (process.env.REACT_FORGE_NATIVE_PROFILE && !release) throw new Error("Unsupported React Forge native profile.");
const child = spawn("cargo", ["build", "--locked", ...(release ? ["--release"] : []), "--manifest-path", `${root}/Cargo.toml`, "-p", "react-forge-node", "--target-dir", target, "--target", host.target], { stdio: "inherit" });
// Console events already reach Cargo on Windows. Node's kill API would force
// termination there, bypassing its cleanup; Unix requires explicit forwarding.
const forward = signal => { if (process.platform !== "win32") child.kill(signal); };
const interrupt = () => forward("SIGINT");
const terminate = () => forward("SIGTERM");
process.on("SIGINT", interrupt);
process.on("SIGTERM", terminate);
try {
  const code = await new Promise((resolve, reject) => { child.once("error", reject); child.once("exit", code => resolve(code)); });
  if (code !== 0) throw new Error("React Forge native build failed.");
  const output = new URL("../dist/", import.meta.url);
  await mkdir(output, { recursive: true });
  await copyFile(`${target}/${host.target}/${release ? "release" : "debug"}/${host.library}`, new URL(`react-forge.${host.id}.node`, output));
} finally {
  process.off("SIGINT", interrupt);
  process.off("SIGTERM", terminate);
}
