import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { assertAndroidBackupExclusions, assertAndroidNativeBridge, assertMobileCi, assertMobileContracts, assertMobileTargets } from "./mobile-policy.mjs";

const appRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const mobileTargets = JSON.parse(readFileSync(join(appRoot, "mobile-platforms.json"), "utf8")).targets;

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

test("mobile policy requires checked Android persistence and app-level notification state", () => {
  const androidNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"), "utf8");
  assert.doesNotThrow(() => assertAndroidNativeBridge(androidNativeBridge));
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace(".commit()", ".apply()")), /writes and removals must confirm persistence/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("areNotificationsEnabled()", "isNotificationPolicyAccessGranted")), /app-level disablement/u);
});

test("mobile policy requires production and simulator iOS builds", () => {
  const workflow = readFileSync(join(appRoot, "../../.github/workflows/CI.yml"), "utf8");
  assert.doesNotThrow(() => assertMobileCi(workflow));
  const withoutDeviceBuild = workflow.replace("          - target: aarch64\n            runner: macos-15\n", "");
  assert.throws(() => assertMobileCi(withoutDeviceBuild), /iOS CI target aarch64/u);
  assert.throws(() => assertMobileCi(workflow.replace(" --no-sign", "")), /without signing/u);
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
