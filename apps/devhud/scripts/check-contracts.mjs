import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const revision = "f49ebda2fdba5755456b0f049e32593ca0ea331a";
const repository = "https://github.com/tauri-apps/tauri";

const paths = {
  cargoLock: resolve(repositoryRoot, "Cargo.lock"),
  cargoManifest: resolve(appRoot, "src-tauri/Cargo.toml"),
  diagnosticsBridgeManifest: resolve(
    appRoot,
    "src-tauri/diagnostics-bridge/Cargo.toml",
  ),
  authBridgeManifest: resolve(appRoot, "src-tauri/auth-bridge/Cargo.toml"),
  desktopMainCapability: resolve(
    appRoot,
    "src-tauri/capabilities/desktop-main.json",
  ),
  mobileMainCapability: resolve(
    appRoot,
    "src-tauri/capabilities/mobile-main.json",
  ),
  packageLock: resolve(repositoryRoot, "pnpm-lock.yaml"),
  packageManifest: resolve(appRoot, "package.json"),
  realqaCaptureCapability: resolve(
    appRoot,
    "src-tauri/capabilities/realqa-capture.json",
  ),
  realqaComposerCapability: resolve(
    appRoot,
    "src-tauri/capabilities/realqa-composer.json",
  ),
  rootCargoManifest: resolve(repositoryRoot, "Cargo.toml"),
  rustShell: resolve(appRoot, "src-tauri/src/lib.rs"),
  settingsCapability: resolve(appRoot, "src-tauri/capabilities/settings.json"),
  tauriConfig: resolve(appRoot, "src-tauri/tauri.conf.json"),
  updaterBoundary: resolve(appRoot, "src-tauri/src/updater.rs"),
  widgetBridgeManifest: resolve(
    appRoot,
    "src-tauri/widget-bridge/Cargo.toml",
  ),
};

const [
  cargoLock,
  cargoManifest,
  authBridgeManifest,
  diagnosticsBridgeManifest,
  desktopMainCapability,
  mobileMainCapability,
  packageLock,
  packageManifest,
  realqaCaptureCapability,
  realqaComposerCapability,
  rootCargoManifest,
  rustShell,
  settingsCapability,
  tauriConfig,
  updaterBoundary,
  widgetBridgeManifest,
] = await Promise.all([
  readFile(paths.cargoLock, "utf8"),
  readFile(paths.cargoManifest, "utf8"),
  readFile(paths.authBridgeManifest, "utf8"),
  readFile(paths.diagnosticsBridgeManifest, "utf8"),
  readFile(paths.desktopMainCapability, "utf8"),
  readFile(paths.mobileMainCapability, "utf8"),
  readFile(paths.packageLock, "utf8"),
  readFile(paths.packageManifest, "utf8"),
  readFile(paths.realqaCaptureCapability, "utf8"),
  readFile(paths.realqaComposerCapability, "utf8"),
  readFile(paths.rootCargoManifest, "utf8"),
  readFile(paths.rustShell, "utf8"),
  readFile(paths.settingsCapability, "utf8"),
  readFile(paths.tauriConfig, "utf8"),
  readFile(paths.updaterBoundary, "utf8"),
  readFile(paths.widgetBridgeManifest, "utf8"),
]);

const packageJson = JSON.parse(packageManifest);
const desktopMainCapabilityJson = JSON.parse(desktopMainCapability);
const mobileMainCapabilityJson = JSON.parse(mobileMainCapability);
const realqaCaptureCapabilityJson = JSON.parse(realqaCaptureCapability);
const realqaComposerCapabilityJson = JSON.parse(realqaComposerCapability);
const settingsCapabilityJson = JSON.parse(settingsCapability);
const tauriJson = JSON.parse(tauriConfig);
const failures = [];

function requireCondition(condition, message) {
  if (!condition) {
    failures.push(message);
  }
}

requireCondition(
  packageJson.devDependencies?.["@tauri-apps/cli-cef"] ===
    "3.0.0-alpha.6",
  "@tauri-apps/cli-cef must be pinned exactly to 3.0.0-alpha.6",
);
requireCondition(
  packageJson.version === "0.1.0" && tauriJson.version === "0.1.0",
  "the production preview manifests must identify version 0.1.0",
);
requireCondition(
  packageJson.devDependencies?.["@tauri-apps/cli-mobile"] ===
    "npm:@tauri-apps/cli@2.11.4",
  "@tauri-apps/cli-mobile must alias the standard CLI exactly at 2.11.4",
);
requireCondition(
  packageLock.includes("specifier: 3.0.0-alpha.6") &&
    packageLock.includes("version: 3.0.0-alpha.6"),
  "pnpm-lock.yaml must lock @tauri-apps/cli-cef 3.0.0-alpha.6",
);
requireCondition(
    !/\bbranch\s*=/u.test(cargoManifest) &&
    !/\bbranch\s*=/u.test(authBridgeManifest) &&
    !/\bbranch\s*=/u.test(diagnosticsBridgeManifest) &&
    !/\bbranch\s*=/u.test(widgetBridgeManifest),
  "Cargo.toml must not follow a moving Git branch",
);
requireCondition(
  rootCargoManifest.includes('"apps/devhud/src-tauri/auth-bridge"') &&
    new RegExp(
      `tauri-plugin\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
      "u",
    ).test(authBridgeManifest) &&
    new RegExp(
      `tauri\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
      "u",
    ).test(authBridgeManifest),
  "the private mobile authentication plugin must be a workspace member pinned to the same Tauri revision",
);
requireCondition(
  !cargoManifest.includes("[patch") && !rootCargoManifest.includes("[patch"),
  "Tauri, WRY, and cef-rs must not be overridden with Cargo patches",
);

for (const dependency of [
  "tauri",
  "tauri-build",
  "tauri-runtime-cef",
  "tauri-runtime-wry",
]) {
  const dependencyPattern = new RegExp(
    `${dependency}\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
    "u",
  );
  requireCondition(
    dependencyPattern.test(cargoManifest),
    `${dependency} must use the exact upstream Tauri revision`,
  );
}

requireCondition(
  /tauri-runtime-cef\s*=\s*\{[^}]*default-features\s*=\s*false[^}]*features\s*=\s*\["devtools", "sandbox"\]/u.test(
    cargoManifest,
  ),
  "desktop must select tauri-runtime-cef's sandbox feature directly",
);
requireCondition(
  cargoManifest.includes(
    "cfg(not(any(target_os = \"android\", target_os = \"ios\")))",
  ) &&
    cargoManifest.includes(
      "cfg(any(target_os = \"android\", target_os = \"ios\"))",
    ),
  "Cargo dependencies must select CEF on desktop and WRY on mobile",
);
requireCondition(
  cargoManifest.includes(
    'desktop-cef = ["linux-capture-backend", "dep:rfd", "dep:tauri", "dep:tauri-runtime-cef", "realqa-macos-capture", "tauri/devtools"]',
  ) &&
    cargoManifest.includes(
      'linux-capture-backend = ["dep:ashpd", "dep:tokio", "dep:x11rb"]',
    ) &&
    cargoManifest.includes(
      'mobile-system-webview = ["dep:tauri", "dep:tauri-runtime-wry"]',
    ),
  "Cargo features must keep desktop CEF and mobile system webviews mutually selectable",
);
requireCondition(
  rootCargoManifest.includes('"apps/devhud/src-tauri"'),
  "the DevHud Rust crate must be a root Cargo workspace member",
);
requireCondition(
  rootCargoManifest.includes('"apps/devhud/src-tauri/diagnostics-bridge"') &&
    new RegExp(
      `tauri-plugin\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
      "u",
    ).test(diagnosticsBridgeManifest) &&
    new RegExp(
      `tauri\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
      "u",
    ).test(diagnosticsBridgeManifest),
  "the private mobile diagnostics plugin must be a workspace member pinned to the same Tauri revision",
);
requireCondition(
  rootCargoManifest.includes('"apps/devhud/src-tauri/widget-bridge"') &&
    new RegExp(
      `tauri-plugin\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
      "u",
    ).test(widgetBridgeManifest) &&
    new RegExp(
      `tauri\\s*=\\s*\\{[^}]*git\\s*=\\s*"${repository}"[^}]*rev\\s*=\\s*"${revision}"`,
      "u",
    ).test(widgetBridgeManifest),
  "the private mobile widget plugin must be a workspace member pinned to the same Tauri revision",
);
requireCondition(
  cargoLock.includes(
    `git+${repository}?rev=${revision}#${revision}`,
  ),
  "Cargo.lock must resolve Tauri sources at the exact revision",
);
requireCondition(
  tauriJson.identifier === "dev.deli.devhud",
  "the Tauri application identifier must be dev.deli.devhud",
);
requireCondition(
  tauriJson.build?.frontendDist === "../dist" &&
    tauriJson.build?.devUrl === undefined,
  "Tauri must load only bundled frontend assets",
);
requireCondition(
  Array.isArray(tauriJson.app?.windows) &&
    tauriJson.app.windows.length === 0,
  "the main window must be created with explicit navigation guards",
);
requireCondition(
  tauriJson.bundle?.active === true &&
    tauriJson.bundle?.targets === "all" &&
    tauriJson.bundle?.createUpdaterArtifacts === true &&
    tauriJson.bundle?.macOS?.minimumSystemVersion === "14.0",
  "the 0.1.0 preview must enable host bundles, updater artifacts, and macOS 14+",
);
requireCondition(
  tauriJson.plugins === undefined,
  "the common scaffold must not expose plugin, updater, or deep-link configuration",
);
const expectedMobileMainPermissions = [
  "allow-get-runtime-info",
  "allow-read-settings",
  "allow-write-settings",
  "allow-read-shortcut-effective-state",
  "allow-write-shortcut-effective-state",
  "allow-read-widget-configuration",
  "allow-write-widget-configuration",
  "allow-export-diagnostics",
  "allow-reset-dev-hud",
  "allow-get-auth-session",
  "allow-start-authentication",
  "allow-logout-authentication",
];
const expectedDesktopMainPermissions = [
  "allow-get-runtime-info",
  "allow-read-settings",
  "allow-read-widget-configuration",
  "allow-hide-hud",
  "allow-show-settings",
  "allow-get-auth-session",
  "allow-start-authentication",
  "allow-logout-authentication",
];
const expectedSettingsPermissions = [
  "allow-get-runtime-info",
  "allow-read-settings",
  "allow-write-settings",
  "allow-read-shortcut-effective-state",
  "allow-write-shortcut-effective-state",
  "allow-read-widget-configuration",
  "allow-export-diagnostics",
  "allow-reset-dev-hud",
  "allow-hide-settings",
  "allow-replace-global-shortcut",
  "allow-set-launch-at-login",
  "allow-complete-first-run",
  "allow-request-update-action",
  "allow-get-auth-session",
  "allow-start-authentication",
  "allow-logout-authentication",
];
const expectedRealqaCapturePermissions = [
  "allow-realqa-capture-permission-status",
  "allow-realqa-request-capture-permission",
  "allow-realqa-inspect-capture-capabilities",
  "allow-realqa-list-capture-sources",
  "allow-realqa-adjust-capture-selection",
  "allow-realqa-begin-capture",
  "allow-realqa-cancel-capture",
];
const expectedRealqaComposerPermissions = [
  "allow-realqa-composer-accept-image",
  "allow-realqa-composer-flatten-image",
  "allow-realqa-composer-remove-image",
  "allow-realqa-composer-reset-session",
  "allow-realqa-take-browser-capture",
];
requireCondition(
  JSON.stringify(mobileMainCapabilityJson.windows) === JSON.stringify(["main"]) &&
    JSON.stringify(mobileMainCapabilityJson.platforms) ===
      JSON.stringify(["iOS", "android"]) &&
    JSON.stringify(mobileMainCapabilityJson.permissions) ===
      JSON.stringify(expectedMobileMainPermissions),
  "the mobile capability must expose only its scoped persistence, diagnostics, and reset commands",
);
requireCondition(
  JSON.stringify(desktopMainCapabilityJson.windows) ===
      JSON.stringify(["main"]) &&
    JSON.stringify(desktopMainCapabilityJson.permissions) ===
      JSON.stringify(expectedDesktopMainPermissions),
  "the desktop HUD capability must expose only desktop lifecycle commands",
);
requireCondition(
  JSON.stringify(settingsCapabilityJson.windows) ===
      JSON.stringify(["settings"]) &&
    JSON.stringify(settingsCapabilityJson.permissions) ===
      JSON.stringify(expectedSettingsPermissions),
  "the settings capability must expose only its scoped settings commands",
);
requireCondition(
  JSON.stringify(realqaCaptureCapabilityJson.windows) ===
      JSON.stringify(["realqa-capture"]) &&
    JSON.stringify(realqaCaptureCapabilityJson.platforms) ===
      JSON.stringify(["linux", "macOS", "windows"]) &&
    JSON.stringify(realqaCaptureCapabilityJson.permissions) ===
      JSON.stringify(expectedRealqaCapturePermissions),
  "the RealQA capture capability must expose only capture-window commands",
);
requireCondition(
  JSON.stringify(realqaComposerCapabilityJson.windows) ===
      JSON.stringify(["realqa-composer"]) &&
    JSON.stringify(realqaComposerCapabilityJson.platforms) ===
      JSON.stringify(["linux", "macOS", "windows"]) &&
    JSON.stringify(realqaComposerCapabilityJson.permissions) ===
      JSON.stringify(expectedRealqaComposerPermissions),
  "the RealQA composer capability must expose only composer-window commands",
);
requireCondition(
  JSON.stringify(tauriJson.app?.security?.capabilities) ===
    JSON.stringify([
      "desktop-main",
      "settings",
      "mobile-main",
      "realqa-capture",
      "realqa-composer",
    ]),
  "the Tauri configuration must enable only the five exact window capabilities",
);
for (const action of [
  "Open DevHud",
  "Settings",
  "Check for Updates",
  "Open DevTools",
  "Quit",
]) {
  requireCondition(
    rustShell.includes(`"${action}"`),
    `the tray must include ${action}`,
  );
}
requireCondition(
  rustShell.includes(".visible(false)") &&
    rustShell.includes(".always_on_top(true)") &&
    rustShell.includes(".skip_taskbar(true)") &&
    rustShell.includes("ActivationPolicy::Accessory"),
  "the desktop HUD must be hidden, always-on-top, taskbar-free, and menu-bar resident",
);
requireCondition(
  rustShell.match(/\.incognito\(true\)/gu)?.length === 3,
  "all three desktop CEF webviews must use off-the-record profiles",
);
for (const storageSwitch of [
  "--disable-application-cache",
  "--disable-databases",
  "--disable-local-storage",
  "--disable-session-storage",
  "--disable-sync",
  "--incognito",
]) {
  requireCondition(
    rustShell.includes(`"${storageSwitch}"`),
    `desktop CEF must apply ${storageSwitch}`,
  );
}
requireCondition(
  rustShell.includes(".root_cache_path(profile)") &&
    rustShell.includes('const CEF_PROFILE_DIRECTORY: &str = "cef"') &&
    rustShell.includes("validate_cef_profile_target") &&
    rustShell.includes("preflight_cef_profile_reset") &&
    rustShell.includes("reset_cef_profile_directory"),
  "desktop CEF must use and reset only its explicit application-owned profile root",
);
requireCondition(
  (rustShell.match(/\.on_download\(\|_, _\| false\)/gu)?.length ?? 0) === 4 &&
    (rustShell.match(/\.on_web_resource_request\(apply_web_resource_policy\)/gu)
      ?.length ?? 0) === 4,
  "every desktop and mobile webview must deny downloads and remote resources",
);
requireCondition(
  rustShell.includes("preflight_local_logs_for_reset") &&
    rustShell.includes("preflight_reset()") &&
    rustShell.includes("prepare_reset()"),
  "reset must resolve persistence, mobile widget, and bounded-log preconditions before mutation",
);
requireCondition(
  rustShell.includes("build_settings_window(app.handle()).is_err()") &&
    rustShell.includes(
      "diagnostics::DiagnosticClassification::DisplayWindowUnavailable",
    ) &&
    !rustShell.includes("build_settings_window(app.handle())?;"),
  "first-run settings failure must keep the tray-resident process alive",
);
requireCondition(
  !updaterBoundary.includes("reqwest") &&
    !updaterBoundary.includes("ureq") &&
    !updaterBoundary.includes("github.com") &&
    updaterBoundary.includes("ScopedUpdaterUnavailable"),
  "the typed preview updater boundary must perform no network access",
);

if (failures.length > 0) {
  throw new Error(
    `DevHud foundation contracts failed:\n- ${failures.join("\n- ")}`,
  );
}

console.log(
  JSON.stringify({
    check: "devhud-foundation-contracts",
    status: "passed",
    tauriRevision: revision,
    cliCefVersion: "3.0.0-alpha.6",
  }),
);
