import { readFile } from "node:fs/promises";
import { resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const repositoryRoot = resolve(appRoot, "../..");
const revision = "f49ebda2fdba5755456b0f049e32593ca0ea331a";
const repository = "https://github.com/tauri-apps/tauri";

const paths = {
  cargoLock: resolve(repositoryRoot, "Cargo.lock"),
  cargoManifest: resolve(appRoot, "src-tauri/Cargo.toml"),
  capability: resolve(appRoot, "src-tauri/capabilities/probe.json"),
  packageLock: resolve(repositoryRoot, "pnpm-lock.yaml"),
  packageManifest: resolve(appRoot, "package.json"),
  rootCargoManifest: resolve(repositoryRoot, "Cargo.toml"),
  tauriConfig: resolve(appRoot, "src-tauri/tauri.conf.json"),
};

const [
  cargoLock,
  cargoManifest,
  capability,
  packageLock,
  packageManifest,
  rootCargoManifest,
  tauriConfig,
] = await Promise.all([
  readFile(paths.cargoLock, "utf8"),
  readFile(paths.cargoManifest, "utf8"),
  readFile(paths.capability, "utf8"),
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
    !capabilityJson.permissions?.includes("allow-probe-forbidden"),
  "the capability must allow the handshake and deny the forbidden command",
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
