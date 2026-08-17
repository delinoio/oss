import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, isAuthCallback, nativeBridge, validateAuthenticationBrowserRequest, validateExternalRequest, validateSecretValue, validateSecureSettingRef } from "../src/native-bridge.ts";

const fixtures = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../fixtures/deep-links.json"), "utf8"));
const tauriConfig = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/tauri.conf.json"), "utf8"));
const appCargo = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/Cargo.toml"), "utf8");
const nativePlugin = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/native_plugin.rs"), "utf8");
const cargoLock = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../../../Cargo.lock"), "utf8");
const desktopSecureStore = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/secure_store.rs"), "utf8");
const desktopHost = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/main.rs"), "utf8");
const nativeBridgeHost = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/bridge.rs"), "utf8");

test("deep-link fixtures accept only the contracted auth callback", () => {
  assert.deepEqual(tauriConfig.plugins["deep-link"].desktop.schemes, ["devhud"]);
  assert.match(appCargo, /tauri-plugin-deep-link = "=2\.4\.9"/u);
  assert.match(appCargo, /tauri-plugin-single-instance = \{ version = "=2\.4\.3", features = \["deep-link"\] \}/u);
  assert(desktopHost.indexOf("tauri_plugin_single_instance::init") < desktopHost.indexOf("tauri_plugin_deep_link::init"));
  assert.match(desktopHost, /deep_link\(\)\.get_current/u);
  assert.match(desktopHost, /deep_link\(\)\.on_open_url/u);
  assert.match(nativePlugin, /offer_auth_callback/u);
  for (const candidate of fixtures.accepted) assert.equal(isAuthCallback(candidate), true, candidate);
  for (const candidate of fixtures.rejected) assert.equal(isAuthCallback(candidate), false, candidate);
});

test("secure setting references and values are bounded before native invocation", () => {
  assert.doesNotThrow(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "work-profile" }));
  assert.throws(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "../escape" }), (error) => error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.InvalidArgument);
  assert.throws(() => validateSecretValue("x".repeat(65 * 1024)), NativeBridgeError);
});

test("desktop secure storage resolves Keychain, Credential Manager, and Secret Service without plaintext fallback", () => {
  for (const provider of ["apple-native-keyring-store", "windows-native-keyring-store", "zbus-secret-service-keyring-store"]) {
    assert.match(cargoLock, new RegExp(`name = "${provider}"`, "u"));
  }
  assert.match(desktopSecureStore, /Entry::new\(SERVICE/u);
  assert.doesNotMatch(desktopSecureStore, /(?:std::fs|File::create|write\()/u);
});

test("external navigation is restricted to account destinations", () => {
  assert.doesNotThrow(() => validateExternalRequest({ target: "authentication", apiOrigin: "https://api.delino.io/" }));
  assert.doesNotThrow(() => validateExternalRequest({ target: "authentication", apiOrigin: "http://127.0.0.1:8787/" }));
  assert.doesNotThrow(() => validateExternalRequest({ target: "pat", apiOrigin: "ignored" }));
  assert.throws(() => validateExternalRequest({ target: "authentication", apiOrigin: "http://example.com/" }), NativeBridgeError);
  assert.throws(() => validateExternalRequest({ target: "authentication", apiOrigin: "https://user@example.com/" }), NativeBridgeError);
});

test("authentication navigation is restricted to the discovered HTTPS issuer", () => {
  assert.doesNotThrow(() => validateAuthenticationBrowserRequest({ issuer: "https://identity.example/oidc", url: "https://identity.example/oidc/auth?state=opaque" }));
  assert.doesNotThrow(() => validateAuthenticationBrowserRequest({ issuer: "http://127.0.0.1:3001/oidc", url: "http://127.0.0.1:3001/oidc/auth?state=opaque" }));
  assert.throws(() => validateAuthenticationBrowserRequest({ issuer: "https://identity.example/oidc", url: "https://identity.example/auth" }), NativeBridgeError);
  assert.throws(() => validateAuthenticationBrowserRequest({ issuer: "https://identity.example/oidc", url: "https://identity.example/oidc-attacker/auth" }), NativeBridgeError);
  assert.throws(() => validateAuthenticationBrowserRequest({ issuer: "https://identity.example/", url: "https://attacker.example/oidc/auth" }), NativeBridgeError);
  assert.throws(() => validateAuthenticationBrowserRequest({ issuer: "http://identity.example/", url: "http://identity.example/oidc/auth" }), NativeBridgeError);
});

test("desktop authentication uses the diagnosed bounded system opener", () => {
  assert.match(desktopHost, /async fn open_system_browser/u);
  assert.match(nativeBridgeHost, /crate::open_system_browser\(destination\.to_string\(\)\)\s+\.await/u);
  assert.doesNotMatch(nativeBridgeHost, /open::that_detached/u);
});

test("desktop secure writes preserve credentials across bounded Windows storage and index failures", () => {
  assert.match(desktopSecureStore, /WINDOWS_CREDENTIAL_CHUNK_BYTES: usize = 1024/u);
  assert.match(desktopSecureStore, /let previous = read_value/u);
  assert.match(desktopSecureStore, /Some\(previous\) => write_value/u);
  assert.match(desktopSecureStore, /secure_store_write_rollback_failed/u);
});

test("Tauri rejection codes become typed native bridge errors", async () => {
  const previousWindow = globalThis.window;
  globalThis.window = { __TAURI_INTERNALS__: { invoke: async () => { throw NativeBridgeErrorCode.NotConfigured; } } };
  try {
    await assert.rejects(
      nativeBridge.request({ operation: "updates.open-store" }),
      (error) => error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.NotConfigured,
    );
  } finally {
    globalThis.window = previousWindow;
  }
});
