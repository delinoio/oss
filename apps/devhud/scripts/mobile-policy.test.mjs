import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { assertAndroidArtifactEntries, assertAndroidBackupExclusions, assertAndroidNativeBridge, assertIosNativeBridge, assertMobileCi, assertMobileContracts, assertMobileDependencyClosures, assertMobileDependencyResolution, assertMobileTargets, mobileCargoTreeDigest } from "./mobile-policy.mjs";

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

test("mobile policy excludes encrypted preferences from every Android backup path", () => {
  const policies = {
    androidManifest: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/AndroidManifest.xml"), "utf8"),
    androidBackupRules: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/res/xml/backup_rules.xml"), "utf8"),
    androidDataExtractionRules: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/res/xml/data_extraction_rules.xml"), "utf8"),
  };
  assert.doesNotThrow(() => assertAndroidBackupExclusions(policies));
  assert.throws(() => assertAndroidBackupExclusions({ ...policies, androidDataExtractionRules: policies.androidDataExtractionRules.replace("devhud-secure-settings-v1.xml", "other.xml") }), /cloud-backup secure-setting exclusion/u);
});

test("mobile policy requires lifecycle-owned Android persistence and native platform safeguards", () => {
  const androidNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"), "utf8");
  assert.doesNotThrow(() => assertAndroidNativeBridge(androidNativeBridge));
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("secureSettingsExecutor.shutdown()", "Unit")), /executor must stop with the plugin lifecycle/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace(".commit()", ".apply()")), /writes and removals must confirm persistence/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('(it.path != "" && it.path != "/")', 'it.path != "/"')), /root API-origin spellings/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('it.host == "[::1]"', "false")), /bracketed IPv6 loopback origins/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("areNotificationsEnabled()", "isNotificationPolicyAccessGranted")), /app-level disablement/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("NotificationManager.IMPORTANCE_NONE", "NotificationManager.IMPORTANCE_LOW")), /channel disablement/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('PermissionState.PROMPT -> "not-determined"', 'PermissionState.PROMPT, PermissionState.PROMPT_WITH_RATIONALE -> "not-determined"')), /rationale-required/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("manager.notify(notificationId, 0, built)", "manager.notify(deckId.hashCode(), built)")), /distinct notification identities/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("it.notification.group == deckId", "false")), /every associated notification/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("Intent(activity.intent).setData(null)", "Intent(activity.intent)")), /activity intent/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("storeIntent().resolveActivity(activity.packageManager)", "true")), /market handler/u);
});

test("mobile policy keeps native iOS origins aligned with normalized root URLs", () => {
  const iosNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/ios/Sources/DevhudNativePlugin.swift"), "utf8");
  assert.doesNotThrow(() => assertIosNativeBridge(iosNativeBridge));
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace('(url.path.isEmpty || url.path == "/")', 'url.path == "/"')), /root API-origin spellings/u);
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

test("mobile policy pins normalized resolved dependency closures", () => {
  const tree = [
    "0devhud v0.1.0 (/checkout/apps/devhud/src-tauri) ",
    "1serde v1.0.0 default,derive",
    "2serde_derive v1.0.0 (proc-macro) default",
    "3host-only v1.0.0 default",
    "1serde v1.0.0 default,derive (*)",
  ].join("\n");
  assert.equal(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(tree.replaceAll("/checkout", "/other"), "/other"));
  assert.equal(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(tree.replace("host-only v1.0.0", "other-host-only v1.0.0"), "/checkout"));
  assert.notEqual(mobileCargoTreeDigest(tree, "/checkout"), mobileCargoTreeDigest(`${tree}\n1reqwest v1.0.0 default`, "/checkout"));
  assert.doesNotThrow(() => assertMobileDependencyClosures(mobilePlatforms, mobilePlatforms.dependencyClosures));
  assert.throws(
    () => assertMobileDependencyClosures(mobilePlatforms, { ...mobilePlatforms.dependencyClosures, "aarch64-linux-android": "sha256-changed" }),
    /dependency closure changed/u,
  );
});

test("mobile policy requires production and simulator iOS builds", () => {
  const workflow = readFileSync(join(appRoot, "../../.github/workflows/CI.yml"), "utf8");
  assert.doesNotThrow(() => assertMobileCi(workflow));
  const withoutDeviceBuild = workflow.replace("          - target: aarch64\n            runner: macos-15\n", "");
  assert.throws(() => assertMobileCi(withoutDeviceBuild), /iOS CI target aarch64/u);
  assert.throws(() => assertMobileCi(workflow.replace("xcrun simctl list > /dev/null", "true")), /initialize simulator devices/u);
  assert.throws(() => assertMobileCi(workflow.replace(" --no-sign", "")), /without signing/u);
  assert.throws(() => assertMobileCi(workflow.replace("            artifacts: --apk --aab", "            artifacts: --apk")), /build --apk --aab/u);
  assert.throws(() => assertMobileCi(workflow.replace('--android-artifact "${aab_artifacts[0]}"', '--android-artifact "missing.aab"')), /inspect the generated App Bundle/u);
});

test("mobile policy rejects release networking and CEF leakage", () => {
  const base = {
    platforms: { schemaVersion: 1, identity: "io.delino.devhud", deepLinkScheme: "devhud", authCallback: "devhud://auth/callback", frontendDist: "../dist", minimumVersions: { ios: "16.0", androidApi: 29 }, targets: mobileTargets },
    tauri: { identifier: "io.delino.devhud", build: { frontendDist: "../dist" } },
    ios: { bundle: { iOS: { minimumSystemVersion: "16.0" } } },
    android: { bundle: { android: { minSdkVersion: 29 } } }, cargo: "", androidManifest: "android.permission.INTERNET", androidPluginManifest: "", androidNativeBridge: "", iosPlist: "", packageJson: { scripts: {} }, nativeBridge: "", app: "", workflow: "",
  };
  assert.throws(() => assertMobileContracts(base), /system-webview features/u);
});
