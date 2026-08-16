export function assertMobileContracts({ platforms, tauri, ios, android, cargo, androidManifest, androidPluginManifest, iosPlist, packageJson, nativeBridge, app, workflow }) {
  const assert = (condition, message) => { if (!condition) throw new Error(message); };
  assert(platforms.schemaVersion === 1, "unsupported mobile platform schema");
  assert(platforms.identity === "io.delino.devhud" && tauri.identifier === platforms.identity, "mobile identity changed");
  assert(platforms.deepLinkScheme === "devhud", "deep-link scheme changed");
  assert(platforms.authCallback === "devhud://auth/callback", "auth callback changed");
  assert(platforms.frontendDist === "../dist" && tauri.build.frontendDist === platforms.frontendDist, "mobile frontend is not shared");
  assert(platforms.minimumVersions.ios === "16.0" && ios.bundle.iOS.minimumSystemVersion === "16.0", "iOS minimum must be 16.0");
  assert(platforms.minimumVersions.androidApi === 29 && android.bundle.android.minSdkVersion === 29, "Android minimum must be API 29");

  const targets = new Map(platforms.targets.map((target) => [target.id, target]));
  assert(targets.size === 6, "mobile target matrix must have exactly six entries");
  for (const id of ["ios-device-arm64", "ios-simulator-arm64", "ios-simulator-x64", "android-arm64", "android-armv7", "android-emulator-x64"]) assert(targets.has(id), `missing mobile target ${id}`);
  assert(!platforms.targets.some(({ rustTarget }) => rustTarget.includes("i686")), "uncontracted Android i686 target detected");

  const mobileCargo = cargo.match(/\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]([\s\S]*?)(?=\n\[|$)/u)?.[1] ?? "";
  assert(mobileCargo.includes('features = ["wry"]'), "mobile Tauri system-webview features changed");
  assert(!/cef|chromium|chrome-extension/iu.test(mobileCargo), "CEF or browser-extension dependency leaked into the mobile dependency set");
  assert(/features = \["cef"/u.test(cargo), "desktop CEF contract was lost");

  assert(!androidManifest.includes("android.permission.INTERNET"), "release Android manifest must not grant networking");
  assert((androidManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidManifest.includes("android.permission.POST_NOTIFICATIONS"), "release Android permissions are not least-privileged");
  assert(!androidManifest.includes("LEANBACK") && !androidManifest.includes("FileProvider"), "unneeded Android surface was generated");
  assert((androidManifest.match(/android:scheme="devhud"/gu) ?? []).length === 1, "Android must register only one devhud scheme");
  assert(androidManifest.includes('android:host="auth" android:path="/callback"'), "Android auth callback filter changed");
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
