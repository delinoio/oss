const expectedMobileTargets = [
  { id: "ios-device-arm64", platform: "ios", kind: "production", tauriTarget: "aarch64", rustTarget: "aarch64-apple-ios", architecture: "arm64" },
  { id: "ios-simulator-arm64", platform: "ios", kind: "simulator", tauriTarget: "aarch64-sim", rustTarget: "aarch64-apple-ios-sim", architecture: "arm64" },
  { id: "ios-simulator-x64", platform: "ios", kind: "simulator", tauriTarget: "x86_64", rustTarget: "x86_64-apple-ios", architecture: "x64" },
  { id: "android-arm64", platform: "android", kind: "production", tauriTarget: "aarch64", rustTarget: "aarch64-linux-android", architecture: "arm64-v8a" },
  { id: "android-armv7", platform: "android", kind: "production", tauriTarget: "armv7", rustTarget: "armv7-linux-androideabi", architecture: "armeabi-v7a" },
  { id: "android-emulator-x64", platform: "android", kind: "emulator", tauriTarget: "x86_64", rustTarget: "x86_64-linux-android", architecture: "x86_64" },
];
const mobileTargetFields = ["id", "platform", "kind", "tauriTarget", "rustTarget", "architecture"];
const assert = (condition, message) => { if (!condition) throw new Error(message); };

export function assertMobileTargets(actualTargets) {
  assert(Array.isArray(actualTargets) && actualTargets.length === expectedMobileTargets.length, "mobile target matrix must have exactly six entries");
  const targets = new Map(actualTargets.map((target) => [target.id, target]));
  assert(targets.size === expectedMobileTargets.length, "mobile target IDs must be unique");
  for (const expected of expectedMobileTargets) {
    const actual = targets.get(expected.id);
    const fieldsAreExact = actual
      && mobileTargetFields.every((field) => actual[field] === expected[field])
      && Object.keys(actual).length === mobileTargetFields.length
      && Object.keys(actual).every((field) => mobileTargetFields.includes(field));
    assert(fieldsAreExact, `mobile target ${expected.id} tuple changed`);
  }
}

export function assertAndroidBackupExclusions({ androidManifest, androidBackupRules, androidDataExtractionRules }) {
  const securePreferenceExclusion = '<exclude domain="sharedpref" path="devhud-secure-settings-v1.xml" />';
  assert(androidManifest.includes('android:fullBackupContent="@xml/backup_rules"'), "Android full-backup policy is missing");
  assert(androidManifest.includes('android:dataExtractionRules="@xml/data_extraction_rules"'), "Android data-extraction policy is missing");
  assert((androidBackupRules.match(/<exclude domain="sharedpref" path="devhud-secure-settings-v1\.xml" \/>/gu) ?? []).length === 1, "Android full-backup secure-setting exclusion changed");
  for (const section of ["cloud-backup", "device-transfer"]) {
    const content = androidDataExtractionRules.match(new RegExp(`<${section}>([\\s\\S]*?)<\\/${section}>`, "u"))?.[1] ?? "";
    assert(content.includes(securePreferenceExclusion), `Android ${section} secure-setting exclusion changed`);
  }
}

export function assertMobileContracts({ platforms, tauri, ios, android, cargo, androidManifest, androidBackupRules, androidDataExtractionRules, androidPluginManifest, iosPlist, packageJson, nativeBridge, app, workflow }) {
  assert(platforms.schemaVersion === 1, "unsupported mobile platform schema");
  assert(platforms.identity === "io.delino.devhud" && tauri.identifier === platforms.identity, "mobile identity changed");
  assert(platforms.deepLinkScheme === "devhud", "deep-link scheme changed");
  assert(platforms.authCallback === "devhud://auth/callback", "auth callback changed");
  assert(platforms.frontendDist === "../dist" && tauri.build.frontendDist === platforms.frontendDist, "mobile frontend is not shared");
  assert(platforms.minimumVersions.ios === "16.0" && ios.bundle.iOS.minimumSystemVersion === "16.0", "iOS minimum must be 16.0");
  assert(platforms.minimumVersions.androidApi === 29 && android.bundle.android.minSdkVersion === 29, "Android minimum must be API 29");

  assertMobileTargets(platforms.targets);

  const mobileCargo = cargo.match(/\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]([\s\S]*?)(?=\n\[|$)/u)?.[1] ?? "";
  assert(mobileCargo.includes('features = ["wry"]'), "mobile Tauri system-webview features changed");
  assert(!/cef|chromium|chrome-extension/iu.test(mobileCargo), "CEF or browser-extension dependency leaked into the mobile dependency set");
  assert(/features = \["cef"/u.test(cargo), "desktop CEF contract was lost");

  assert(!androidManifest.includes("android.permission.INTERNET"), "release Android manifest must not grant networking");
  assert((androidManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidManifest.includes("android.permission.POST_NOTIFICATIONS"), "release Android permissions are not least-privileged");
  assert(!androidManifest.includes("LEANBACK") && !androidManifest.includes("FileProvider"), "unneeded Android surface was generated");
  assert((androidManifest.match(/android:scheme="devhud"/gu) ?? []).length === 1, "Android must register only one devhud scheme");
  assert(androidManifest.includes('android:host="auth" android:path="/callback"'), "Android auth callback filter changed");
  assertAndroidBackupExclusions({ androidManifest, androidBackupRules, androidDataExtractionRules });
  assert((androidPluginManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidPluginManifest.includes("android.permission.POST_NOTIFICATIONS"), "Android native bridge permissions are not least-privileged");
  assert((iosPlist.match(/<string>devhud<\/string>/gu) ?? []).length === 1, "iOS must register only one devhud scheme");
  assert(!/com\.apple\.developer\.|NSExtension/iu.test(iosPlist), "uncontracted iOS entitlement or extension detected");

  assert(packageJson.scripts["build:ios"] && packageJson.scripts["build:android"] && packageJson.scripts["mobile:generate"], "package-local mobile commands are incomplete");
  for (const operation of ["runtime.snapshot", "lifecycle.open-external", "secure.read", "secure.write", "notifications.request-permission", "updates.status", "widgets.replace-deck-snapshot"]) assert(nativeBridge.includes(`\"${operation}\"`), `typed bridge operation missing: ${operation}`);
  assert(nativeBridge.includes("readonly widgets: false"), "widget scope must remain bridge-only");
  assert(app.includes("mobile &&") && app.includes("copy.realqaMobileTitle"), "mobile RealQA unavailable state is missing");
  assert(app.includes("!mobile") && app.includes("ExternalLinkTarget.Issue"), "issue creation is not explicitly desktop-only");
  assert(workflow.includes("devhud-mobile-contracts") && workflow.includes("devhud-ios-simulator") && workflow.includes("devhud-android-emulator"), "mobile CI validation jobs are incomplete");
}
