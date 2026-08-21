import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { NativeBridgeError, NativeBridgeErrorCode, SecureSettingKind, isAuthCallback, nativeBridge, validateAuthenticationBrowserRequest, validateCaptureRequest, validateExternalRequest, validateGitHubPatReconciliation, validateSecretValue, validateSecureSettingRef, validateWidgetRequest } from "../src/native-bridge.ts";
import { ShortcutActionId, ShortcutKey, ShortcutModifier, ShortcutValidationCode, defaultDesktopShortcutBindings, parseDesktopShortcutBindings } from "../src/shortcuts.ts";

const fixtures = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../fixtures/deep-links.json"), "utf8"));
const tauriConfig = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/tauri.conf.json"), "utf8"));
const appCargo = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/Cargo.toml"), "utf8");
const nativePlugin = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/native_plugin.rs"), "utf8");
const cargoLock = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../../../Cargo.lock"), "utf8");
const desktopSecureStore = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/secure_store.rs"), "utf8");
const desktopHost = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/main.rs"), "utf8");
const nativeBridgeHost = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/bridge.rs"), "utf8");
const nativeShortcuts = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/shortcuts.rs"), "utf8");
const nativeCapture = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/src/capture.rs"), "utf8");
const windowsInstallerHooks = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/windows/hooks.nsh"), "utf8");
const androidBridgeHost = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"), "utf8");
const iosBridgeHost = readFileSync(join(dirname(fileURLToPath(import.meta.url)), "../src-tauri/mobile/ios/Sources/DevhudNativePlugin.swift"), "utf8");

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
  assert.doesNotThrow(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "work-profile", scopeId: "origin.scope" }));
  assert.throws(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "../escape", scopeId: "origin.scope" }), (error) => error instanceof NativeBridgeError && error.code === NativeBridgeErrorCode.InvalidArgument);
  assert.throws(() => validateSecureSettingRef({ kind: SecureSettingKind.GithubPat, profileId: "work-profile", scopeId: "../escape" }), NativeBridgeError);
  assert.throws(() => validateSecretValue("x".repeat(65 * 1024)), NativeBridgeError);
  assert.doesNotThrow(() => validateGitHubPatReconciliation("origin.scope", ["work-profile"]));
  assert.throws(() => validateGitHubPatReconciliation("origin.scope", ["work-profile", "work-profile"]), NativeBridgeError);
  assert.throws(() => validateGitHubPatReconciliation("origin.scope", ["../escape"]), NativeBridgeError);
});

test("widget bridge accepts only selected bounded Deck data and never accepts a credential payload", () => {
  const configuration = { version: 1, deckId: "018f47a2-7b3c-7def-8abc-1234567890ac", name: "Private", query: "repo:octo/private is:pr", repositories: [{ owner: "octo", name: "private" }], profileId: "work", profileKind: "fine-grained", scopeId: "origin.scope", language: "en" };
  assert.doesNotThrow(() => validateWidgetRequest({ operation: "widgets.enable-deck", configuration }));
  assert.throws(() => validateWidgetRequest({ operation: "widgets.enable-deck", configuration: { ...configuration, deckId: "../escape" } }), NativeBridgeError);
  assert.throws(() => validateWidgetRequest({ operation: "widgets.enable-deck", configuration: { ...configuration, repositories: [] } }), NativeBridgeError);
  assert.throws(() => validateWidgetRequest({ operation: "widgets.enable-deck", configuration: { ...configuration, repositories: [{ owner: "octo", name: "private" }, { owner: "OCTO", name: "PRIVATE" }] } }), NativeBridgeError);
  assert.throws(() => validateWidgetRequest({ operation: "widgets.enable-deck", configuration: Object.assign({}, configuration, { token: "must-not-cross" }) }), NativeBridgeError);
  const snapshot = { version: 1, deckId: configuration.deckId, query: configuration.query, counts: { total: 1, open: 1, draft: 0, merged: 0, closed: 0, bounded: false }, results: [{ nodeId: "PR_private", number: 1, title: "Private title", repository: "octo/private", state: "open", draft: false }], state: "fresh", lastSuccessfulAt: "2026-08-20T00:00:00.000Z", lastAttemptedAt: "2026-08-20T00:00:00.000Z", rate: null };
  assert.doesNotThrow(() => validateWidgetRequest({ operation: "widgets.replace-deck-snapshot", snapshot }));
  assert.throws(() => validateWidgetRequest({ operation: "widgets.replace-deck-snapshot", snapshot: Object.assign({}, snapshot, { credential: "must-not-cross" }) }), NativeBridgeError);
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
  assert.doesNotThrow(() => validateExternalRequest({ target: "fine-grained-pat", apiOrigin: "ignored" }));
  assert.doesNotThrow(() => validateExternalRequest({ target: "classic-pat", apiOrigin: "ignored" }));
  assert.throws(() => validateExternalRequest({ target: "authentication", apiOrigin: "http://example.com/" }), NativeBridgeError);
  assert.throws(() => validateExternalRequest({ target: "authentication", apiOrigin: "https://user@example.com/" }), NativeBridgeError);
});

test("all native hosts expose only the contracted PAT creation links", () => {
  for (const source of [desktopHost, androidBridgeHost, iosBridgeHost]) {
    assert.match(source, /personal-access-tokens\/new\?contents=read&issues=write&metadata=read&pull_requests=read/u);
    assert.match(source, /settings\/tokens\/new\?scopes=repo/u);
    assert.doesNotMatch(source, /target_name/u);
  }
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

test("Windows uninstall stops before file removal when Native Messaging cleanup fails", () => {
  const presence = windowsInstallerHooks.indexOf('IfFileExists "$INSTDIR\\devhud-native-messaging-host.exe" devhud_native_messaging_unregister devhud_native_messaging_unregister_missing');
  const unregister = windowsInstallerHooks.indexOf("nsExec::ExecToLog");
  const status = windowsInstallerHooks.indexOf("Pop $0", unregister);
  const success = windowsInstallerHooks.indexOf('StrCmp $0 "0" devhud_native_messaging_unregister_done', status);
  const failure = windowsInstallerHooks.indexOf("Abort", success);
  const missing = windowsInstallerHooks.indexOf("devhud_native_messaging_unregister_missing:", failure);
  const missingFailure = windowsInstallerHooks.indexOf("Abort", missing);
  const done = windowsInstallerHooks.indexOf("devhud_native_messaging_unregister_done:", missingFailure);
  assert(presence >= 0);
  assert(unregister > presence);
  assert(status > unregister);
  assert(success > status);
  assert(failure > success);
  assert(missing > failure);
  assert(missingFailure > missing);
  assert(done > missingFailure);
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

test("iOS runtime snapshots use the installed native OS version", () => {
  assert.match(iosBridgeHost, /case "runtime\.snapshot":[\s\S]*UIDevice\.current\.systemVersion/u);
  assert.match(nativeBridgeHost, /#\[cfg\(target_os = "ios"\)\][\s\S]*operation == "runtime\.snapshot"[\s\S]*native_plugin::request[\s\S]*ios_runtime_os_version/u);
});

test("desktop shortcuts persist only structured enums and reject unsafe candidates before invoking native code", async () => {
  assert.deepEqual(defaultDesktopShortcutBindings[ShortcutActionId.CommandPalette], {
    enabled: true,
    modifiers: [ShortcutModifier.RightPrimary],
    key: ShortcutKey.K,
  });
  const conflicting = structuredShortcuts();
  conflicting[ShortcutActionId.CaptureDisplay] = { ...conflicting[ShortcutActionId.CaptureDisplay], key: ShortcutKey.Digit2 };
  assert.throws(() => parseDesktopShortcutBindings(conflicting), { message: ShortcutValidationCode.Conflict });

  const previousWindow = globalThis.window;
  let invoked = false;
  globalThis.window = { __TAURI_INTERNALS__: { invoke: async () => { invoked = true; return { kind: "ok" }; } } };
  try {
    await assert.rejects(
      nativeBridge.request({ operation: "shortcuts.apply", bindings: conflicting }),
      { message: ShortcutValidationCode.Conflict },
    );
    assert.equal(invoked, false);
  } finally {
    globalThis.window = previousWindow;
  }
});

test("native shortcut boundary is physical-key-only and redacts unrelated input", () => {
  assert.match(nativeShortcuts, /NativeKey::RightPrimary/u);
  assert.match(nativeShortcuts, /NativeKey::LeftControl/u);
  assert.match(nativeShortcuts, /NativeKey::LeftMeta/u);
  assert.match(nativeShortcuts, /normalize_native_key\(platform/u);
  assert.match(nativeShortcuts, /_\s*=> None/u);
  assert.match(nativeShortcuts, /never exposes raw input/u);
  assert.doesNotMatch(nativeShortcuts, /println!|info!|debug!|warn!/u);
});

test("RealQA requests are bounded and capture data stays out of logs and recording APIs", () => {
  assert.doesNotThrow(() => validateCaptureRequest({ operation: "capture.start", actionId: ShortcutActionId.CaptureSelection, options: { delaySeconds: 5, selection: { x: -100, y: 0, width: 200, height: 100 } } }));
  assert.throws(() => validateCaptureRequest({ operation: "capture.start", actionId: ShortcutActionId.CommandPalette }), NativeBridgeError);
  assert.throws(() => validateCaptureRequest({ operation: "capture.open-draft", draftId: "../escape" }), NativeBridgeError);
  assert.throws(() => validateCaptureRequest({ operation: "capture.remove-browser-context", draftId: "01900000-0000-7000-8000-000000000001", expectedRevision: -1 }), NativeBridgeError);
  assert.match(nativeCapture, /Aes256Gcm/u);
  assert.match(nativeCapture, /MAX_IMAGES: usize = 10/u);
  assert.match(nativeCapture, /MAX_PNG_BYTES: usize = 50 \* 1024 \* 1024/u);
  assert.doesNotMatch(nativeCapture, /\.video_recorder\(|println!|debug!\(|info!\(/u);
  assert.match(tauriConfig.app.security.csp, /realqa:/u);
});

test("RealQA draft recovery runs only after the primary instance is claimed", () => {
  const singleInstance = desktopHost.indexOf("tauri_plugin_single_instance::init");
  const applicationSetup = desktopHost.indexOf(".setup(move |app|");
  const draftRecovery = desktopHost.indexOf("capture_recovery.with_draft_store(|store| store.recover())");
  assert(singleInstance >= 0);
  assert(applicationSetup > singleInstance);
  assert(draftRecovery > applicationSetup);
});

test("direct captures restore hidden or minimized windows only after acquisition", () => {
  const captureStart = nativeBridgeHost.indexOf('Some("capture.start")');
  const visibilityCheck = nativeBridgeHost.indexOf("capture_window.is_visible()", captureStart);
  const minimizedCheck = nativeBridgeHost.indexOf("capture_window.is_minimized()", captureStart);
  const acquisition = nativeBridgeHost.indexOf("let capture_result =", captureStart);
  const restoration = nativeBridgeHost.indexOf("capture_window.unminimize()", acquisition);
  assert(visibilityCheck > captureStart && visibilityCheck < acquisition);
  assert(minimizedCheck > captureStart && minimizedCheck < acquisition);
  assert(restoration > acquisition);
  assert.match(nativeBridgeHost.slice(restoration), /capture_window\.show\(\)/u);
  assert.match(nativeBridgeHost.slice(restoration), /capture_window\.set_focus\(\)/u);
});

test("capture failures emit only structured safe diagnostics", () => {
  const captureStart = nativeBridgeHost.indexOf('Some("capture.start")');
  const failureEvent = nativeBridgeHost.indexOf('event = "capture_failed"', captureStart);
  const failureReturn = nativeBridgeHost.indexOf("return Err(error_code)", failureEvent);
  assert(failureEvent > captureStart);
  assert(failureReturn > failureEvent);
  const diagnostic = nativeBridgeHost.slice(failureEvent, failureReturn);
  assert.match(diagnostic, /action = action_id/u);
  assert.match(diagnostic, /platform = capture\.adapter_platform\(\)/u);
  assert.match(diagnostic, /error_code = %error_code/u);
  assert.doesNotMatch(diagnostic, /options|image|editor|request/u);
});

function structuredShortcuts() {
  return Object.fromEntries(Object.entries(defaultDesktopShortcutBindings).map(([action, binding]) => [action, { ...binding, modifiers: [...binding.modifiers] }]));
}
