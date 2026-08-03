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
  androidBackupRules,
  androidDataExtractionRules,
  gradleWrapper,
  androidAuthBuild,
  androidAuthPlugin,
  androidMainActivity,
  iosAuthPlugin,
  iosEntitlements,
  iosInfo,
  iosProject,
  productionRegistry,
  authNativeSource,
  runtimeSource,
] = await Promise.all([
  read("src-tauri/tauri.android.conf.json"),
  read("src-tauri/Cargo.toml"),
  read("src-tauri/capabilities/mobile-main.json"),
  read("src-tauri/tauri.ios.conf.json"),
  read("package.json"),
  read("src-tauri/tauri.conf.json"),
  read("src-tauri/gen/android/app/build.gradle.kts"),
  read("src-tauri/gen/android/app/src/main/AndroidManifest.xml"),
  read("src-tauri/gen/android/app/src/main/res/xml/backup_rules.xml"),
  read("src-tauri/gen/android/app/src/main/res/xml/data_extraction_rules.xml"),
  read("src-tauri/gen/android/gradle/wrapper/gradle-wrapper.properties"),
  read("src-tauri/auth-bridge/android/build.gradle.kts"),
  read(
    "src-tauri/auth-bridge/android/src/main/java/dev/deli/devhud/auth/DevHudAuthPlugin.kt",
  ),
  read(
    "src-tauri/gen/android/app/src/main/java/dev/deli/devhud/MainActivity.kt",
  ),
  read(
    "src-tauri/auth-bridge/ios/Sources/DevHudAuthPlugin/DevHudAuthPlugin.swift",
  ),
  read("src-tauri/gen/apple/devhud_iOS/devhud_iOS.entitlements"),
  read("src-tauri/gen/apple/devhud_iOS/Info.plist"),
  read("src-tauri/gen/apple/project.yml"),
  read("src/tools/registry.ts"),
  read("src-tauri/src/auth_native.rs"),
  read("src-tauri/src/lib.rs"),
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
const iosDependencies =
  cargoManifest.match(
    /\[target\.'cfg\(target_os = "ios"\)'\.dependencies\]([\s\S]*?)(?=\n\[)/u,
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
  /objc2-foundation\s*=\s*\{[^}]*default-features\s*=\s*false[^}]*"NSError"[^}]*"NSString"[^}]*"NSURL"[^}]*"NSValue"[^}]*"std"[^}]*\}/u.test(
    iosDependencies,
  ) &&
    runtimeSource.includes("NSURLIsExcludedFromBackupKey") &&
    /fs::create_dir_all\(directory\)\?;[\s\S]*exclude_ios_persistence_from_backup\(directory\)\?;/u.test(
      runtimeSource,
    ),
  "iOS must exclude the local persistence directory from device backups",
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
  (androidManifest.match(/android\.permission\.INTERNET/gu) ?? []).length === 1 &&
    (androidManifest.match(/android\.intent\.category\.BROWSABLE/gu) ?? []).length === 2 &&
    (androidManifest.match(/android:autoVerify="true"/gu) ?? []).length === 2 &&
    androidManifest.includes('android:scheme="https"') &&
    androidManifest.includes('android:host="deli.dev"') &&
    androidManifest.includes('android:path="/auth/devhud/callback"') &&
    androidManifest.includes('android:path="/devhud/deck/open"') &&
    !androidManifest.includes("android:pathPrefix") &&
    !androidManifest.includes("android:pathPattern"),
  "Android must grant native networking and register only the exact verified callback and Deck action paths",
);
requireCondition(
  androidManifest.includes('android:name="dev.deli.devhud.widget.DevHudWidgetProvider"') &&
    (androidManifest.match(/<receiver\b/gu) ?? []).length === 1 &&
    androidManifest.includes("android.appwidget.action.APPWIDGET_UPDATE") &&
    androidManifest.includes("android.appwidget.provider") &&
    androidManifest.includes('android:exported="false"'),
  "the distributed Android manifest must register exactly the non-exported Deck widget receiver",
);
requireCondition(
  androidManifest.includes('android:allowBackup="false"') &&
    androidManifest.includes(
      'android:dataExtractionRules="@xml/data_extraction_rules"',
    ) &&
    androidManifest.includes('android:fullBackupContent="@xml/backup_rules"'),
  "the distributed Android host must disable backup and declare exclusions",
);
requireCondition(
  androidAuthBuild.includes(
    'implementation("androidx.security:security-crypto:1.1.0")',
  ) &&
    androidAuthPlugin.includes("MasterKey.KeyScheme.AES256_GCM") &&
    androidAuthPlugin.includes("EncryptedSharedPreferences.create") &&
    androidAuthPlugin.includes('"active-session"') &&
    androidAuthPlugin.includes('it.scheme == "https"') &&
    androidAuthPlugin.includes('it.host == "deli.dev"') &&
    androidAuthPlugin.includes('it.path == "/auth/devhud/callback"') &&
    androidAuthPlugin.includes("pendingCallback = null") &&
    /override fun onNewIntent\(intent: Intent\) \{\s*pendingCallback = pendingCallback \?: validatedCallback\(intent\.data\)\s*\}/u.test(
      androidAuthPlugin,
    ) &&
    androidMainActivity.includes("override fun onNewIntent(intent: Intent)") &&
    androidMainActivity.includes("super.onNewIntent(intent)"),
  "Android auth must use a Keystore-backed vault, preserve queued callbacks, and consume only the exact app link once",
);
requireCondition(
  /fn begin_mobile[\s\S]*?manager\s*\.begin[\s\S]*?take_callback\(\)[\s\S]*?cancel_pending\(\)/u.test(
    authNativeSource,
  ),
  "mobile auth must discard a stale callback before starting a new authorization transaction",
);
requireCondition(
  androidAuthPlugin.includes("fun openPullRequest") &&
    androidAuthPlugin.includes('target.host == "github.com"') &&
    androidAuthPlugin.includes('segments[2] == "pull"') &&
    iosAuthPlugin.includes("func openPullRequest") &&
    iosAuthPlugin.includes('target.host == "github.com"') &&
    iosAuthPlugin.includes('segments[2] == "pull"'),
  "mobile Deck PR handoff must use a distinct exact-shape GitHub system-browser command",
);
requireCondition(
  authNativeSource.includes('option_env!("DEVHUD_LOGTO_ENDPOINT")') &&
    authNativeSource.includes('option_env!("DEVHUD_LOGTO_APP_ID")'),
  "mobile auth must embed its public Logto issuer and app ID at Rust build time",
);
const backupDomains = [
  "root",
  "file",
  "database",
  "sharedpref",
  "external",
  "device_root",
  "device_file",
  "device_database",
  "device_sharedpref",
];
for (const domain of backupDomains) {
  requireCondition(
    androidBackupRules.includes(`<exclude domain="${domain}" path="." />`),
    `Android legacy backup rules must exclude the ${domain} domain`,
  );
  requireCondition(
    androidDataExtractionRules
      .split(`<exclude domain="${domain}" path="." />`).length === 3,
    `Android extraction rules must exclude the ${domain} domain from cloud and device transfer`,
  );
}
requireCondition(
  androidDataExtractionRules.includes("<cloud-backup>") &&
    androidDataExtractionRules.includes("<device-transfer>"),
  "Android extraction rules must exclude both cloud backup and device transfer",
);
requireCondition(
  gradleWrapper.includes(
    "distributionSha256Sum=bd71102213493060956ec229d946beee57158dbd89d0e62b91bca0fa2c5f3531",
  ),
  "the pinned Gradle distribution must retain its official SHA-256 checksum",
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
  iosProject.includes("DevHudWidgetExtension:") &&
    iosProject.includes("type: app-extension") &&
    iosProject.includes("dev.deli.devhud.widget") &&
    /target: DevHudWidgetExtension[\s\S]*?embed: true/u.test(iosProject) &&
    iosProject.includes("com.apple.developer.associated-domains") &&
    iosProject.includes("applinks:deli.dev"),
  "the distributed iOS project must embed the exact WidgetKit extension and declare only the DeliDev associated domain",
);
requireCondition(
  !iosInfo.includes("CFBundleURLTypes") &&
    !iosInfo.includes("CFBundleURLSchemes") &&
    iosEntitlements.includes("com.apple.developer.associated-domains") &&
    (iosEntitlements.match(/applinks:/gu) ?? []).length === 1 &&
    iosEntitlements.includes("applinks:deli.dev") &&
    iosEntitlements.includes("com.apple.security.application-groups") &&
    iosEntitlements.includes("group.dev.deli.devhud") &&
    !iosEntitlements.includes("dev.deli.devhud.widget"),
  "the distributed iOS target must have the shared App Group, exact associated domain, and no custom scheme or widget identity",
);
requireCondition(
  iosAuthPlugin.includes("kSecClassGenericPassword") &&
    iosAuthPlugin.includes("kSecAttrSynchronizable as String: false") &&
    iosAuthPlugin.includes("kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly") &&
    iosAuthPlugin.includes("SecItemUpdate") &&
    !iosAuthPlugin.includes("try clear()") &&
    iosAuthPlugin.includes('target.scheme == "https"') &&
    iosAuthPlugin.includes('target.path == "/oidc/auth"') &&
    runtimeSource.includes("tauri::RunEvent::Opened { urls }") &&
    runtimeSource.includes("auth::is_mobile_callback_boundary(&callback)") &&
    runtimeSource.includes(".accept_mobile_callback(callback)"),
  "iOS auth must atomically update a this-device-only Keychain item and consume the verified universal link natively",
);
requireCondition(
  (iosProject.split("\ntargets:\n")[1]?.match(/^ {2}[A-Za-z0-9_]+:\s*$/gmu) ?? [])
    .length === 3,
  "the iOS project must contain the app, widget core, and widget extension targets",
);

requireCondition(
  JSON.stringify(capability.permissions) ===
    JSON.stringify([
      "allow-get-runtime-info",
      "allow-read-settings",
      "allow-write-settings",
      "allow-read-shortcut-effective-state",
      "allow-write-shortcut-effective-state",
      "allow-read-widget-configuration",
      "allow-write-widget-configuration",
      "allow-export-diagnostics",
      "allow-reset-dev-hud",
      "allow-deck-connect",
      "allow-deck-device-id",
      "allow-deck-open-pull-request",
      "allow-get-auth-session",
      "allow-start-authentication",
      "allow-logout-authentication",
    ]),
  "mobile IPC must remain limited to scoped shell, authentication, and exact Deck commands",
);
requireCondition(
  runtimeSource.includes('"Unsupported"') &&
    !runtimeSource.includes("Managed by the App Store") &&
    !runtimeSource.includes("Managed by Google Play"),
  "mobile runtime diagnostics must report updates as unsupported",
);
requireCondition(
  productionRegistry.includes('toolId: "deck"') &&
    productionRegistry.includes("ToolPlatform.Ios") &&
    productionRegistry.includes("ToolPlatform.Android") &&
    productionRegistry.includes('toolId: "realqa"') &&
    productionRegistry.includes(
      "supportedPlatforms: new Set([ToolPlatform.Desktop])",
    ) &&
    /supportedOperatingSystems:\s*new Set\(\[\s*ToolOperatingSystem\.Macos,\s*ToolOperatingSystem\.Ubuntu,\s*ToolOperatingSystem\.Windows,\s*\]\)/u.test(
      productionRegistry,
    ) &&
    (productionRegistry.match(/defineTool\(\{/gu)?.length ?? 0) === 2,
  "production registration must contain cross-platform Deck and desktop-only RealQA",
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
    !/CFBundleURLSchemes/u.test(source) &&
      (!/(com\.apple\.developer\.associated-domains|android\.intent\.action\.VIEW)/u.test(source) ||
        ((source.match(/applinks:/gu) ?? []).length <= 1 &&
          (source.match(/android\.intent\.action\.VIEW/gu) ?? []).length <= 2)),
    `native project contains a prohibited deep-link registration: ${path}`,
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
    (source.match(/<receiver\b/gu) ?? []).length === 1 &&
      /DevHudWidgetProvider/u.test(source) &&
      /APPWIDGET_UPDATE/u.test(source) &&
      /android\.appwidget/u.test(source) &&
      (source.match(/android\.permission\.INTERNET/gu) ?? []).length <= 1 &&
      (source.match(/android\.intent\.category\.BROWSABLE/gu) ?? []).length <= 2 &&
      (source.match(/android:autoVerify/gu) ?? []).length <= 2 &&
      (!source.includes("android:autoVerify") ||
        (source.includes('android:scheme="https"') &&
          source.includes('android:host="deli.dev"') &&
          source.includes('android:path="/auth/devhud/callback"') &&
          source.includes('android:path="/devhud/deck/open"'))),
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
