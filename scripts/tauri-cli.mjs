import { appendFile, realpath } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { dependencyPaths, prepareDependency } from "./prebuilt-dependencies.mjs";
import { exitLikeChild, spawnDevServer } from "./spawn-dev-server.mjs";

const script = fileURLToPath(import.meta.url);
export function cliPaths(sourceRoot, platform = process.platform, arch = process.arch) {
  return dependencyPaths(sourceRoot, "tauri-cli", { platform, arch });
}
export function prepareCli(sourceRoot, options = {}) {
  return prepareDependency(sourceRoot, "tauri-cli", options);
}
// Keep argv structured even for synchronous package builders. The wrapper
// validates the host binary before delegating its original Tauri arguments.
export function tauriCommand(args) {
  return [process.execPath, [script, "--", ...args]];
}

if (process.argv[1] && (await realpath(process.argv[1]).catch(() => undefined)) === script) {
  try {
    const args = process.argv.slice(2);
    const separator = args.indexOf("--");
    const flags = separator === -1 ? args : args.slice(0, separator);
    const forwarded = separator === -1 ? [] : args.slice(separator + 1);
    if (flags.some(flag => !["--source", "--print-path"].includes(flag)) || new Set(flags).size !== flags.length || (separator !== -1 && !forwarded.length)) throw new Error("Expected optional --source/--print-path, then -- and Tauri arguments.");
    const root = resolve(process.env.TAURI_CLI_SOURCE_ROOT ?? resolve(dirname(script), ".."));
    const result = await prepareCli(root, { source: flags.includes("--source") });
    if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `installed=${result.installed}\n`);
    if (flags.includes("--print-path")) process.stdout.write(result.executable + "\n");
    if (forwarded.length) {
      exitLikeChild(await spawnDevServer(result.executable, ["tauri", ...forwarded], { cwd: process.cwd(), env: process.env, stdio: "inherit", shell: false }, { terminateProcessTree: true }));
    }
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
