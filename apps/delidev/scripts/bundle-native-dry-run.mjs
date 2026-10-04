import { spawnSync } from "node:child_process";
import { copyFileSync, createReadStream, existsSync, mkdirSync, mkdtempSync, readdirSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { basename, dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { tmpdir } from "node:os";
import { dryRunEnvironment, verifyBundle } from "./bundle-macos-dry-run.mjs";
import { targets, selectTarget, acquireNativeBuildLock, verifyPackageRevision, cefCredits, prepareCefCredits, cefResourcePath, packageResources, verifyNotices, verifyNativePayload, findOneFile } from "./native-package.mjs";
import { prepareAssets } from "./prepare-assets.mjs";
import { exitLikeChild } from "../../../scripts/spawn-dev-server.mjs";

// Windows needs the native MSVC/SDK and system lookup context. None of these
// names carries product/signing credentials or arbitrary compiler/linker flags.
const windowsNames = ["SystemRoot", "WINDIR", "COMSPEC", "PATHEXT", "USERPROFILE", "APPDATA", "LOCALAPPDATA", "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432", "INCLUDE", "LIB", "LIBPATH", "VCINSTALLDIR", "VCToolsInstallDir", "VCToolsVersion", "VisualStudioVersion", "VSINSTALLDIR", "WindowsSdkDir", "WindowsSDKVersion", "WindowsSDKLibVersion", "UniversalCRTSdkDir", "UCRTVersion"];
export function nativeEnvironment(source, platform) {
  const env = dryRunEnvironment(source);
  if (platform === "win32") {
    for (const [name, value] of Object.entries(source)) {
      const canonical = windowsNames.find(item => item.toLowerCase() === name.toLowerCase());
      if (canonical && typeof value === "string") env[canonical] = value;
      if (name.toLowerCase() === "path" && typeof value === "string") env.PATH = value;
    }
  }
  env.CI = "true";
  return env;
}

async function sha256(path) {
  const hash = createHash("sha256");
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest("hex");
}

async function main() {
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === "--plan") {
    process.stdout.write(JSON.stringify({ include: targets }) + "\n");
    return;
  }
  if (args.length !== 2 || args[0] !== "--target") throw new Error("Use --plan or --target with an exact supported native Rust triple.");
  const selected = selectTarget(args[1], process.platform, process.arch);
  const app = fileURLToPath(new URL("..", import.meta.url));
  const root = resolve(app, "../..");
  const env = nativeEnvironment(process.env, process.platform);
  const run = (command, arguments_, inherit = false) => {
    const result = spawnSync(command, arguments_, { cwd: app, env, encoding: "utf8", ...(inherit ? { stdio: "inherit" } : {}) });
    if (result.error || result.status !== 0) throw new Error(`Native dry-run command failed: ${command}.`);
    return inherit ? "" : command === "git" ? result.stdout : result.stdout + result.stderr;
  };
  if (run("rustc", ["--print", "host-tuple"]).trim() !== selected.target) throw new Error("The Rust toolchain does not match the native target.");
  const revision = run("git", ["rev-parse", "HEAD"]).trim();
  if (!/^[a-f0-9]{40}$/.test(revision)) throw new Error("Source revision is unavailable.");
  if (run("git", ["status", "--porcelain", "--untracked-files=normal"]).trim()) throw new Error("Commit the complete source before producing revision-bound dry-run artifacts.");
  const prepared = await prepareAssets({ root, environment: env });
  if (prepared.code !== 0 || prepared.signal !== null) return exitLikeChild(prepared);
  const output = join(root, "target/delidev-dry-run", selected.target, revision);
  // A previous result is never silently replaced with different package bytes.
  mkdirSync(dirname(output), { recursive: true });
  if (existsSync(output)) throw new Error("This revision already has retained dry-run artifacts; do not replace them implicitly.");
  const release = acquireNativeBuildLock(root);
  let staging;
  let scratch;
  try {
    staging = mkdtempSync(join(dirname(output), ".pending-"));
    scratch = mkdtempSync(join(tmpdir(), "delidev-package-"));
    let artifact;
    let signature;
    let resources;
    if (selected.platform === "darwin") {
      run(process.execPath, [join(app, "scripts/bundle-macos-dry-run.mjs")], true);
      resources = packageResources(app, root, cefCredits(selected, env));
      const bundle = join(root, "target/release/bundle/macos/DeliDev.app");
      verifyBundle(bundle, run, selected.arch === "arm64" ? "arm64" : "x86_64");
      verifyNotices(join(bundle, "Contents/Resources"), resources);
      artifact = join(staging, "DeliDev.app.tar.gz");
      run("tar", ["-czf", artifact, "-C", dirname(bundle), "DeliDev.app"]);
      signature = "adhoc-verified";
    } else {
      // npm_execpath is the explicitly invoked pnpm tool, not inherited child
      // authority. Running its JavaScript entry avoids cmd.exe string parsing.
      const pnpm = process.env.npm_execpath;
      if (!pnpm || !/pnpm\.(?:c?js)$/.test(basename(pnpm))) throw new Error("Invoke this command through pnpm.");
      run(process.execPath, [pnpm, "--filter", "@delinoio/delidev-api-client", "build"], true);
      run(process.execPath, [pnpm, "build"], true);
      run(process.execPath, [join(app, "scripts/prepare-sidecar.mjs"), selected.target], true);
      const credits = prepareCefCredits(selected, env, (command, arguments_) => run(command, arguments_, true));
      resources = packageResources(app, root, credits);
      const kind = selected.platform === "win32" ? "msi" : "deb";
      const config = JSON.stringify({ bundle: { resources: { [cefResourcePath(app, root, selected, credits)]: "notices/Chromium-CREDITS.html" } } });
      const started = Date.now();
      run("cargo", ["run", "--locked", "--manifest-path", "src-tauri/Cargo.toml", "--features", "cli", "--bin", "delidev-tauri-cli", "--", "build", "--target", selected.target, "--bundles", kind, "--features", "desktop-host,custom-protocol,tauri/cef", "--config", config], true);
      const directory = join(root, "target", selected.target, "release/bundle", kind);
      const packages = readdirSync(directory).filter(name => name.endsWith(`.${kind}`) && statSync(join(directory, name)).mtimeMs >= started - 2000);
      if (packages.length !== 1) throw new Error("Expected one newly built native package.");
      artifact = join(staging, packages[0]);
      copyFileSync(join(directory, packages[0]), artifact);
      if (selected.platform === "win32") {
        run("msiexec.exe", ["/a", artifact, "/qn", `TARGETDIR=${scratch}`]);
        const payload = dirname(findOneFile(scratch, "delidev-desktop.exe"));
        verifyNativePayload(payload, selected);
        verifyNotices(payload, resources);
        run("pwsh", ["-NoProfile", "-NonInteractive", "-File", join(app, "scripts/verify-unsigned-windows.ps1"), "-Directory", payload, "-Installer", artifact]);
        signature = "unsigned-verified";
      } else {
        run("dpkg-deb", ["--extract", artifact, scratch]);
        verifyNativePayload(scratch, selected);
        verifyNotices(join(scratch, "usr/lib/DeliDev"), resources);
        signature = "unsigned";
      }
    }
    const digest = await sha256(artifact);
    verifyPackageRevision(revision, run("git", ["rev-parse", "HEAD"]), run("git", ["status", "--porcelain", "--untracked-files=normal"]));
    const report = { version: 1, sourceRevision: revision, target: selected.target, artifact: basename(artifact), bytes: statSync(artifact).size, sha256: digest, signature, cefVersion: "150.0.10", chromiumCreditsSHA256: await sha256(Object.keys(resources).find(path => resources[path] === "notices/Chromium-CREDITS.html")), runtimeAcceptance: "unverified", publication: "not-requested" };
    writeFileSync(join(staging, "verification.json"), JSON.stringify(report, null, 2) + "\n", { flag: "wx" });
    writeFileSync(join(staging, "SHA256SUMS"), `${digest}  ${basename(artifact)}\n`, { flag: "wx" });
    renameSync(staging, output);
    process.stdout.write(`DeliDev native dry run verified ${selected.target}. Runtime acceptance and production signing remain separate.\n`);
  } finally {
    try {
      if (scratch) rmSync(scratch, { recursive: true, force: true });
    } finally {
      try { if (staging) rmSync(staging, { recursive: true, force: true }); }
      finally { release(); }
    }
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) await main();
