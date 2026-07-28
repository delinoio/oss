import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const read = (path) => readFile(resolve(appRoot, path), "utf8");
const failures = [];
const requireCondition = (condition, message) => {
  if (!condition) failures.push(message);
};

const [
  androidPlugin,
  cargoManifest,
  diagnosticsSource,
  frontendSource,
  iosPlugin,
  localLogSource,
  macosCaptureSource,
  mobileCapabilitySource,
  nativeSource,
  packageSource,
  realqaCaptureSource,
  settingsCapabilitySource,
] = await Promise.all([
  read(
    "src-tauri/diagnostics-bridge/android/src/main/java/dev/deli/devhud/diagnostics/DevHudDiagnosticsPlugin.kt",
  ),
  read("src-tauri/Cargo.toml"),
  read("src-tauri/src/diagnostics.rs"),
  read("src/App.tsx"),
  read(
    "src-tauri/diagnostics-bridge/ios/Sources/DevHudDiagnosticsPlugin/DevHudDiagnosticsPlugin.swift",
  ),
  read("src-tauri/src/local_log.rs"),
  read("src-tauri/src/realqa_capture/macos.rs"),
  read("src-tauri/capabilities/mobile-main.json"),
  read("src-tauri/src/lib.rs"),
  read("package.json"),
  read("src-tauri/src/realqa_capture/mod.rs"),
  read("src-tauri/capabilities/settings.json"),
]);

const mobileCapability = JSON.parse(mobileCapabilitySource);
const settingsCapability = JSON.parse(settingsCapabilitySource);
const packageJson = JSON.parse(packageSource);

requireCondition(
  cargoManifest.includes('tracing = "0.1"') &&
    cargoManifest.includes(
      'uuid = { version = "1", features = ["serde", "v7"] }',
    ) &&
    !cargoManifest.includes("tracing-subscriber"),
  "diagnostics must use the typed tracing facade and UUID v7 without a permissive subscriber",
);
requireCondition(
  !/(?:tracing::|localizedDescription|NSLog)/u.test(
    macosCaptureSource,
  ) &&
    macosCaptureSource.includes("safe_metadata") &&
    macosCaptureSource.includes("looks_like_path"),
  "macOS capture must retain value-free diagnostics and redact path-like native metadata",
);
requireCondition(
  diagnosticsSource.includes("#[serde(rename_all = \"camelCase\", deny_unknown_fields)]") &&
    diagnosticsSource.includes("record.is_valid().then_some(record)") &&
    diagnosticsSource.includes("self.session_id.get_version_num() == 7"),
  "stored and exported diagnostic records must fail closed against unknown fields, metadata, and non-v7 sessions",
);
requireCondition(
  diagnosticsSource.includes("7 * 24 * 60 * 60") === false &&
    localLogSource.includes("Duration::from_secs(7 * 24 * 60 * 60)") &&
    localLogSource.includes("20 * 1024 * 1024") &&
    localLogSource.includes("rotation_never_exceeds_the_total_byte_budget") &&
    localLogSource.includes("pruning_removes_logs_at_the_seven_day_boundary"),
  "the local sink must validate seven-day and 20 MB rotation",
);
requireCondition(
  diagnosticsSource.includes(
    '2.11.5+f49ebda2fdba5755456b0f049e32593ca0ea331a',
  ) &&
    diagnosticsSource.includes("150.0.0+150.0.10"),
  "diagnostic metadata must identify the exact upstream Tauri and CEF pins",
);
requireCondition(
  diagnosticsSource.includes("export_recursively_rejects_unknown_and_adversarial_values") &&
    diagnosticsSource.includes("fatal_initialization_is_one_safe_record_without_an_exception"),
  "Rust diagnostics tests must cover recursive adversarial redaction and fatal events",
);
const captureOutcome =
  realqaCaptureSource.match(
    /pub\(crate\) fn record_outcome[\s\S]*?\n\}\n\n#\[cfg\(test\)\]/u,
  )?.[0] ?? "";
requireCondition(
  captureOutcome.includes("RealqaCaptureOutcome") &&
    captureOutcome.includes("RealqaCapturePortalCancelled") &&
    captureOutcome.includes("RealqaCaptureProtectedContent") &&
    !/(process_name|session_id|title|window_id|display_id|logical_bounds|rgba|bytes)/u.test(
      captureOutcome,
    ),
  "capture diagnostics must emit only closed outcome classifications without source, pixel, session, or geometry values",
);
requireCondition(
  (nativeSource.match(/tracing::/gu) ?? []).length === 0 &&
    (diagnosticsSource.match(/tracing::/gu) ?? []).length === 2,
  "only the typed diagnostics facade may emit tracing events",
);
requireCondition(
  nativeSource.includes("export_selected_destination") &&
    nativeSource.includes("Cancelled") &&
    nativeSource.includes("cancelled_diagnostics_export_never_opens_or_mutates_a_destination") &&
    !nativeSource.includes("tauri_plugin_http") &&
    !nativeSource.includes("reqwest"),
  "native export must validate explicit selection, cancellation, and absence of remote transport",
);
requireCondition(
  mobileCapability.permissions.includes("allow-export-diagnostics") &&
    settingsCapability.permissions.includes("allow-export-diagnostics") &&
    !mobileCapability.permissions.some((permission) =>
      /(?:dialog|fs|http|upload|shell|opener)/u.test(permission),
    ) &&
    !settingsCapability.permissions.some((permission) =>
      /(?:dialog|fs|http|upload|shell|opener)/u.test(permission),
    ),
  "frontend capabilities must expose only the scoped diagnostics command, never generic picker, filesystem, or transport authority",
);
requireCondition(
  androidPlugin.includes("Intent.ACTION_CREATE_DOCUMENT") &&
    androidPlugin.includes("Activity.RESULT_CANCELED") &&
    androidPlugin.includes('status("cancelled")') &&
    androidPlugin.includes('"devhud-diagnostics-export"') &&
    androidPlugin.includes("activity.runOnUiThread") &&
    !androidPlugin.includes("android.permission.INTERNET") &&
    !/https?:\/\//u.test(androidPlugin),
  "Android diagnostics export must use the user document picker, perform writes off the UI thread, and have no network authority",
);
requireCondition(
  iosPlugin.includes("UIDocumentPickerViewController") &&
    iosPlugin.includes('finish(status: "cancelled")') &&
    iosPlugin.includes("removeItem(at: temporaryURL)") &&
    iosPlugin.includes("if let temporaryURL") &&
    !iosPlugin.includes("try? FileManager.default.removeItem") &&
    !/https?:\/\//u.test(iosPlugin),
  "iOS diagnostics export must use the user document picker and retry or surface staging cleanup failures",
);
requireCondition(
  frontendSource.includes('onClick={() => void startExport()}') &&
    frontendSource.includes("never sends diagnostics remotely") &&
    frontendSource.includes("No file was changed"),
  "frontend diagnostics export must require an explicit action and disclose cancellation/local-only behavior",
);
requireCondition(
  packageJson.scripts?.["check:diagnostics"] ===
      "node scripts/check-diagnostics.mjs" &&
    typeof packageJson.scripts?.["test:diagnostics"] === "string",
  "the package must expose local diagnostic check and test commands",
);

const forbiddenDiagnosticFields = [
  "account",
  "authorization",
  "clipboard",
  "credential",
  "environment",
  "invitation",
  "path:",
  "search_text",
  "shortcut_key",
  "signing_data",
  "token:",
  "user_file",
];
const recordDefinition =
  diagnosticsSource.match(/struct DiagnosticRecord \{([\s\S]*?)\n\}/u)?.[1] ?? "";
for (const field of forbiddenDiagnosticFields) {
  requireCondition(
    !recordDefinition.toLowerCase().includes(field),
    `diagnostic records must not contain forbidden field ${field}`,
  );
}

if (failures.length > 0) {
  throw new Error(`DevHud diagnostics contract failed:\n- ${failures.join("\n- ")}`);
}

console.log(
  JSON.stringify({
    check: "devhud-local-diagnostics",
    status: "passed",
    retentionDays: 7,
    maximumBytes: 20 * 1024 * 1024,
    remoteTransport: false,
  }),
);
