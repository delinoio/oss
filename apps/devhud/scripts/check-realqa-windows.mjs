import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const read = (path) => readFile(resolve(appRoot, path), "utf8");
const failures = [];
const requireCondition = (condition, message) => {
  if (!condition) failures.push(message);
};

const [
  capabilitySource,
  cargoSource,
  captureCoreSource,
  nativeSource,
  packageSource,
  windowsSource,
] = await Promise.all([
  read("src-tauri/capabilities/realqa-capture.json"),
  read("src-tauri/Cargo.toml"),
  read("src-tauri/src/realqa_capture/mod.rs"),
  read("src-tauri/src/lib.rs"),
  read("package.json"),
  read("src-tauri/src/realqa_capture/windows.rs"),
]);

const capability = JSON.parse(capabilitySource);
const packageJson = JSON.parse(packageSource);
const exactCommands = [
  "allow-realqa-capture-permission-status",
  "allow-realqa-request-capture-permission",
  "allow-realqa-inspect-capture-capabilities",
  "allow-realqa-list-capture-sources",
  "allow-realqa-adjust-capture-selection",
  "allow-realqa-begin-capture",
  "allow-realqa-cancel-capture",
];

requireCondition(
  capability.identifier === "realqa-capture" &&
    capability.local === true &&
    capability.remote === undefined &&
    JSON.stringify(capability.windows) === JSON.stringify(["realqa-capture"]) &&
    JSON.stringify(capability.permissions) === JSON.stringify(exactCommands),
  "Windows capture must remain local and scoped to the exact RealQA window and commands",
);
requireCondition(
  capability.permissions.every(
    (permission) =>
      !/(?:^|:)(?:default|fs|http|opener|os|process|screen|shell|store)(?::|$)/u.test(
        permission,
      ),
  ),
  "Windows capture must not grant a generic screen, process, filesystem, or network plugin",
);
requireCondition(
  cargoSource.includes("[target.'cfg(target_os = \"windows\")'.dependencies]") &&
    cargoSource.includes('windows-capture = "=2.0.0"') &&
    cargoSource.includes('"Graphics_Capture"') &&
    cargoSource.includes('"Win32_UI_HiDpi"'),
  "the backend must use pinned Windows-only Graphics Capture and DPI APIs",
);
requireCondition(
  windowsSource.includes(
    'any(target_arch = "x86_64", target_arch = "aarch64")',
  ) &&
    windowsSource.includes(
      'not(any(target_arch = "x86_64", target_arch = "aarch64"))',
    ),
  "the Windows capture backend must activate only on x64 and ARM64",
);
for (const contract of [
  "GraphicsCaptureSession::IsSupported",
  "CursorCaptureSettings::WithCursor",
  "CursorCaptureSettings::WithoutCursor",
  "GetDpiForMonitor",
  "GetWindowDisplayAffinity",
  "WindowsPlatformAdapter",
  "MAX_DECODED_PIXELS",
  "WindowMinimized",
  "WindowClosed",
  "DisplayRemoved",
  "revalidate_snapshot",
  "active_captures",
]) {
  requireCondition(
    windowsSource.includes(contract),
    `Windows capture source must retain ${contract}`,
  );
}
for (const privateValue of [
  "as_raw_hwnd",
  "as_raw_hmonitor",
  "process_id",
  "device_name",
]) {
  requireCondition(
    !captureCoreSource.includes(privateValue),
    `the shared capture/IPC core must not receive native ${privateValue} values`,
  );
}
requireCondition(
  captureCoreSource.includes("WindowMetadata") &&
    captureCoreSource.includes("CaptureDiagnostic") &&
    windowsSource.includes("sanitize_metadata") &&
    windowsSource.includes("diagnostics_never_receive_it"),
  "only bounded metadata may cross the core and capture diagnostics must remain value-free",
);
requireCondition(
  nativeSource.includes("async fn realqa_begin_capture("),
  "capture must run as an async Tauri command so cancellation stays responsive",
);
requireCondition(
  windowsSource.includes("SecondaryWindowSettings::Default") &&
    !windowsSource.includes("SecondaryWindowSettings::Exclude"),
  "Windows 11 capture must not require the Windows 11 24H2 secondary-window API",
);
requireCondition(
  packageJson.scripts?.["check:realqa:windows"] ===
      "node scripts/check-realqa-windows.mjs" &&
    typeof packageJson.scripts?.["test:realqa:native"] === "string",
  "the package must expose Windows capability inspection and native RealQA tests",
);

if (failures.length > 0) {
  throw new Error(
    `RealQA Windows capture contract failed:\n- ${failures.join("\n- ")}`,
  );
}

console.log(
  JSON.stringify({
    architectures: ["x64", "arm64"],
    backend: "windows-graphics-capture",
    capabilities: exactCommands,
    check: "devhud-realqa-windows-capture",
    genericAuthority: false,
    status: "passed",
  }),
);
