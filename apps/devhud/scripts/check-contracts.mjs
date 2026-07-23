import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const revision = "649d4e6b0fbfd0b60cb5f2ed8d83ceef648a6769";
const repository = "https://github.com/tauri-apps/tauri";

const paths = {
  cargoLock: resolve(repositoryRoot, "Cargo.lock"),
  cargoManifest: resolve(appRoot, "src-tauri/Cargo.toml"),
  capability: resolve(appRoot, "src-tauri/capabilities/probe.json"),
  infoPlist: resolve(appRoot, "src-tauri/Info.plist"),
  macosWorkflow: resolve(
    repositoryRoot,
    ".github/workflows/devhud-macos-cef-gate.yml",
  ),
  packageLock: resolve(repositoryRoot, "pnpm-lock.yaml"),
  packageManifest: resolve(appRoot, "package.json"),
  rootCargoManifest: resolve(repositoryRoot, "Cargo.toml"),
  tauriConfig: resolve(appRoot, "src-tauri/tauri.conf.json"),
};

const [
  cargoLock,
  cargoManifest,
  capability,
  infoPlist,
  macosWorkflow,
  packageLock,
  packageManifest,
  rootCargoManifest,
  tauriConfig,
] = await Promise.all([
  readFile(paths.cargoLock, "utf8"),
  readFile(paths.cargoManifest, "utf8"),
  readFile(paths.capability, "utf8"),
  readFile(paths.infoPlist, "utf8"),
  readFile(paths.macosWorkflow, "utf8"),
  readFile(paths.packageLock, "utf8"),
  readFile(paths.packageManifest, "utf8"),
  readFile(paths.rootCargoManifest, "utf8"),
  readFile(paths.tauriConfig, "utf8"),
]);

const packageJson = JSON.parse(packageManifest);
const capabilityJson = JSON.parse(capability);
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
    cargoManifest.includes('"desktop-cef",') &&
    cargoManifest.includes('"dep:auto-launch",') &&
    cargoManifest.includes('"dep:global-hotkey",') &&
    cargoManifest.includes('mobile-system-webview = ["dep:tauri"]'),
  "Cargo features must isolate the macOS gate and keep mobile system webviews selectable",
);
requireCondition(
  /auto-launch\s*=\s*\{\s*version\s*=\s*"=0\.5\.0",\s*optional\s*=\s*true\s*\}/u.test(
    cargoManifest,
  ) &&
    /global-hotkey\s*=\s*\{\s*version\s*=\s*"=0\.8\.0",\s*optional\s*=\s*true\s*\}/u.test(
      cargoManifest,
    ),
  "macOS native integration crates must remain exact, optional target dependencies",
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
  "the probe window must be created with explicit navigation guards",
);
requireCondition(
  tauriJson.bundle?.active === false,
  "the common gate scaffold must not enable production bundling",
);
requireCondition(
  tauriJson.plugins === undefined,
  "the common scaffold must not expose plugin, updater, or deep-link configuration",
);
requireCondition(
  capabilityJson.windows?.length === 1 &&
    capabilityJson.windows[0] === "probe",
  "the probe capability must be window-specific",
);
requireCondition(
  capabilityJson.permissions?.includes("allow-probe-bundled-asset-ready") &&
    capabilityJson.permissions?.includes("allow-probe-denial-observed") &&
    capabilityJson.permissions?.includes("allow-probe-gate-mode") &&
    capabilityJson.permissions?.includes("allow-probe-macos-gate-run") &&
    capabilityJson.permissions?.includes("allow-probe-macos-gate-complete") &&
    capabilityJson.permissions?.includes(
      "allow-probe-macos-gate-renderer-ready",
    ) &&
    capabilityJson.permissions?.includes("allow-probe-gate-failure") &&
    !capabilityJson.permissions?.includes("allow-probe-forbidden"),
  "the capability must allow only the scoped probe commands and deny the forbidden command",
);
requireCondition(
  infoPlist.includes("<key>LSUIElement</key>") &&
    infoPlist.includes("<true/>"),
  "the macOS probe must declare persistent hidden-Dock behavior",
);
requireCondition(
  macosWorkflow.includes("runner: macos-15-intel") &&
    macosWorkflow.includes("target: x86_64-apple-darwin") &&
    macosWorkflow.includes("runner: macos-15") &&
    macosWorkflow.includes("target: aarch64-apple-darwin") &&
    macosWorkflow.includes("pnpm --dir apps/devhud gate:macos"),
  "the isolated macOS gate must cover native x64 and ARM64 runners",
);
requireCondition(
  macosWorkflow.includes(
    "github.event_name == 'pull_request' || ! github.ref_protected",
  ) &&
    macosWorkflow.includes(
      "github.event_name != 'pull_request' && github.ref_protected",
    ),
  "pull requests and unprotected refs must run the macOS gate without signing credentials",
);
requireCondition(
  macosWorkflow.includes(
    "APPLE_API_PRIVATE_KEY: ${{ secrets.DEVHUD_APPLE_API_PRIVATE_KEY }}",
  ) &&
    macosWorkflow.includes(
      'export APPLE_API_KEY_PATH="${app_store_connect_key_path}"',
    ) &&
    !macosWorkflow.includes(
      "APPLE_API_KEY_PATH: ${{ secrets.",
    ),
  "protected signing must materialize the App Store Connect private key before exporting its path",
);
requireCondition(
  packageJson.scripts?.["gate:macos"] ===
    "node scripts/macos-gate.mjs" &&
    packageJson.scripts?.["test:macos-gate-contract"] ===
      "node --test scripts/macos-gate-contract.test.mjs",
  "the package must expose the macOS gate and its deterministic contract tests",
);

if (failures.length > 0) {
  throw new Error(
    `DevHud feasibility contracts failed:\n- ${failures.join("\n- ")}`,
  );
}

console.log(
  JSON.stringify({
    check: "devhud-feasibility-contracts",
    status: "passed",
    tauriRevision: revision,
    cliCefVersion: "3.0.0-alpha.6",
  }),
);
