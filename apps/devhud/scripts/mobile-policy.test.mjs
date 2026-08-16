import assert from "node:assert/strict";
import test from "node:test";

import { assertMobileContracts } from "./mobile-policy.mjs";

test("mobile policy rejects release networking and CEF leakage", () => {
  const base = {
    platforms: { schemaVersion: 1, identity: "io.delino.devhud", deepLinkScheme: "devhud", authCallback: "devhud://auth/callback", frontendDist: "../dist", minimumVersions: { ios: "16.0", androidApi: 29 }, targets: [] },
    tauri: { identifier: "io.delino.devhud", build: { frontendDist: "../dist" } },
    ios: { bundle: { iOS: { minimumSystemVersion: "16.0" } } },
    android: { bundle: { android: { minSdkVersion: 29 } } }, cargo: "", androidManifest: "android.permission.INTERNET", androidPluginManifest: "", iosPlist: "", packageJson: { scripts: {} }, nativeBridge: "", app: "", workflow: "",
  };
  assert.throws(() => assertMobileContracts(base), /target matrix/u);
  const six = ["ios-device-arm64", "ios-simulator-arm64", "ios-simulator-x64", "android-arm64", "android-armv7", "android-emulator-x64"].map((id) => ({ id, rustTarget: id }));
  assert.throws(() => assertMobileContracts({ ...base, platforms: { ...base.platforms, targets: six } }), /system-webview features/u);
});
