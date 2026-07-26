import { readFile, mkdir, writeFile } from "node:fs/promises";
import { resolve } from "node:path";
import { spawnSync } from "node:child_process";

import { run, runPackageManager } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const mobileCli = resolve(
  appRoot,
  "node_modules/@tauri-apps/cli-mobile/tauri.js",
);
const generationPolicy = resolve(
  appRoot,
  "scripts/enforce-mobile-generation.mjs",
);
const assetGenerator = resolve(appRoot, "scripts/generate-assets.mjs");
const assetCheck = resolve(appRoot, "scripts/check-assets.mjs");
const contractCheck = resolve(appRoot, "scripts/check-mobile-contracts.mjs");
const node = process.execPath;
const [operation, platform, targetSet = "production"] = process.argv.slice(2);

const platformNames = new Set(["android", "ios"]);
const operations = new Set(["build", "generate"]);
const targetSets = new Set(["production", "ci", "artifact"]);

async function snapshotTrackedFiles(directory) {
  const files = spawnSync("git", ["ls-files", "-z", "--", directory], {
    cwd: appRoot,
  });
  if (files.status !== 0) {
    throw new Error(`could not snapshot tracked generated files in ${directory}`);
  }
  const snapshot = await Promise.all(
    files.stdout
      .toString("utf8")
      .split("\0")
      .filter(Boolean)
      .map(async (file) => {
        const path = resolve(appRoot, file);
        return [path, await readFile(path)];
      }),
  );
  return () => Promise.all(snapshot.map(([path, source]) => writeFile(path, source)));
}

async function stampBuildRevision(platform) {
  const revision = spawnSync("git", ["rev-parse", "HEAD"], { cwd: appRoot, encoding: "utf8" });
  const worktree = spawnSync("git", ["status", "--porcelain", "--untracked-files=all"], { cwd: appRoot, encoding: "utf8" });
  // Local development builds remain usable with uncommitted changes. Their
  // explicit unverified stamp prevents perf:mobile from publishing evidence.
  const buildRevision = revision.status === 0 && /^[0-9a-f]{40}\n$/u.test(revision.stdout) && worktree.status === 0 && !worktree.stdout.trim()
    ? revision.stdout.trim()
    : "unverified";
  if (platform === "android") {
    const path = resolve(appRoot, "src-tauri/gen/android/app/build.gradle.kts");
    const source = await readFile(path, "utf8");
    await writeFile(path, source.replace(/^\s*versionName = [^\n]+$/mu, `        versionName = "0.1.0+f49ebda2fdba5755456b0f049e32593ca0ea331a+${buildRevision}"`));
    return () => writeFile(path, source);
  }
  const restoreGeneratedFiles = await snapshotTrackedFiles("src-tauri/gen/apple");
  const path = resolve(appRoot, "src-tauri/gen/apple/project.yml");
  const source = await readFile(path, "utf8");
  const updated = source.includes("DevHudBuildRevision")
    ? source.replace(/(\s+DevHudBuildRevision:\s*)[^\r\n]*/u, `$1${buildRevision}`)
    : source.replace("        DevHudTauriRevision: f49ebda2fdba5755456b0f049e32593ca0ea331a", `        DevHudTauriRevision: f49ebda2fdba5755456b0f049e32593ca0ea331a\n        DevHudBuildRevision: ${buildRevision}`);
  await writeFile(path, updated);
  return restoreGeneratedFiles;
}

if (
  !operations.has(operation) ||
  !platformNames.has(platform) ||
  !targetSets.has(targetSet)
) {
  throw new Error(
    "Usage: node scripts/mobile.mjs <generate|build> <android|ios> [production|ci|artifact]",
  );
}

if (platform === "ios" && process.platform !== "darwin") {
  throw new Error(
    "The standard Tauri iOS generator and Xcode build require a macOS host.",
  );
}

if (operation === "generate") {
  await run(
    node,
    [mobileCli, platform, "init", "--ci", "--skip-targets-install"],
    { cwd: appRoot },
  );
  await run(node, [generationPolicy, platform], { cwd: appRoot });
  await run(node, [assetGenerator], { cwd: appRoot });
  await run(node, [assetCheck], { cwd: appRoot });
  await run(node, [contractCheck], { cwd: appRoot });
  process.exit(0);
}

const restoreBuildTemplate = await stampBuildRevision(platform);

try {
await runPackageManager(["run", "build"], { cwd: appRoot });
if (platform === "android") {
  const targets =
    targetSet === "production" ? ["aarch64", "armv7"] : ["x86_64"];
  await run(
    node,
    [
      mobileCli,
      "android",
      "build",
      "--ci",
      "--apk",
      "--split-per-abi",
      ...(targetSet === "ci" ? ["--debug"] : []),
      "--features",
      "mobile-system-webview,custom-protocol",
      "--target",
      ...targets,
    ],
    { cwd: appRoot },
  );
} else {
  const simulatorTarget =
    process.arch === "arm64" ? "aarch64-sim" : "x86_64";
  const targets =
    targetSet === "production" ? ["aarch64"] : [simulatorTarget];
  const iosGeneratedRoot = resolve(appRoot, "src-tauri/gen/apple");
  await mkdir(resolve(iosGeneratedRoot, "Externals"), { recursive: true });
  await run("xcodegen", ["generate", "--spec", "project.yml"], {
    cwd: iosGeneratedRoot,
  });
  await run(
    node,
    [
      mobileCli,
      "ios",
      "build",
      "--ci",
      "--no-sign",
      ...(targetSet === "ci" ? ["--debug"] : []),
      "--features",
      "mobile-system-webview,custom-protocol",
      "--target",
      ...targets,
    ],
    { cwd: appRoot },
  );
}
} finally {
  await restoreBuildTemplate();
}
