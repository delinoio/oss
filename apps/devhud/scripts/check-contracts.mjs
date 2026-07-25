import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const revision = "f49ebda2fdba5755456b0f049e32593ca0ea331a";
const repository = "https://github.com/tauri-apps/tauri";

const paths = {
  cargoLock: resolve(repositoryRoot, "Cargo.lock"),
  cargoManifest: resolve(appRoot, "src-tauri/Cargo.toml"),
  mainCapability: resolve(appRoot, "src-tauri/capabilities/main.json"),
  packageLock: resolve(repositoryRoot, "pnpm-lock.yaml"),
  packageManifest: resolve(appRoot, "package.json"),
  rootCargoManifest: resolve(repositoryRoot, "Cargo.toml"),
  rustShell: resolve(appRoot, "src-tauri/src/lib.rs"),
  settingsCapability: resolve(appRoot, "src-tauri/capabilities/settings.json"),
  tauriConfig: resolve(appRoot, "src-tauri/tauri.conf.json"),
  updaterBoundary: resolve(appRoot, "src-tauri/src/updater.rs"),
};

const [
  cargoLock,
  cargoManifest,
  mainCapability,
  packageLock,
  packageManifest,
  rootCargoManifest,
  rustShell,
  settingsCapability,
  tauriConfig,
  updaterBoundary,
] = await Promise.all([
  readFile(paths.cargoLock, "utf8"),
  readFile(paths.cargoManifest, "utf8"),
  readFile(paths.mainCapability, "utf8"),
  readFile(paths.packageLock, "utf8"),
  readFile(paths.packageManifest, "utf8"),
  readFile(paths.rootCargoManifest, "utf8"),
  readFile(paths.rustShell, "utf8"),
  readFile(paths.settingsCapability, "utf8"),
  readFile(paths.tauriConfig, "utf8"),
  readFile(paths.updaterBoundary, "utf8"),
]);

const packageJson = JSON.parse(packageManifest);
const mainCapabilityJson = JSON.parse(mainCapability);
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
  packageLock.includes("specifier: 3.0.0-alpha.6") &&
    packageLock.includes("version: 3.0.0-alpha.6"),
  "pnpm-lock.yaml must lock @tauri-apps/cli-cef 3.0.0-alpha.6",
);
requireCondition(
  !/\bbranch\s*=/u.test(cargoManifest),
  "Cargo.toml must not follow a moving Git branch",
);
requireCondition(
  !cargoManifest.includes("[patch") && !rootCargoManifest.includes("[patch"),
  "Tauri, WRY, and cef-rs must not be overridden with Cargo patches",
);

for (const dependency of ["tauri", "tauri-build", "tauri-runtime-cef"]) {
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
  /tauri-runtime-cef\s*=\s*\{[^}]*default-features\s*=\s*false[^}]*features\s*=\s*\["sandbox"\]/u.test(
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
    'desktop-cef = ["dep:tauri", "dep:tauri-runtime-cef"]',
  ) &&
    cargoManifest.includes('mobile-system-webview = ["dep:tauri"]'),
  "Cargo features must keep desktop CEF and mobile system webviews mutually selectable",
);
requireCondition(
  rootCargoManifest.includes('"apps/devhud/src-tauri"'),
  "the DevHud Rust crate must be a root Cargo workspace member",
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
const expectedMainPermissions = [
  "allow-get-runtime-info",
  "allow-read-settings",
  "allow-write-settings",
  "allow-read-widget-configuration",
  "allow-write-widget-configuration",
  "allow-hide-hud",
  "allow-show-settings",
];
const expectedSettingsPermissions = [
  "allow-get-runtime-info",
  "allow-read-settings",
  "allow-write-settings",
  "allow-read-widget-configuration",
  "allow-hide-settings",
  "allow-replace-global-shortcut",
  "allow-set-launch-at-login",
  "allow-complete-first-run",
];
requireCondition(
  JSON.stringify(mainCapabilityJson.windows) === JSON.stringify(["main"]) &&
    JSON.stringify(mainCapabilityJson.permissions) ===
      JSON.stringify(expectedMainPermissions),
  "the HUD capability must expose only its scoped shell and persistence commands",
);
requireCondition(
  JSON.stringify(settingsCapabilityJson.windows) ===
      JSON.stringify(["settings"]) &&
    JSON.stringify(settingsCapabilityJson.permissions) ===
      JSON.stringify(expectedSettingsPermissions),
  "the settings capability must expose only its scoped settings commands",
);
requireCondition(
  JSON.stringify(tauriJson.app?.security?.capabilities) ===
    JSON.stringify(["main", "settings"]),
  "the Tauri configuration must enable the split HUD and settings capabilities",
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
