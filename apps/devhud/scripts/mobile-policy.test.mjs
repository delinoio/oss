import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { assertAndroidArtifactEntries, assertAndroidBackupExclusions, assertAndroidNativeBridge, assertAndroidNativeLibrary, assertAndroidPermissions, assertIosNativeBridge, assertMobileCi, assertMobileContracts, assertMobileDependencyClosures, assertMobileDependencyResolution, assertMobileTargets, mobileCargoTreeDigest } from "./mobile-policy.mjs";

const appRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const mobileTargets = JSON.parse(readFileSync(join(appRoot, "mobile-platforms.json"), "utf8")).targets;
const mobilePlatforms = JSON.parse(readFileSync(join(appRoot, "mobile-platforms.json"), "utf8"));

test("mobile policy validates every field in every immutable target tuple", () => {
  assert.doesNotThrow(() => assertMobileTargets(mobileTargets));
  for (const [index, target] of mobileTargets.entries()) {
    for (const field of ["id", "platform", "kind", "tauriTarget", "rustTarget", "architecture"]) {
      const changed = structuredClone(mobileTargets);
      changed[index][field] = `${target[field]}-changed`;
      assert.throws(() => assertMobileTargets(changed), new RegExp(`mobile target ${target.id} tuple changed`, "u"));
    }
  }
  const duplicate = structuredClone(mobileTargets);
  duplicate[1].id = duplicate[0].id;
  assert.throws(() => assertMobileTargets(duplicate), /target IDs must be unique/u);
});

test("mobile policy excludes private preferences and System WebView storage from every Android backup path", () => {
  const policies = {
    androidManifest: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/AndroidManifest.xml"), "utf8"),
    androidBackupRules: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/res/xml/backup_rules.xml"), "utf8"),
    androidDataExtractionRules: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/res/xml/data_extraction_rules.xml"), "utf8"),
  };
  assert.doesNotThrow(() => assertAndroidBackupExclusions(policies));
  assert.throws(() => assertAndroidBackupExclusions({ ...policies, androidDataExtractionRules: policies.androidDataExtractionRules.replace("devhud-secure-settings-v1.xml", "other.xml") }), /cloud-backup exclusion/u);
  assert.throws(() => assertAndroidBackupExclusions({ ...policies, androidBackupRules: policies.androidBackupRules.replace("devhud-diagnostics-cleanup-v1.xml", "other.xml") }), /full-backup exclusion/u);
  assert.throws(() => assertAndroidBackupExclusions({ ...policies, androidBackupRules: policies.androidBackupRules.replace('path="app_webview/"', 'path="other/"') }), /full-backup WebView exclusion/u);
  assert.throws(() => assertAndroidBackupExclusions({ ...policies, androidDataExtractionRules: policies.androidDataExtractionRules.replace('path="app_webview/"', 'path="other/"') }), /cloud-backup WebView exclusion/u);
  assert.throws(() => assertAndroidBackupExclusions({ ...policies, androidDataExtractionRules: policies.androidDataExtractionRules.replace(/(<device-transfer>[\s\S]*?)path="app_webview\/"/u, '$1path="other/"') }), /device-transfer WebView exclusion/u);
});

test("mobile policy requires lifecycle-owned Android persistence and native platform safeguards", () => {
  const androidNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"), "utf8").replaceAll("\r\n", "\n");
  assert.doesNotThrow(() => assertAndroidNativeBridge(androidNativeBridge));
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("secureSettingsExecutor.shutdown()", "Unit")), /executor must stop with the plugin lifecycle/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace(".commit()", ".apply()")), /must confirm persistence/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("updateAAD", "missingAAD")), /AES-GCM AAD/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("authenticateKey = false", "authenticateKey = true")), /legacy ciphertext/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('if (kind == "github-pat")', "if (false)")), /matching API-origin scope marker/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('(it.path != "" && it.path != "/")', 'it.path != "/"')), /root API-origin spellings/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('it.host == "[::1]"', "false")), /bracketed IPv6 loopback origins/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('issuer.scheme == "http" && loopback', "false")), /configured issuer paths and loopback HTTP/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("areNotificationsEnabled()", "isNotificationPolicyAccessGranted")), /app-level disablement/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("NotificationManager.IMPORTANCE_NONE", "NotificationManager.IMPORTANCE_LOW")), /channel disablement/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('PermissionState.PROMPT -> "not-determined"', 'PermissionState.PROMPT, PermissionState.PROMPT_WITH_RATIONALE -> "not-determined"')), /rationale-required/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("manager.notify(notificationId, 0, built)", "manager.notify(deckId.hashCode(), built)")), /distinct notification identities/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("it.notification.group == deckId", "false")), /every associated notification/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("Intent(activity.intent).setData(null)", "Intent(activity.intent)")), /activity intent/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replaceAll("peekAuthCallback", "missing")), /inspection must be non-destructive/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("if (diagnosticsExportPickerActive)", "if (false)")), /reject a concurrent picker/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("if (diagnosticsPurgesInProgress.get() > 0)", "if (false)")), /remain blocked until destructive secure purges finish/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("diagnosticsExportPickerActive = true", "diagnosticsExportPickerActive = false")), /concurrent picker|record the active picker/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("if (!diagnosticsExportPickerActive)", "if (false)")), /picker callbacks/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('            invoke.reject("storage-failure", "storage-failure")\n            return\n        }\n        try {\n            // Retain cleanup ownership', '            invoke.reject("storage-failure", "storage-failure")\n        }\n        try {\n            // Retain cleanup ownership')), /reject failed URI grants/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("            cleanupPendingDiagnosticsExport()\n            invoke.reject(\"storage-failure\", \"storage-failure\")\n        }\n    }\n\n    private fun retainDiagnosticsCleanup", "            if (pendingDiagnosticsCleanup == null) retainDiagnosticsCleanup(destination)\n            cleanupPendingDiagnosticsExport()\n            invoke.reject(\"storage-failure\", \"storage-failure\")\n        }\n    }\n\n    private fun retainDiagnosticsCleanup")), /must not retain cleanup state/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("takePersistableUriPermission", "missingPersistablePermission")), /persistable destination URI/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("releasePersistableUriPermission", "missingPersistablePermission")), /release-only transition|release its URI grant/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("if (hasPersistedDiagnosticsWriteGrant(destination))", "if (true)")), /already-released URI grant/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("putBoolean(diagnosticsCleanupReleaseOnlyKey, true).commit()", "putBoolean(diagnosticsCleanupReleaseOnlyKey, true).apply()")), /confirm persistence|release-only transition/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("return false\n            }\n        }\n        val removed", "Unit\n            }\n        }\n        val removed")), /preserve retry state/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("if (diagnosticsCleanupReleaseOnly) return forgetDiagnosticsCleanup()", "if (diagnosticsCleanupReleaseOnly) return false")), /already-released URI grant/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("if (!hasPersistedDiagnosticsWriteGrant(destination)) return false", "if (!hasPersistedDiagnosticsWriteGrant(destination)) return forgetDiagnosticsCleanup()")), /byte cleanup must preserve retry state/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('openFileDescriptor(destination, "wt")', 'openFileDescriptor(destination, "w")')), /explicitly truncate/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('requireNotNull(activity.contentResolver.openFileDescriptor(destination, "wt")).use { true }', 'requireNotNull(activity.contentResolver.openFileDescriptor(destination, "wt")).use { false }')), /treat success as complete/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replaceAll("FileNotFoundException", "MissingFileException")), /confirm destination absence/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('val destructivePurge = scope in setOf("logout", "account-deletion")', "val destructivePurge = false")), /reserve invalidation before invalidating active diagnostics pickers/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("diagnosticsPurgesInProgress.incrementAndGet()", "Unit")), /retain and release export invalidation/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("            } catch (error: Exception) {\n                diagnosticsPurgesInProgress.decrementAndGet()\n                throw error\n            }", "            }")), /across queued persistence and failures/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("                } finally {\n                    onComplete()\n                }", "                }")), /release purge state after executor completion/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("            if (!cleanupPendingDiagnosticsExport())", "            if (cleanupPendingDiagnosticsExport())")), /propagate diagnostics cleanup failures/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("storeIntent().resolveActivity(activity.packageManager)", "true")), /market handler/u);
});

test("mobile policy keeps native iOS origins aligned with normalized root URLs", () => {
  const iosNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/ios/Sources/DevhudNativePlugin.swift"), "utf8").replaceAll("\r\n", "\n");
  assert.doesNotThrow(() => assertIosNativeBridge(iosNativeBridge));
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("UIDevice.current.systemVersion", '"ios"')), /installed native OS version/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace('(url.path.isEmpty || url.path == "/")', 'url.path == "/"')), /root API-origin spellings/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("isSecureOrLoopback(issuer)", 'issuer.scheme == "https"')), /configured issuer paths and loopback HTTP/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replaceAll("legacyAccessGroupKey", "missingLegacyGroup")), /legacy application-group/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("guard markerStatus == errSecSuccess", "guard true")), /matching API-origin scope marker/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("rollbackCreatedGitHubPatScope(createdMarker)", "missingRollback(createdMarker)")), /roll back newly created scope markers/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("rollbackGitHubPatWrite(setting, previousData: previousGitHubPatData)", "true")), /restore or remove the shared GitHub PAT/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("pendingDiagnosticsCleanup = target", "pendingDiagnosticsCleanup = nil")), /cleanup must remain pending/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("if failed || !cleanupSucceeded", "if failed")), /fail closed/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace('if scope == "logout" || scope == "account-deletion"', "if true")), /preserve pending diagnostics exports/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("guard diagnosticsCleanupSucceeded else", "guard true else")), /propagate diagnostics cleanup failures/u);
});

test("mobile open URL handling accepts authentication callbacks only", () => {
  const nativePlugin = readFileSync(join(appRoot, "src-tauri/src/native_plugin.rs"), "utf8").replace(/\r\n?/gu, "\n");
  const start = nativePlugin.indexOf("#[cfg(mobile)]\n            if let tauri::RunEvent::Opened");
  const end = nativePlugin.indexOf("\n            }\n        })", start);
  const openedHandler = nativePlugin.slice(start, end);
  assert.ok(start >= 0 && end > start, "mobile opened handler must exist");
  assert.match(openedHandler, /offer_auth_callback/u);
  assert.doesNotMatch(openedHandler, /offer_deck_link/u);
});

test("Android release permissions enable System WebView networking", () => {
  const release = readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/AndroidManifest.xml"), "utf8");
  const debug = readFileSync(join(appRoot, "mobile/overrides/android/app/src/debug/AndroidManifest.xml"), "utf8");
  assert.doesNotThrow(() => assertAndroidPermissions(release, debug));
  assert.throws(() => assertAndroidPermissions(release.replace(/\s*<uses-permission android:name="android\.permission\.INTERNET" \/>/u, ""), debug), /System WebView networking/u);
});

test("mobile dependency resolution includes production default features", () => {
  assert.doesNotThrow(() => assertMobileDependencyResolution('"cargo", "tree", "--edges", "normal"'));
  assert.throws(() => assertMobileDependencyResolution('"cargo", "tree", "--no-default-features"'), /production default features/u);
});

test("mobile policy validates APK and App Bundle layouts", () => {
  const apkEntries = ["AndroidManifest.xml", "classes.dex", "lib/arm64-v8a/libdevhud_lib.so"];
  const aabEntries = ["BundleConfig.pb", "base/manifest/AndroidManifest.xml", "base/dex/classes.dex", "base/lib/arm64-v8a/libdevhud_lib.so"];
  assert.doesNotThrow(() => assertAndroidArtifactEntries(apkEntries, "arm64-v8a", "apk"));
  assert.doesNotThrow(() => assertAndroidArtifactEntries(aabEntries, "arm64-v8a", "aab"));
  assert.throws(() => assertAndroidArtifactEntries(aabEntries.filter((entry) => entry !== "BundleConfig.pb"), "arm64-v8a", "aab"), /Bundle configuration/u);
  assert.throws(() => assertAndroidArtifactEntries([...aabEntries, "base/lib/x86_64/libdevhud_lib.so"], "arm64-v8a", "aab"), /architecture changed/u);
  assert.throws(() => assertAndroidArtifactEntries([...aabEntries, "base/assets/chromium.pak"], "arm64-v8a", "aab"), /CEF or browser-extension/u);
});

test("mobile policy distinguishes embedded runtime metadata from CEF symbols", () => {
  assert.doesNotThrow(() => assertAndroidNativeLibrary("150.0.10+g8042e43+chromium-150.0.7871.101"));
  assert.throws(() => assertAndroidNativeLibrary("libcef.so"), /CEF symbols/u);
  assert.throws(() => assertAndroidNativeLibrary("cef_initialize"), /CEF symbols/u);
});

test("mobile policy pins normalized resolved dependency closures", () => {
  const tree = [
    "0devhud v0.1.0 (/checkout/apps/devhud/src-tauri) ",
    "1serde v1.0.0 default,derive",
    "2serde_derive v1.0.0 (proc-macro) default",
    "3host-only v1.0.0 default",
    "1serde v1.0.0 default,derive (*)",
  ].join("\n");
  const windowsRoot = String.raw`C:\a\oss\oss`;
  const windowsTree = tree.replace("/checkout/apps/devhud/src-tauri", String.raw`C:\a\oss\oss\apps\devhud\src-tauri`);
  assert.equal(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(tree.replaceAll("/checkout", "/other"), "/other"));
  assert.equal(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(windowsTree, windowsRoot));
  assert.equal(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(tree.replace("host-only v1.0.0", "other-host-only v1.0.0"), "/checkout"));
  assert.notEqual(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(`${tree}\n1reqwest v1.0.0 default`, "/checkout"));
  assert.doesNotThrow(() => assertMobileDependencyClosures(mobilePlatforms, mobilePlatforms.dependencyClosures));
  assert.throws(
    () => assertMobileDependencyClosures(mobilePlatforms, { ...mobilePlatforms.dependencyClosures, "aarch64-linux-android": "sha256-changed" }),
    /dependency closure changed/u,
  );
});

test("mobile policy requires production and simulator iOS builds", () => {
  const workflow = readFileSync(join(appRoot, "../../.github/workflows/CI.yml"), "utf8").replace(/\r\n?/gu, "\n");
  assert.doesNotThrow(() => assertMobileCi(workflow));
  assert.doesNotThrow(() => assertMobileCi(workflow.replaceAll("\n", "\r\n")));
  const withoutDeviceBuild = workflow.replace("          - target: aarch64\n            runner: macos-15\n", "");
  assert.throws(() => assertMobileCi(withoutDeviceBuild), /iOS CI target aarch64/u);
  assert.throws(() => assertMobileCi(workflow.replace("xcrun simctl list > /dev/null", "true")), /initialize simulator devices/u);
  assert.throws(() => assertMobileCi(workflow.replace(" --no-sign", "")), /without signing/u);
  assert.throws(() => assertMobileCi(workflow.replace("            artifacts: --apk --aab", "            artifacts: --apk")), /build --apk --aab/u);
  assert.throws(() => assertMobileCi(workflow.replace('--android-artifact "${aab_artifacts[0]}"', '--android-artifact "missing.aab"')), /inspect the generated App Bundle/u);
});

test("mobile policy rejects CEF leakage", () => {
  const base = {
    platforms: { schemaVersion: 1, identity: "io.delino.devhud", deepLinkScheme: "devhud", authCallback: "devhud://auth/callback", frontendDist: "../dist", minimumVersions: { ios: "16.0", androidApi: 29 }, targets: mobileTargets },
    tauri: { identifier: "io.delino.devhud", build: { frontendDist: "../dist" } },
    ios: { bundle: { iOS: { minimumSystemVersion: "16.0" } } },
    android: { bundle: { android: { minSdkVersion: 29 } } }, cargo: "", androidManifest: "android.permission.INTERNET", androidPluginManifest: "", androidNativeBridge: "", iosPlist: "", packageJson: { scripts: {} }, nativeBridge: "", app: "", workflow: "",
  };
  assert.throws(() => assertMobileContracts(base), /system-webview features/u);
});
