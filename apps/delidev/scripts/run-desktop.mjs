import { tauriCommand } from "../../../scripts/tauri-cli.mjs";
import { basename, join, resolve } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";
import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import { dryRunEnvironment } from "./bundle-macos-dry-run.mjs";
import { cefCredits, targets } from "./native-package.mjs";

const app = fileURLToPath(new URL("..", import.meta.url));

export function desktopEnvironment(platform, source, home = homedir()) {
  if (platform !== "darwin") return { ...source };
  // The development CLI now bundles and signs. Use the same credential-free
  // system/tool environment as native dry runs so ambient release credentials
  // cannot turn a local launch into signing or notarization with a real identity.
  return {
    ...dryRunEnvironment(source),
    // Tauri changes cwd to src-tauri; resolve a relative Cargo output directory
    // once so preparation and the CLI still share the same artifacts.
    ...(source.CARGO_TARGET_DIR ? { CARGO_TARGET_DIR: resolve(app, source.CARGO_TARGET_DIR) } : {}),
    CEF_PATH: join(home, "Library/Caches/tauri-cef"),
  };
}

export function desktopArguments(platform, args, credits) {
  const cargo = ["run", "--locked", "--manifest-path", "src-tauri/Cargo.toml"];
  if (platform !== "darwin") {
    return [...cargo, "--features", "desktop-host,custom-protocol", "--bin", "delidev-desktop", "--", ...args];
  }
  // Scope: pinned macOS CEF requires Frameworks and helper apps beside the main
  // executable. The pinned CLI's dev path builds that layout before launch.
  // Remove this workaround only when standalone Cargo runs prepare that layout.
  const config = {
    build: { devUrl: null },
    bundle: {
      macOS: { signingIdentity: "-" },
      resources: { [credits]: "notices/Chromium-CREDITS.html" },
    },
  };
  return [
    "dev", "--features", "desktop-host,custom-protocol", "--no-watch", "--no-dev-server",
    "--config", JSON.stringify(config),
    "--", "--locked", "--bin", "delidev-desktop", "--", ...args,
  ];
}

export async function runDesktop(args, {
  platform = process.platform,
  arch = process.arch,
  environment = process.env,
  run = spawnDevServer,
  creditsFor = cefCredits,
  log = entry => process.stderr.write(`${JSON.stringify(entry)}\n`),
} = {}) {
  let stage = "prepare";
  const report = (state, fields = {}) => log({ operation: "desktop_development", stage, state, ...fields });
  try {
    const pnpm = environment.npm_execpath;
    if (!pnpm || !/pnpm\.(?:c?js)$/.test(basename(pnpm))) {
      report("failed", { code: "pnpm-required" });
      return { code: 1, signal: null };
    }
    const env = desktopEnvironment(platform, environment);
    const options = { cwd: app, env, stdio: "inherit", shell: false };
    const lifecycle = { terminateProcessTree: true };
    report("started");
    const prepared = await run(process.execPath, [pnpm, "build:native"], options, lifecycle);
    if (prepared.code !== 0 || prepared.signal !== null) {
      report("failed", prepared);
      return prepared;
    }
    stage = platform === "darwin" ? "bundle-and-run" : "run";
    const selected = targets.find(target => target.platform === platform && target.arch === arch);
    const credits = platform === "darwin" ? creditsFor(selected, env) : undefined;
    // pnpm callers may include one explicit separator. Every remaining token is
    // an application argument, never a Tauri/config/Cargo override.
    const forwarded = args[0] === "--" ? args.slice(1) : args;
    report("started");
    const cliArguments = desktopArguments(platform, forwarded, credits);
    const invocation = platform === "darwin" ? tauriCommand(cliArguments) : ["cargo", cliArguments];
    const result = await run(...invocation, options, lifecycle);
    report(result.code === 0 && result.signal === null ? "exited" : "failed", result);
    return result;
  } catch {
    // Child errors can contain full argv or private filesystem paths.
    report("failed", { code: "launch-failed" });
    return { code: 1, signal: null };
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  exitLikeChild(await runDesktop(process.argv.slice(2)));
}
