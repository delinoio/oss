// SPDX-License-Identifier: Apache-2.0
import {
  readFileSync,
  writeFileSync,
  mkdirSync,
  existsSync,
  copyFileSync,
} from "node:fs";
import { resolve, join, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";
import {
  candidate,
  retainCandidate,
  validateInputs,
  credentials,
  distribute,
  Stage,
  Identity,
} from "./beta.mjs";
import { inspectIos, inspectAndroid } from "./artifacts.mjs";
import { appleProvider, googleProvider } from "./providers.mjs";
const app = resolve(dirname(fileURLToPath(import.meta.url)), ".."),
  root = resolve(app, "../.."),
  output = resolve(
    process.env.DELIDEV_MOBILE_CANDIDATE_DIR ?? join(app, "artifacts"),
  );
function run(program, args, cwd = app, env = {}) {
  const r = spawnSync(program, args, {
    cwd,
    env: { ...process.env, ...env },
    stdio: "inherit",
    shell: false,
  });
  if (r.error || r.status !== 0)
    throw new Error("Mobile candidate build failed");
}
function capture(program, args) {
  const r = spawnSync(program, args, {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 4 << 20,
  });
  if (r.error || r.status !== 0)
    throw new Error("Mobile signing configuration failed");
  return r.stdout.trim();
}
export function inputs(e) {
  return validateInputs({
    identity: Identity,
    sourceSha: e.DELIDEV_MOBILE_SOURCE_SHA,
    version: e.DELIDEV_MOBILE_VERSION,
    iosBuild: e.DELIDEV_MOBILE_IOS_BUILD,
    androidCode: e.DELIDEV_MOBILE_ANDROID_CODE,
    appleGroupType: "INTERNAL",
    playTrack: "internal",
    appleSigner: e.DELIDEV_MOBILE_APPLE_SIGNER,
    androidSigner: e.DELIDEV_MOBILE_ANDROID_SIGNER,
  });
}
export function source(input) {
  if (capture("git", ["rev-parse", "HEAD"]) !== input.sourceSha)
    throw new Error("Candidate source revision mismatch");
  if (capture("git", ["status", "--porcelain", "--untracked-files=no"]))
    throw new Error("Candidate source has tracked changes");
}
function config(input) {
  const file = join(app, "src-tauri/tauri.conf.json"),
    original = readFileSync(file, "utf8"),
    value = JSON.parse(original);
  value.version = input.version;
  value.bundle.iOS.bundleVersion = input.iosBuild;
  value.bundle.android.versionCode = Number(input.androidCode);
  writeFileSync(file, JSON.stringify(value, null, 2) + "\n");
  return () => writeFileSync(file, original);
}
async function build(input, platform) {
  source(input);
  const restore = config(input);
  mkdirSync(output, { recursive: true });
  try {
    if (platform === "android") {
      for (const key of [
        "TAURI_ANDROID_KEYSTORE_PATH",
        "TAURI_ANDROID_KEYSTORE_PASSWORD",
        "TAURI_ANDROID_KEY_ALIAS",
        "TAURI_ANDROID_KEY_PASSWORD",
      ])
        if (!process.env[key])
          throw new Error("Android signing input is missing");
      run("pnpm", ["build:android"]);
      const path = join(
        app,
        "src-tauri/gen/android/app/build/outputs/bundle/universalRelease/app-universal-release.aab",
      );
      const artifact = inspectAndroid(
        path,
        input,
        process.env.DELIDEV_MOBILE_BUNDLETOOL,
      );
      copyFileSync(path, join(output, "DeliDev.aab"));
      writeFileSync(
        join(output, "android.json"),
        JSON.stringify({ ...artifact, bytes: undefined }) + "\n",
        { flag: "wx" },
      );
    } else if (platform === "ios") {
      if (
        !process.env.DELIDEV_MOBILE_APPLE_TEAM ||
        !process.env.DELIDEV_MOBILE_IOS_PROFILE_UUID
      )
        throw new Error("iOS signing input is missing");
      run("node", ["scripts/mobile.mjs", "generate-ios"]);
      run("pnpm", ["build"]);
      run(
        "cargo",
        [
          "build",
          "-p",
          "delidev-mobile",
          "--release",
          "--target",
          "aarch64-apple-ios",
          "--features",
          "custom-protocol",
        ],
        root,
      );
      const target = resolve(root, process.env.CARGO_TARGET_DIR ?? "target"),
        external = join(app, "src-tauri/gen/apple/Externals/arm64/release");
      mkdirSync(external, { recursive: true });
      copyFileSync(
        join(target, "aarch64-apple-ios/release/libdelidev_mobile.a"),
        join(external, "libapp.a"),
      );
      run("ditto", [
        join(app, "dist"),
        join(app, "src-tauri/gen/apple/assets"),
      ]);
      const archive = join(output, "DeliDev.xcarchive");
      run("xcodebuild", [
        "-project",
        "src-tauri/gen/apple/delidev-mobile.xcodeproj",
        "-scheme",
        "delidev-mobile_iOS",
        "-configuration",
        "release",
        "-sdk",
        "iphoneos",
        "-destination",
        "generic/platform=iOS",
        "-archivePath",
        archive,
        "ARCHS=arm64",
        "DELIDEV_MOBILE_PREBUILT_RUST=1",
        `DEVELOPMENT_TEAM=${process.env.DELIDEV_MOBILE_APPLE_TEAM}`,
        "CODE_SIGN_STYLE=Manual",
        `PROVISIONING_PROFILE_SPECIFIER=${process.env.DELIDEV_MOBILE_IOS_PROFILE_UUID}`,
        "CODE_SIGN_IDENTITY=Apple Distribution",
        "archive",
      ]);
      const options = join(output, "ExportOptions.plist"),
        team = process.env.DELIDEV_MOBILE_APPLE_TEAM,
        profile = process.env.DELIDEV_MOBILE_IOS_PROFILE_UUID;
      if (!/^[A-Z0-9]{10}$/.test(team) || !/^[A-Fa-f0-9-]{36}$/.test(profile))
        throw new Error("Invalid iOS signing identity");
      writeFileSync(
        options,
        `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd"><plist version="1.0"><dict><key>method</key><string>app-store-connect</string><key>destination</key><string>export</string><key>teamID</key><string>${team}</string><key>signingStyle</key><string>manual</string><key>testFlightInternalTestingOnly</key><true/><key>uploadSymbols</key><false/><key>provisioningProfiles</key><dict><key>${Identity}</key><string>${profile}</string></dict></dict></plist>`,
        { mode: 0o600 },
      );
      run("xcodebuild", [
        "-exportArchive",
        "-archivePath",
        archive,
        "-exportPath",
        join(output, "export"),
        "-exportOptionsPlist",
        options,
      ]);
      const path = join(output, "export/DeliDev.ipa"),
        artifact = inspectIos(path, input);
      copyFileSync(path, join(output, "DeliDev.ipa"));
      writeFileSync(
        join(output, "ios.json"),
        JSON.stringify({ ...artifact, bytes: undefined }) + "\n",
        { flag: "wx" },
      );
    } else throw new Error("Unknown mobile platform");
  } finally {
    restore();
  }
}
export function assemble(input, directory) {
  const artifact = (platform, name) => ({
    ...JSON.parse(readFileSync(join(directory, `${platform}.json`))),
    bytes: readFileSync(join(directory, name)),
  });
  const manifest = candidate(
    input,
    artifact("ios", "DeliDev.ipa"),
    artifact("android", "DeliDev.aab"),
  );
  retainCandidate(join(directory, "manifest.json"), manifest);
  return manifest;
}
async function submit(input, resume) {
  credentials(process.env);
  source(input);
  const manifest = JSON.parse(readFileSync(join(output, "manifest.json"))),
    actual = assemble(input, output);
  if (actual.candidateId !== manifest.candidateId)
    throw new Error("Original candidate changed");
  for (const platform of ["ios", "android"]) {
    const file = join(output, `${platform}-receipt.json`),
      receipt = existsSync(file)
        ? JSON.parse(readFileSync(file))
        : {
            candidateId: manifest.candidateId,
            platform,
            stage: resume ? Stage.Unknown : Stage.Ready,
          };
    if (
      receipt.candidateId !== manifest.candidateId ||
      receipt.platform !== platform
    )
      throw new Error("Original receipt identity mismatch");
    const checkpoint = async (value) => {
      const temporary = file + ".tmp";
      writeFileSync(temporary, JSON.stringify(value) + "\n", { mode: 0o600 });
      run(
        "node",
        [
          "-e",
          "require('node:fs').renameSync(process.argv[1],process.argv[2])",
          temporary,
          file,
        ],
        root,
      );
    };
    const bytes = readFileSync(
        join(output, platform === "ios" ? "DeliDev.ipa" : "DeliDev.aab"),
      ),
      provider =
        platform === "ios"
          ? appleProvider(process.env, bytes, checkpoint)
          : googleProvider(process.env, bytes, checkpoint);
    // All artifact hashes and retained source metadata are checked again before
    // this explicit upload boundary. Submission never rebuilds candidate bytes.
    await distribute(manifest, receipt, provider, checkpoint, {
      dryRun: false,
    });
  }
}
if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
)
  try {
    const command = process.argv[2],
      input = inputs(process.env);
    source(input);
    switch (command) {
      case "plan":
        process.stdout.write(
          JSON.stringify({
            operation: "mobile-beta-plan",
            ...input,
            dryRun: true,
          }) + "\n",
        );
        break;
      case "build":
        await build(input, process.argv[3]);
        break;
      case "assemble":
        assemble(input, output);
        break;
      case "submit":
        await submit(input, false);
        break;
      case "resume":
        await submit(input, true);
        break;
      default:
        throw new Error("Unknown mobile beta command");
    }
  } catch {
    process.stderr.write(
      "Mobile beta operation failed. Preserve the original candidate and receipts; inspect before explicit resume.\n",
    );
    process.exitCode = 1;
  }
