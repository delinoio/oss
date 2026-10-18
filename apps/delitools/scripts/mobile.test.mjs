// SPDX-License-Identifier: Apache-2.0
import assert from "node:assert/strict";
import { generateKeyPairSync } from "node:crypto";
import { test } from "node:test";
import { configureAndroid, releaseConfig } from "./mobile.mjs";
import { publishAndroid } from "./publish-android.mjs";

test("generated Android signing uses environment values and rejects template drift", () => {
  const result = configureAndroid(
    '    buildTypes {\n        getByName("release") {\n        }\n    }',
  );
  assert.match(result, /signingConfig = signingConfigs.getByName\("release"\)/);
  assert.match(result, /System.getenv\("TAURI_ANDROID_KEY_PASSWORD"\)/);
  assert.match(
    result,
    /getByName\("release"\) \{\s+if \(System.getenv\("TAURI_ANDROID_KEYSTORE_PATH"\) != null\) \{\s+signingConfig = signingConfigs.getByName\("release"\)/,
  );
  assert.equal(configureAndroid(result), result);
  assert.throws(() => configureAndroid(""), /Unsupported/);
});
test("shared store build numbers reject invalid input and iOS component overflow", () => {
  for (const value of ["0", "-1", "1.2", "01", "10000", "2100000000", "$(id)"])
    assert.throws(() => releaseConfig(value));
  assert.equal(releaseConfig("9999").bundle.iOS.bundleVersion, "9999");
  assert.deepEqual(releaseConfig("42"), {
    bundle: { android: { versionCode: 42 }, iOS: { bundleVersion: "42" } },
  });
});
const { privateKey } = generateKeyPairSync("rsa", { modulusLength: 2048 });
const credentials = {
  client_email: "fixture@example.invalid",
  private_key: privateKey.export({ format: "pem", type: "pkcs8" }),
};
for (const mismatch of [false, true])
  test(`Google Play commits only the requested signed build (mismatch=${mismatch})`, async () => {
    const calls = [];
    const request = async (url, options) => {
      calls.push({ url, ...options });
      const body = url.includes("oauth2")
        ? { access_token: "fixture" }
        : url ===
            "https://androidpublisher.googleapis.com/androidpublisher/v3/applications/io.delino.delitools/edits"
          ? { id: "edit-1" }
          : url.includes("/bundles?")
            ? { versionCode: mismatch ? 43 : 42 }
            : {};
      return { ok: true, json: async () => body };
    };
    const promise = publishAndroid(
      Buffer.from("fixture"),
      "42",
      credentials,
      request,
    );
    if (mismatch) {
      await assert.rejects(promise, /does not match/);
      assert.equal(calls.at(-1).method, "DELETE");
    } else {
      await promise;
      assert.match(calls.at(-1).url, /:commit$/);
      assert.match(
        calls.find((call) => call.method === "PUT").body,
        /"track":"internal"/,
      );
    }
    assert.equal(
      calls.some((call) => call.url.includes(":commit")),
      !mismatch,
    );
  });
