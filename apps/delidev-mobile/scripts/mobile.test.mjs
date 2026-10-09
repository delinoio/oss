// SPDX-License-Identifier: Apache-2.0
import { test } from "node:test";
import assert from "node:assert/strict";
import { androidManifest, iosBuildScript, execution } from "./mobile.mjs";
test("mobile generated overlays prohibit cleartext and backups and retain keyboard resize", () => {
  const m = androidManifest(
    '<uses-permission android:name="android.permission.INTERNET" />\n<application\n android:usesCleartextTraffic="${usesCleartextTraffic}"><activity android:launchMode="singleTask"/></application>',
  );
  assert.match(m, /allowBackup="false"/);
  assert.match(m, /usesCleartextTraffic="false"/);
  assert.match(m, /adjustResize/);
  assert.match(m, /POST_NOTIFICATIONS/);
});
test("all four native targets remain actual mobile build commands", () => {
  assert.equal(execution("ios-simulator").target, "aarch64-apple-ios-sim");
  assert.equal(execution("ios-device").target, "aarch64-apple-ios");
  assert.deepEqual(execution("android-bundle").args.slice(-4), [
    "--target",
    "aarch64",
    "armv7",
    "--aab",
  ]);
  assert.throws(() => execution("desktop"));
  assert.match(
    iosBuildScript("script: cargo tauri ios xcode-script"),
    /test -s/,
  );
});
