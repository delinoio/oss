import { appendFile, realpath } from "node:fs/promises";
import { dirname, posix, resolve, win32 } from "node:path";
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

// Generated Xcode and Gradle steps invoke cargo-tauri through Cargo. Keep
// their child environment bound to the same verified executable as the caller.
export function tauriEnvironment(executable, environment = process.env, platform = process.platform) {
  const windows = platform === "win32";
  const keys = Object.keys(environment).filter(key => windows ? key.toLowerCase() === "path" : key === "PATH");
  const key = keys[0] ?? (windows ? "Path" : "PATH");
  const result = { ...environment };
  for (const existing of keys) delete result[existing];
  const inherited = environment[key];
  result[key] = (windows ? win32 : posix).dirname(executable) + (inherited ? `${windows ? ";" : ":"}${inherited}` : "");
  return result;
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
      exitLikeChild(await spawnDevServer(result.executable, ["tauri", ...forwarded], { cwd: process.cwd(), env: tauriEnvironment(result.executable), stdio: "inherit", shell: false }, { terminateProcessTree: true }));
    }
  } catch (error) {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  }
}
