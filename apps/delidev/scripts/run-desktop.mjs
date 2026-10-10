import { StringDecoder } from "node:string_decoder";
import { tauriCommand } from "../../../scripts/tauri-cli.mjs";
import { basename, join, resolve } from "node:path";
import { homedir } from "node:os";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { exitLikeChild, spawnDevServer } from "../../../scripts/spawn-dev-server.mjs";
import { dryRunEnvironment } from "./bundle-macos-dry-run.mjs";
import { acquireNativeBuildLock, cefCredits, targets } from "./native-package.mjs";
import { DevelopmentSigningError, developmentBundleDirectory, inspectDevelopmentIdentity, publishDevelopmentBundle, readSigningIdentity, signingCommand } from "./development-signing.mjs";

const app = fileURLToPath(new URL("..", import.meta.url));
const root = resolve(app, "../..");

export function desktopEnvironment(platform, source, home = homedir()) {
  if (platform !== "darwin") return { ...source };
  // Only the separately registered local development identity may sign the
  // server. Build children retain the credential-free dry-run environment.
  return {
    ...dryRunEnvironment(source),
    // Tauri changes cwd to src-tauri; resolve a relative Cargo output directory
    // once so preparation and the CLI still share the same artifacts.
    ...(source.CARGO_TARGET_DIR ? { CARGO_TARGET_DIR: resolve(app, source.CARGO_TARGET_DIR) } : {}),
    CEF_PATH: join(home, "Library/Caches/tauri-cef"),
    // The pinned Tauri CLI sets this from the bundle config. Match it during
    // Cargo preparation so each launch does not invalidate macOS dependencies
    // twice. Keep this tied to the config until both builds share one entry.
    MACOSX_DEPLOYMENT_TARGET: JSON.parse(readFileSync(join(app, "src-tauri/tauri.conf.json"), "utf8")).bundle.macOS.minimumSystemVersion,
  };
}

export function desktopArguments(platform, args, credits) {
  const cargo = ["run", "--locked", "--manifest-path", "src-tauri/Cargo.toml"];
  if (platform !== "darwin") {
    return [...cargo, "--features", "desktop-host,custom-protocol", "--bin", "delidev-desktop", "--", ...args];
  }
  // Scope: pinned macOS CEF requires Frameworks and helper apps beside the main
  // executable. Build the layout without launching its mutable output path.
  // Remove this workaround only when standalone Cargo runs prepare that layout.
  const config = {
    build: { devUrl: null },
    bundle: {
      macOS: { signingIdentity: "-" },
      resources: { [credits]: "notices/Chromium-CREDITS.html" },
    },
  };
  return [
    "build", "--debug", "--bundles", "app", "--features", "desktop-host,custom-protocol",
    "--config", JSON.stringify(config),
    "--", "--locked", "--bin", "delidev-desktop",
  ];
}

// Cargo echoes complete application argv. Capture bounded lines and remove
// command echoes before forwarding ordinary build/runtime diagnostics.
export function desktopDiagnostics(args, write) {
  const decoder = new StringDecoder("utf8");
  const values = [...new Set(args.flatMap(arg => [arg, arg.includes("=") ? arg.slice(arg.indexOf("=") + 1) : ""])
    .flatMap(value => [value, JSON.stringify(value).slice(1, -1), ...value.split(/[\r\n]/)])
    .filter(Boolean))].sort((a, b) => b.length - a.length);
  let line = "", dropped = false;
  const emit = () => {
    const plain = line.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "");
    if (dropped || /^\s*(?:Running\s|process didn't exit successfully:)/.test(plain)) write("[desktop command diagnostic omitted]\n");
    else {
      let safe = plain;
      for (const value of values) safe = safe.replaceAll(value, "[redacted]");
      write(`${safe}\n`);
    }
    line = ""; dropped = false;
  };
  const accept = text => {
    const parts = text.split(/\r?\n/);
    for (let index = 0; index < parts.length; index++) {
      if (!dropped && line.length + parts[index].length <= 64 * 1024) line += parts[index];
      else { line = ""; dropped = true; }
      if (index < parts.length - 1) emit();
    }
  };
  return { push: chunk => accept(decoder.write(chunk)), end: () => { accept(decoder.end()); if (line || dropped) emit(); } };
}

export async function runDesktop(args, {
  platform = process.platform,
  arch = process.arch,
  environment = process.env,
  run = spawnDevServer,
  creditsFor = cefCredits,
  identityFor = async (options, command) => inspectDevelopmentIdentity(readSigningIdentity(), options, command),
  publish = publishDevelopmentBundle,
  lock = () => acquireNativeBuildLock(root),
  log = entry => process.stderr.write(`${JSON.stringify(entry)}\n`),
  stdout = text => process.stdout.write(text),
  stderr = text => process.stderr.write(text),
} = {}) {
  let stage = "prepare";
  let release;
  const report = (state, fields = {}) => log({ operation: "desktop_development", stage, state, ...fields });
  try {
    const pnpm = environment.npm_execpath;
    if (!pnpm || !/pnpm\.(?:c?js)$/.test(basename(pnpm))) {
      report("failed", { code: "pnpm-required" });
      return { code: 1, signal: null };
    }
    const env = desktopEnvironment(platform, environment);
    const options = { cwd: app, env, stdio: ["inherit", "pipe", "pipe"], shell: false };
    const capturedRun = async (executable, argv, opts, lifecycle = {}) => {
      const out = desktopDiagnostics(args, stdout), err = desktopDiagnostics(args, stderr);
      try { return await run(executable, argv, opts, { ...lifecycle, onStdout: out.push, onStderr: err.push }); }
      finally { out.end(); err.end(); }
    };
    const lifecycle = { terminateProcessTree: true };
    const command = (executable, argv, opts) => signingCommand(executable, argv, opts, run);
    let identity;
    if (platform === "darwin") {
      stage = "signing-preflight";
      identity = await identityFor(options, command);
      release = lock();
      stage = "prepare";
    }
    report("started");
    const prepared = await capturedRun(process.execPath, [pnpm, "build:native"], options, lifecycle);
    if (prepared.code !== 0 || prepared.signal !== null) {
      report("failed", prepared);
      return prepared;
    }
    stage = platform === "darwin" ? "bundle" : "run";
    const selected = targets.find(target => target.platform === platform && target.arch === arch);
    const credits = platform === "darwin" ? creditsFor(selected, env) : undefined;
    // pnpm callers may include one explicit separator. Every remaining token is
    // an application argument, never a Tauri/config/Cargo override.
    const forwarded = args[0] === "--" ? args.slice(1) : args;
    report("started");
    const cliArguments = desktopArguments(platform, forwarded, credits);
    const invocation = platform === "darwin" ? tauriCommand(cliArguments) : ["cargo", cliArguments];
    let result = await capturedRun(...invocation, options, lifecycle);
    if (platform === "darwin" && result.code === 0 && result.signal === null) {
      stage = "sign-and-publish";
      report("started");
      const output = env.CARGO_TARGET_DIR ?? join(root, "target");
      const executable = await publish(join(output, "debug/bundle/macos/DeliDev.app"), developmentBundleDirectory(), identity, options, command);
      release();
      release = undefined;
      stage = "run";
      report("started");
      // An interrupted launcher may leave its Go server alive. Signal only the
      // original desktop child; never group-kill its crash-surviving sidecar.
      result = await capturedRun(executable, forwarded, { ...options, detached: true }, { terminateProcessTree: false });
    }
    report(result.code === 0 && result.signal === null ? "exited" : "failed", result);
    return result;
  } catch (error) {
    // Child errors can contain full argv or private filesystem paths.
    report("failed", { code: error instanceof DevelopmentSigningError ? error.code : "launch-failed" });
    if (error instanceof DevelopmentSigningError && error.code === "development-signing-not-configured") {
      process.stderr.write('Create a "DeliDev Local Development" self-signed Code Signing certificate with Keychain Access > Certificate Assistant, then run pnpm dev:signing --identity <certificate SHA-1 fingerprint>.\n');
    }
    return { code: 1, signal: null };
  } finally {
    release?.();
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  exitLikeChild(await runDesktop(process.argv.slice(2)));
}
