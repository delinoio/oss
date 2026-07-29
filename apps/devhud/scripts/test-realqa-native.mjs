import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

import { run } from "./process.mjs";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const cargo = process.platform === "win32" ? "cargo.exe" : "cargo";
const rustup = process.platform === "win32" ? "rustup.exe" : "rustup";
const [rustBackend, nativeBackend, infoPlist, capabilitySource] =
  await Promise.all([
    readFile(
      resolve(appRoot, "src-tauri/src/realqa_capture/macos.rs"),
      "utf8",
    ),
    readFile(
      resolve(appRoot, "src-tauri/src/realqa_capture/macos_native.m"),
      "utf8",
    ),
    readFile(resolve(appRoot, "src-tauri/Info.plist"), "utf8"),
    readFile(
      resolve(appRoot, "src-tauri/capabilities/realqa-capture.json"),
      "utf8",
    ),
  ]);
const capability = JSON.parse(capabilitySource);
const failures = [];
const requireCondition = (condition, message) => {
  if (!condition) failures.push(message);
};

requireCondition(
  rustBackend.includes("trait MacosNativeAdapter") &&
    rustBackend.includes("SystemMacosNativeAdapter") &&
    rustBackend.includes("FixtureNative"),
  "macOS capture must retain injectable system and fixture adapters",
);
requireCondition(
  nativeBackend.includes("SCScreenshotManager") &&
    nativeBackend.includes("CGPreflightScreenCaptureAccess") &&
    nativeBackend.includes("CGRequestScreenCaptureAccess") &&
    nativeBackend.includes("configuration.showsCursor = showsCursor"),
  "macOS capture must use ScreenCaptureKit with explicit permission and pointer controls",
);
requireCondition(
  nativeBackend.includes("realqa_safe_metadata(") &&
    nativeBackend.includes("realqa_is_path_like_metadata(value)") &&
    nativeBackend.includes("realqa_is_path_like_metadata(trimmed)") &&
    nativeBackend.includes("REALQA_MAX_PROCESS_NAME_UTF16_UNITS") &&
    nativeBackend.includes("REALQA_MAX_WINDOW_TITLE_UTF16_UNITS"),
  "macOS capture must bound and sanitize metadata, including after normalization, before native JSON serialization",
);
requireCondition(
  nativeBackend.includes("@synchronized(captureStateLock)") &&
    nativeBackend.includes("__block bool acceptsResult = true") &&
    nativeBackend.includes("acceptsResult = false"),
  "macOS capture must reject and release screenshot results that arrive after timeout",
);
requireCondition(
  rustBackend.includes("native_window_ids: HashMap<WindowSourceId, u32>") &&
    rustBackend.includes("expected_frame: request.logical_bounds") &&
    nativeBackend.includes("realqa_frame_matches(selected.frame"),
  "macOS window capture must resolve opaque IDs locally and reject stale native geometry",
);
requireCondition(
  /<key>NSScreenCaptureUsageDescription<\/key>\s*<string>[^<]+<\/string>/u.test(
    infoPlist,
  ),
  "the macOS bundle must declare a screen-capture purpose string",
);
requireCondition(
  !/(?:NSTask|posix_spawn|system\s*\(|Command::new)/u.test(
    `${rustBackend}\n${nativeBackend}`,
  ),
  "macOS capture must not launch helper processes",
);
requireCondition(
  !/(?:tracing::|NSLog|printStackTrace|localizedDescription)/u.test(
    `${rustBackend}\n${nativeBackend}`,
  ),
  "native capture diagnostics must not emit source, path, title, or native error text",
);
requireCondition(
  JSON.stringify(capability.windows) ===
    JSON.stringify(["realqa-capture"]) &&
    capability.permissions.every((permission) =>
      permission.startsWith("allow-realqa-"),
    ),
  "capture capability must remain bound to the exact RealQA window and commands",
);
if (failures.length > 0) {
  throw new Error(
    `RealQA native capture contract failed:\n- ${failures.join("\n- ")}`,
  );
}

await run(
  cargo,
  ["test", "-p", "devhud", "--locked", "realqa_capture::macos"],
  { cwd: repositoryRoot },
);

if (process.platform === "darwin") {
  const targets = ["x86_64-apple-darwin", "aarch64-apple-darwin"];
  await run(rustup, ["target", "add", ...targets], { cwd: repositoryRoot });
  for (const target of targets) {
    await run(
      cargo,
      [
        "check",
        "-p",
        "devhud",
        "--lib",
        "--locked",
        "--features",
        "realqa-macos-capture",
        "--target",
        target,
      ],
      { cwd: repositoryRoot },
    );
  }
  if (process.env.DEVHUD_REALQA_MACOS_SMOKE === "1") {
    await run(
      cargo,
      [
        "test",
        "-p",
        "devhud",
        "--locked",
        "realqa_capture::macos::tests::system_adapter_smoke_uses_current_permission_without_prompting",
        "--",
        "--ignored",
        "--exact",
      ],
      { cwd: repositoryRoot },
    );
  }
}

console.log(
  JSON.stringify({
    check: "devhud-realqa-native-macos",
    status: "passed",
    fixtureTests: true,
    architectureChecks: process.platform === "darwin",
    desktopSmoke: process.env.DEVHUD_REALQA_MACOS_SMOKE === "1",
  }),
);
