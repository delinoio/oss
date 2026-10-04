import { execFileSync, spawnSync } from "node:child_process";
import { accessSync, constants, lstatSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { resolve, join } from "node:path";
import { targets, prepareCefCredits, packageResources, verifyNotices } from "./native-package.mjs";
import { prepareAssets } from "./prepare-assets.mjs";
import { exitLikeChild } from "../../../scripts/spawn-dev-server.mjs";

// Dry runs must not discover imported certificates, notarization credentials,
// updater keys or executable injection flags through the caller's environment.
// Real release signing needs its own explicit, separately validated workflow.
const environmentNames = ["PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL", "CARGO_HOME", "RUSTUP_HOME", "SDKROOT", "DEVELOPER_DIR", "MACOSX_DEPLOYMENT_TARGET"];
export function dryRunEnvironment(source) {
  return Object.fromEntries(environmentNames.filter(name => typeof source[name] === "string").map(name => [name, source[name]]));
}

export function verifyBundle(bundle, run, nativeArch) {
  const executable = (path) => {
    if (!lstatSync(path).isFile()) throw new Error("A required native executable is not a regular file.");
    accessSync(path, constants.X_OK);
    if (run("lipo", ["-archs", path]).trim() !== nativeArch) throw new Error("A native executable has the wrong architecture.");
  };
  for (const name of ["delidev-desktop", "delidev"]) executable(join(bundle, "Contents/MacOS", name));
  const frameworks = join(bundle, "Contents/Frameworks");
  executable(join(frameworks, "Chromium Embedded Framework.framework/Chromium Embedded Framework"));
  for (const resource of ["icudtl.dat", "resources.pak", "chrome_100_percent.pak", "chrome_200_percent.pak"]) {
    accessSync(join(frameworks, "Chromium Embedded Framework.framework/Resources", resource), constants.R_OK);
  }
  for (const suffix of ["", " (Renderer)", " (GPU)", " (Plugin)", " (Alerts)"]) {
    const name = `delidev-desktop Helper${suffix}`;
    executable(join(frameworks, `${name}.app/Contents/MacOS`, name));
  }
  const plist = join(bundle, "Contents/Info.plist");
  if (run("/usr/libexec/PlistBuddy", ["-c", "Print :CFBundleIdentifier", plist]).trim() !== "io.delino.delidev") throw new Error("The bundle identifier changed.");
  if (run("/usr/libexec/PlistBuddy", ["-c", "Print :LSMinimumSystemVersion", plist]).trim() !== "13.0") throw new Error("The bundle no longer targets macOS 13.");
  verifyWidgetBundle(bundle, run, nativeArch);
  run("codesign", ["--verify", "--deep", "--strict", bundle]);
  // codesign writes display metadata to stderr. The wrapper returns both streams;
  // its caller never publishes identities from a production signing environment.
  if (!/^Signature=adhoc$/m.test(run("codesign", ["--display", "--verbose=4", bundle]))) throw new Error("The dry-run bundle is not explicitly ad-hoc signed.");
}

export function verifyWidgetBundle(bundle, run, nativeArch) {
  const checkEntitlements = (path, sandboxed) => {
    // Newer codesign prints human-readable dictionaries for '-'. Keep the
    // explicit XML form across macOS 13+ toolchains until a replacement offers
    // one structured output format on every supported verification host.
    const value = run("codesign", ["--display", "--entitlements", ":-", path]);
    const keys = [...value.matchAll(/<key>([^<]+)<\/key>/g)].map(match => match[1]).sort();
    const cef = ["com.apple.security.cs.allow-jit", "com.apple.security.cs.allow-unsigned-executable-memory", "com.apple.security.cs.disable-library-validation"];
    const expected = ["com.apple.security.application-groups", ...(sandboxed ? ["com.apple.security.app-sandbox"] : cef)].sort();
    if (JSON.stringify(keys) !== JSON.stringify(expected)
      || !/<key>com\.apple\.security\.application-groups<\/key>\s*<array>\s*<string>group\.io\.delino\.delidev<\/string>\s*<\/array>/.test(value)
      || (sandboxed && !/<key>com\.apple\.security\.app-sandbox<\/key>\s*<true\s*\/>/.test(value))
      || (!sandboxed && cef.some(key => !value.includes(`<key>${key}</key><true/>`)))) throw new Error("The widget App Group or sandbox entitlements changed.");
  };
  checkEntitlements(bundle, false);
  for (const [name, id, point] of [
    ["DeliDevWidget", "io.delino.delidev.widget", "com.apple.widgetkit-extension"],
    ["DeliDevWidgetSelection", "io.delino.delidev.widget.selection", "com.apple.intents-service"],
  ]) {
    const extension = join(bundle, "Contents/PlugIns", `${name}.appex`);
    const binary = join(extension, "Contents/MacOS", name);
    if (!lstatSync(binary).isFile()) throw new Error("A widget executable is not a regular file.");
    accessSync(binary, constants.X_OK);
    if (run("lipo", ["-archs", binary]).trim() !== nativeArch) throw new Error("A widget executable has the wrong architecture.");
    const plist = join(extension, "Contents/Info.plist");
    for (const [key, expected] of [["CFBundleIdentifier", id], ["LSMinimumSystemVersion", "13.0"], ["NSExtension:NSExtensionPointIdentifier", point]]) {
      if (run("/usr/libexec/PlistBuddy", ["-c", `Print :${key}`, plist]).trim() !== expected) throw new Error("A widget bundle identifier, extension point or minimum OS changed.");
    }
    run("codesign", ["--verify", "--strict", extension]);
    checkEntitlements(extension, true);
  }
}

async function main() {
  if (process.platform !== "darwin" || !["arm64", "x64"].includes(process.arch) || process.argv.length !== 2) throw new Error("Run this macOS-only dry run on a native x64 or arm64 host without extra arguments.");
  const app = fileURLToPath(new URL("..", import.meta.url));
  const root = resolve(app, "../..");
  const env = dryRunEnvironment(process.env);
  const prepared = await prepareAssets({ root, environment: env });
  if (prepared.code !== 0 || prepared.signal !== null) return exitLikeChild(prepared);
  const build = (command, args) => execFileSync(command, args, { cwd: app, env, stdio: "inherit" });
  build("pnpm", ["--filter", "@delinoio/delidev-api-client", "build"]);
  build("pnpm", ["build"]);
  build("pnpm", ["prepare:sidecar"]);
  build("pnpm", ["prepare:widget"]);
  const selected = targets.find(item => item.platform === process.platform && item.arch === process.arch);
  const credits = prepareCefCredits(selected, env, build);
  const resources = packageResources(app, root, credits);
  const dryRunConfig = JSON.parse(readFileSync(join(app, "src-tauri/tauri.dry-run.conf.json"), "utf8"));
  const config = JSON.stringify({ ...dryRunConfig, bundle: { ...dryRunConfig.bundle, resources: { [credits]: "notices/Chromium-CREDITS.html" } } });
  build("cargo", ["run", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli", "--bin", "delidev-tauri-cli", "--", "build", "--bundles", "app", "--features", "desktop-host,custom-protocol,tauri/cef", "--config", config]);
  const bundle = join(root, "target/release/bundle/macos/DeliDev.app");
  const run = (command, args) => {
    const result = spawnSync(command, args, { cwd: app, env, encoding: "utf8" });
    if (result.error || result.status !== 0) throw new Error(`Bundle verification failed: ${command}.`);
    return result.stdout + result.stderr;
  };
  verifyBundle(bundle, run, process.arch === "arm64" ? "arm64" : "x86_64");
  verifyNotices(join(bundle, "Contents/Resources"), resources);
  process.stdout.write("DeliDev macOS dry run passed: native sidecar, CEF resources, macOS 13 metadata and ad-hoc signature verified. No publication or notarization occurred.\n");
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main();
