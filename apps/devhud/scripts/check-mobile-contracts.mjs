import { readdir, readFile } from "node:fs/promises";
import { extname, resolve } from "node:path";

const appRoot = resolve(import.meta.dirname, "..");
const nativeRoot = resolve(appRoot, "src-tauri");
const androidRoot = resolve(nativeRoot, "gen/android");
const appleRoot = resolve(nativeRoot, "gen/apple");
const failures = [];

function requireCondition(condition, message) {
  if (!condition) failures.push(message);
}

async function read(relativePath) {
  return readFile(resolve(appRoot, relativePath), "utf8");
}

async function collectFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = resolve(directory, entry.name);
    if (entry.isDirectory()) {
      if (
        [".gradle", "build", "DerivedData", "Externals", "generated", "jniLibs"].includes(
          entry.name,
        )
      ) {
        continue;
      }
      files.push(...(await collectFiles(path)));
    } else {
      if (path.endsWith("/src/main/assets/tauri.conf.json")) continue;
      files.push(path);
    }
  }
  return files;
}

const [
  androidConfigSource,
  cargoManifest,
  capabilitySource,
  iosConfigSource,
  packageSource,
  tauriConfigSource,
  androidBuild,
  androidManifest,
  iosEntitlements,
  iosInfo,
  iosProject,
  productionRegistry,
] = await Promise.all([
  read("src-tauri/tauri.android.conf.json"),
  read("src-tauri/Cargo.toml"),
  read("src-tauri/capabilities/main.json"),
  read("src-tauri/tauri.ios.conf.json"),
  read("package.json"),
  read("src-tauri/tauri.conf.json"),
  read("src-tauri/gen/android/app/build.gradle.kts"),
  read("src-tauri/gen/android/app/src/main/AndroidManifest.xml"),
  read("src-tauri/gen/apple/devhud_iOS/devhud_iOS.entitlements"),
  read("src-tauri/gen/apple/devhud_iOS/Info.plist"),
  read("src-tauri/gen/apple/project.yml"),
  read("src/tools/registry.ts"),
]);

const androidConfig = JSON.parse(androidConfigSource);
const capability = JSON.parse(capabilitySource);
const iosConfig = JSON.parse(iosConfigSource);
const packageJson = JSON.parse(packageSource);
const tauriConfig = JSON.parse(tauriConfigSource);
const mobileDependencies =
  cargoManifest.match(
    /\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]([\s\S]*?)(?=\n\[)/u,
  )?.[1] ?? "";

requireCondition(
  tauriConfig.identifier === "dev.deli.devhud",
  "the common application identifier must be dev.deli.devhud",
);
requireCondition(
  androidConfig.bundle?.android?.minSdkVersion === 29,
  "Android must require Android 10/API 29",
);
requireCondition(
  iosConfig.bundle?.iOS?.minimumSystemVersion === "17.0",
  "iOS must require iOS 17.0",
);
for (const [platform, config] of [
  ["Android", androidConfig],
  ["iOS", iosConfig],
]) {
  requireCondition(
    JSON.stringify(config.build?.features) ===
      JSON.stringify(["mobile-system-webview", "custom-protocol"]),
    `${platform} must select only the mobile-system-webview feature`,
  );
}
requireCondition(
  /tauri-runtime-wry\s*=\s*\{[^}]*default-features\s*=\s*false[^}]*optional\s*=\s*true[^}]*\}/u.test(
    mobileDependencies,
  ),
  "mobile dependencies must select only standard Tauri WRY/system webviews",
);
requireCondition(
  !mobileDependencies.includes('"cef"') &&
    !mobileDependencies.includes("tauri-runtime-cef") &&
    !mobileDependencies.includes("tauri-cef"),
  "mobile dependencies must never enable CEF",
);
requireCondition(
  cargoManifest.includes(
    'desktop-cef = ["dep:tauri", "dep:tauri-runtime-cef", "tauri/devtools"]',
  ) &&
    cargoManifest.includes(
      'mobile-system-webview = ["dep:tauri", "dep:tauri-runtime-wry"]',
    ),
  "desktop CEF and mobile system webviews must use isolated dependency edges",
);
requireCondition(
  packageJson.devDependencies?.["@tauri-apps/cli-cef"] === "3.0.0-alpha.6" &&
    packageJson.devDependencies?.["@tauri-apps/cli-mobile"] ===
      "npm:@tauri-apps/cli@2.11.4",
  "desktop and mobile CLIs must retain their exact independent pins",
);

requireCondition(
  androidBuild.includes('namespace = "dev.deli.devhud"') &&
    androidBuild.includes('applicationId = "dev.deli.devhud"'),
  "the Android project must use dev.deli.devhud",
);
for (const abi of ["arm64-v8a", "armeabi-v7a", "x86_64"]) {
  requireCondition(
    androidBuild.includes(`"${abi}"`),
    `the Android project must declare ${abi}`,
  );
}
requireCondition(
  androidBuild.includes("minSdk = 29"),
  "the generated Android project must preserve minSdk 29",
);
requireCondition(
  !androidManifest.includes("android.permission.INTERNET") &&
    !androidManifest.includes("android.intent.category.BROWSABLE") &&
    !androidManifest.includes("android:scheme=") &&
    !androidManifest.includes("android:autoVerify"),
  "the distributed Android manifest must not grant network access or register deep links",
);
requireCondition(
  !/<receiver\b/u.test(androidManifest) &&
    !androidManifest.includes("AppWidgetProvider"),
  "the distributed Android manifest must not register an AppWidgetProvider",
);

requireCondition(
  iosProject.includes("PRODUCT_BUNDLE_IDENTIFIER: dev.deli.devhud") &&
    iosProject.includes("iOS: 17.0"),
  "the iOS project must preserve its identifier and iOS 17 deployment target",
);
for (const architecture of ["arm64", "x86_64"]) {
  requireCondition(
    iosProject.includes(architecture),
    `the iOS project must declare ${architecture}`,
  );
}
requireCondition(
  !iosProject.includes("WidgetKit") &&
    !iosProject.includes(".appex") &&
    !iosProject.includes("dev.deli.devhud.widget") &&
    !iosProject.includes("com.apple.developer.associated-domains"),
  "the distributed iOS project must not embed widgets or associated domains",
);
requireCondition(
  !iosInfo.includes("CFBundleURLTypes") &&
    !iosInfo.includes("CFBundleURLSchemes") &&
    !iosEntitlements.includes("com.apple.developer.associated-domains") &&
    !iosEntitlements.includes("com.apple.security.application-groups"),
  "the distributed iOS target must have no deep-link or shared-widget entitlement",
);
requireCondition(
  (iosProject.split("\ntargets:\n")[1]?.match(/^ {2}[A-Za-z0-9_]+:\s*$/gmu) ?? [])
    .length === 1,
  "the iOS project must contain exactly one application target",
);

requireCondition(
  JSON.stringify(capability.permissions) ===
    JSON.stringify([
      "allow-get-runtime-info",
      "allow-read-settings",
      "allow-write-settings",
      "allow-read-widget-configuration",
      "allow-write-widget-configuration",
    ]),
  "mobile IPC must remain limited to diagnostics and the two versioned records",
);
requireCondition(
  /export const productionTools:\s*readonly ToolDefinition\[\]\s*=\s*\[\];/u.test(
    productionRegistry,
  ),
  "production tool registration must remain empty",
);

const nativeFiles = [
  ...(await collectFiles(androidRoot)),
  ...(await collectFiles(appleRoot)),
];
const textExtensions = new Set([
  ".h",
  ".java",
  ".json",
  ".kt",
  ".m",
  ".mm",
  ".pbxproj",
  ".plist",
  ".swift",
  ".xml",
  ".yml",
]);
for (const path of nativeFiles.filter((file) =>
  textExtensions.has(extname(file)),
)) {
  const source = await readFile(path, "utf8");
  requireCondition(
    !/https?:\/\/(?!(?:schemas\.android\.com|www\.apple\.com\/DTDs\/))/u.test(
      source,
    ),
    `native project contains an unintended network endpoint: ${path}`,
  );
  requireCondition(
    !/(CFBundleURLSchemes|com\.apple\.developer\.associated-domains|android\.intent\.action\.VIEW|AppWidgetProvider|WidgetKit)/u.test(
      source,
    ),
    `native project contains a prohibited deep-link or widget registration: ${path}`,
  );
}

const mergedManifestRoot = resolve(
  androidRoot,
  "app/build/intermediates/merged_manifests",
);
const mergedManifestFiles = await collectFiles(mergedManifestRoot).catch(
  (error) => {
    if (error.code === "ENOENT") return [];
    throw error;
  },
);
for (const path of mergedManifestFiles.filter((file) =>
  file.endsWith("AndroidManifest.xml"),
)) {
  const source = await readFile(path, "utf8");
  requireCondition(
    !/(android\.permission\.INTERNET|android\.intent\.category\.BROWSABLE|android:scheme=|android:autoVerify|AppWidgetProvider|APPWIDGET_UPDATE|android\.appwidget)/u.test(
      source,
    ),
    `merged Android artifact contains network, deep-link, or app-widget authority: ${path}`,
  );
}

if (failures.length > 0) {
  throw new Error(`DevHud mobile contracts failed:\n- ${failures.join("\n- ")}`);
}

console.log(
  JSON.stringify({
    applicationId: "dev.deli.devhud",
    check: "devhud-mobile-contracts",
    minimumAndroidApi: 29,
    minimumIosVersion: "17.0",
    status: "passed",
  }),
);
