import { createHash } from "node:crypto";

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

export function assertAndroidNativeBridge(androidNativeBridge) {
  assert(androidNativeBridge.includes("Executors.newSingleThreadExecutor()"), "Android secure-setting persistence must run off the command thread");
  assert((androidNativeBridge.match(/\.commit\(\)/gu) ?? []).length === 2, "Android secure-setting writes and removals must confirm persistence");
  assert(androidNativeBridge.includes('invoke.reject("storage-failure", "storage-failure"'), "Android secure-setting persistence failures must use storage-failure");
  assert(androidNativeBridge.includes("return@execute") && androidNativeBridge.includes("Base64.getDecoder().decode"), "Android secure-setting reads must map decoding and Keystore failures off-thread");
  assert(androidNativeBridge.includes("areNotificationsEnabled()"), "Android notification state must honor app-level disablement");
  assert(androidNativeBridge.includes("NotificationManager.IMPORTANCE_NONE"), "Android notification publication must honor channel disablement");
  assert(androidNativeBridge.includes("devhud_notification_channel_deck_changes"), "Android notification channels must use localized resources");
  assert(androidNativeBridge.includes("activity.intent = Intent(activity.intent).setData(null)"), "Android consumed auth callbacks must be removed from the activity intent");
  assert(androidNativeBridge.includes("storeIntent().resolveActivity(activity.packageManager)"), "Android update status must resolve a market handler");
}

export function mobileCargoTreeDigest(cargoTree, workspaceRoot) {
  const escapedRoot = workspaceRoot.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
  const workspacePath = new RegExp(` \\(${escapedRoot}/([^)]*)\\)`, "gu");
  const packages = [];
  let skippedProcMacroDepth;
  for (const rawLine of cargoTree.split("\n")) {
    const match = rawLine.match(/^(\d+)(.*)$/u);
    if (!match) continue;
    const depth = Number(match[1]);
    if (skippedProcMacroDepth !== undefined && depth > skippedProcMacroDepth) continue;
    skippedProcMacroDepth = undefined;
    // Proc macros and their dependencies execute on the Cargo host, not in the mobile artifact.
    if (match[2].includes("(proc-macro)")) {
      skippedProcMacroDepth = depth;
      continue;
    }
    const packageLine = match[2].trim().replace(/ \(\*\)$/u, "").replace(workspacePath, " (workspace:$1)");
    if (packageLine) packages.push(packageLine);
  }
  return `sha256-${createHash("sha256").update(`${[...new Set(packages)].sort().join("\n")}\n`).digest("hex")}`;
}

export function assertMobileDependencyClosures(platforms, actualClosures) {
  const targets = [...new Set(platforms.targets.map(({ rustTarget }) => rustTarget))].sort();
  assert(Object.keys(platforms.dependencyClosures ?? {}).sort().join("\n") === targets.join("\n"), "mobile dependency closure targets changed");
  for (const target of targets) {
    assert(platforms.dependencyClosures[target] === actualClosures[target], `mobile dependency closure changed for ${target}`);
  }
}

export function assertMobileCi(workflow) {
  const iosJob = workflow.match(/\n  devhud-ios-simulator:\n([\s\S]*?)(?=\n  devhud-android-emulator:)/u)?.[1] ?? "";
  for (const [target, runner] of [["aarch64", "macos-15"], ["aarch64-sim", "macos-15"], ["x86_64", "macos-15-intel"]]) {
    assert(iosJob.includes(`- target: ${target}\n            runner: ${runner}`), `iOS CI target ${target} must run on ${runner}`);
  }
  assert(iosJob.includes("if: ${{ matrix.target == 'x86_64' }}\n        run: xcrun simctl list > /dev/null"), "Intel iOS CI must initialize simulator devices");
  assert(iosJob.includes("ios build --target ${{ matrix.target }} --ci --no-sign"), "iOS CI must build every matrix target without signing");
}

export function assertMobileContracts({ platforms, tauri, ios, android, cargo, androidManifest, androidDebugManifest, androidBackupRules, androidDataExtractionRules, androidPluginManifest, androidNativeBridge, androidChannelEnglish, androidChannelKorean, iosNativeBridge, iosPlist, packageJson, nativeBridge, app, workflow }) {
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
  assert((androidDebugManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidDebugManifest.includes("android.permission.INTERNET"), "debug Android manifest must grant only development networking");
  assert((androidManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidManifest.includes("android.permission.POST_NOTIFICATIONS"), "release Android permissions are not least-privileged");
  assert(androidManifest.includes('android:scheme="market"'), "Android market handler visibility is missing");
  assert(!androidManifest.includes("LEANBACK") && !androidManifest.includes("FileProvider"), "unneeded Android surface was generated");
  assert((androidManifest.match(/android:scheme="devhud"/gu) ?? []).length === 1, "Android must register only one devhud scheme");
  assert(androidManifest.includes('android:host="auth" android:path="/callback"'), "Android auth callback filter changed");
  assertAndroidBackupExclusions({ androidManifest, androidBackupRules, androidDataExtractionRules });
  assertAndroidNativeBridge(androidNativeBridge);
  assert(androidChannelEnglish.includes("Deck changes") && androidChannelKorean.includes("Deck 변경사항"), "Android notification channel names must be bilingual");
  assert((androidPluginManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidPluginManifest.includes("android.permission.POST_NOTIFICATIONS"), "Android native bridge permissions are not least-privileged");
  assert((iosPlist.match(/<string>devhud<\/string>/gu) ?? []).length === 1, "iOS must register only one devhud scheme");
  assert(!/com\.apple\.developer\.|NSExtension/iu.test(iosPlist), "uncontracted iOS entitlement or extension detected");
  assert(iosNativeBridge.includes('invoke.reject("storage-failure", code: "storage-failure")'), "iOS Keychain failures must use storage-failure");
  assert(iosNativeBridge.includes('invoke.reject("permission-denied", code: "permission-denied")'), "iOS notification publication must honor authorization");
  assert(iosNativeBridge.includes("UNUserNotificationCenterDelegate") && iosNativeBridge.includes("willPresent notification"), "iOS foreground Deck notifications must be presented by a delegate");

  assert(packageJson.scripts["build:ios"] && packageJson.scripts["build:android"] && packageJson.scripts["mobile:generate"], "package-local mobile commands are incomplete");
  for (const operation of ["runtime.snapshot", "lifecycle.open-external", "secure.read", "secure.write", "notifications.request-permission", "updates.status", "widgets.replace-deck-snapshot"]) assert(nativeBridge.includes(`\"${operation}\"`), `typed bridge operation missing: ${operation}`);
  assert(nativeBridge.includes("readonly widgets: false"), "widget scope must remain bridge-only");
  assert(app.includes("mobile &&") && app.includes("copy.realqaMobileTitle"), "mobile RealQA unavailable state is missing");
  assert(app.includes("!mobile") && app.includes("ExternalLinkTarget.Issue"), "issue creation is not explicitly desktop-only");
  assert(workflow.includes("devhud-mobile-contracts") && workflow.includes("devhud-android-emulator"), "mobile CI validation jobs are incomplete");
  assertMobileCi(workflow);
}
