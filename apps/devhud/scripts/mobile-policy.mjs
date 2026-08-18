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
  const privatePreferenceExclusions = [
    '<exclude domain="sharedpref" path="devhud-secure-settings-v1.xml" />',
    '<exclude domain="sharedpref" path="devhud-diagnostics-cleanup-v1.xml" />',
  ];
  assert(androidManifest.includes('android:fullBackupContent="@xml/backup_rules"'), "Android full-backup policy is missing");
  assert(androidManifest.includes('android:dataExtractionRules="@xml/data_extraction_rules"'), "Android data-extraction policy is missing");
  for (const exclusion of privatePreferenceExclusions) {
    assert(androidBackupRules.includes(exclusion), `Android full-backup exclusion changed: ${exclusion}`);
  }
  for (const section of ["cloud-backup", "device-transfer"]) {
    const content = androidDataExtractionRules.match(new RegExp(`<${section}>([\\s\\S]*?)<\\/${section}>`, "u"))?.[1] ?? "";
    for (const exclusion of privatePreferenceExclusions) {
      assert(content.includes(exclusion), `Android ${section} exclusion changed: ${exclusion}`);
    }
  }
}

export function assertAndroidNativeBridge(androidNativeBridge) {
  const onDestroy = androidNativeBridge.match(/override fun onDestroy\(activity: AppCompatActivity\)[\s\S]*?(?=\n    @Command)/u)?.[0] ?? "";
  assert(androidNativeBridge.includes("Executors.newSingleThreadExecutor()"), "Android secure-setting persistence must run off the command thread");
  assert(onDestroy.includes("secureSettingsExecutor.shutdown()"), "Android secure-setting executor must stop with the plugin lifecycle");
  assert((androidNativeBridge.match(/\.commit\(\)/gu) ?? []).length === 7, "Android secure-setting and diagnostics-cleanup writes must confirm persistence");
  assert((androidNativeBridge.match(/updateAAD\(/gu) ?? []).length === 2, "Android secure values must authenticate their setting key as AES-GCM AAD");
  assert(androidNativeBridge.includes("AEADBadTagException") && androidNativeBridge.includes("authenticateKey = false") && androidNativeBridge.includes("encryptSecure(legacy, key)"), "Android must migrate authenticated legacy ciphertext before requiring key-bound AAD");
  assert(androidNativeBridge.includes('invoke.reject("storage-failure", "storage-failure"'), "Android secure-setting persistence failures must use storage-failure");
  assert(androidNativeBridge.includes("return@execute") && androidNativeBridge.includes("Base64.getDecoder().decode"), "Android secure-setting reads must map decoding and Keystore failures off-thread");
  assert(androidNativeBridge.includes('if (kind == "github-pat")') && androidNativeBridge.includes('preferences.contains(githubPatScopeKey(scopeId, profileId))'), "Android GitHub PAT reads must require the matching API-origin scope marker");
  assert(androidNativeBridge.includes('(it.path != "" && it.path != "/")'), "Android native navigation must accept both root API-origin spellings");
  assert(androidNativeBridge.includes('it.host == "[::1]"'), "Android native navigation must accept bracketed IPv6 loopback origins");
  assert(androidNativeBridge.includes('issuer.scheme == "http" && loopback') && androidNativeBridge.includes("destination.scheme == issuer.scheme"), "Android authentication must accept configured issuer paths and loopback HTTP while preserving same-origin navigation");
  assert(!androidNativeBridge.includes('issuer.path == ""') && !androidNativeBridge.includes('issuer.path == "/"'), "Android authentication must not restrict configured issuer paths");
  assert(androidNativeBridge.includes("areNotificationsEnabled()"), "Android notification state must honor app-level disablement");
  assert(androidNativeBridge.includes("NotificationManager.IMPORTANCE_NONE"), "Android notification publication must honor channel disablement");
  assert(androidNativeBridge.includes("devhud_notification_channel_deck_changes"), "Android notification channels must use localized resources");
  assert(androidNativeBridge.includes('PermissionState.PROMPT -> "not-determined"') && !androidNativeBridge.includes("PermissionState.PROMPT, PermissionState.PROMPT_WITH_RATIONALE"), "Android rationale-required notification permission must be denied");
  assert(androidNativeBridge.includes(".setGroup(deckId)") && androidNativeBridge.includes("manager.notify(notificationId, 0, built)"), "Android Deck changes must retain distinct notification identities");
  assert(androidNativeBridge.includes("manager.activeNotifications") && androidNativeBridge.includes("it.notification.group == deckId"), "Android Deck cancellation must remove every associated notification");
  assert(androidNativeBridge.includes("activity.intent = Intent(activity.intent).setData(null)"), "Android consumed auth callbacks must be removed from the activity intent");
  assert(androidNativeBridge.includes("peekAuthCallback") && androidNativeBridge.includes("pendingAuthCallback"), "Android auth callback inspection must be non-destructive");
  assert(androidNativeBridge.includes("pendingDiagnosticsCleanup") && androidNativeBridge.includes("takePersistableUriPermission"), "Android failed diagnostics cleanup must retain a persistable destination URI");
  assert(androidNativeBridge.includes("cleanupPendingDiagnosticsExport()") && androidNativeBridge.includes("FileNotFoundException"), "Android diagnostics cleanup must retry and confirm destination absence");
  assert(androidNativeBridge.includes('scope in setOf("logout", "account-deletion") && !cleanupPendingDiagnosticsExport()'), "Android destructive purges must propagate diagnostics cleanup failures");
  assert(androidNativeBridge.includes("storeIntent().resolveActivity(activity.packageManager)"), "Android update status must resolve a market handler");
}

export function assertIosNativeBridge(iosNativeBridge) {
  const exportDiagnostics = iosNativeBridge.match(/private func exportDiagnostics[\s\S]*?(?=\n    @discardableResult\n    private func cleanupDiagnosticsTemporaryDirectory)/u)?.[0] ?? "";
  const cleanupDiagnostics = iosNativeBridge.match(/private func cleanupDiagnosticsTemporaryDirectory[\s\S]*?(?=\n    @discardableResult\n    private func finishDiagnosticsExport)/u)?.[0] ?? "";
  const finishDiagnosticsExport = iosNativeBridge.match(/private func finishDiagnosticsExport[\s\S]*?(?=\n    func documentPicker)/u)?.[0] ?? "";
  const readSecure = iosNativeBridge.match(/private func readSecure[\s\S]*?(?=\n    private func writeSecure)/u)?.[0] ?? "";
  const writeSecure = iosNativeBridge.match(/private func writeSecure[\s\S]*?(?=\n    private func removeSecure)/u)?.[0] ?? "";
  const purgeSecure = iosNativeBridge.match(/private func purgeSecure[\s\S]*?(?=\n    private func permissionName)/u)?.[0] ?? "";
  assert(iosNativeBridge.includes('invoke.reject("storage-failure", code: "storage-failure")'), "iOS Keychain failures must use storage-failure");
  assert(iosNativeBridge.includes('invoke.reject("permission-denied", code: "permission-denied")'), "iOS notification publication must honor authorization");
  assert(iosNativeBridge.includes("UNUserNotificationCenterDelegate") && iosNativeBridge.includes("willPresent notification"), "iOS foreground Deck notifications must be presented by a delegate");
  assert(iosNativeBridge.includes('(url.path.isEmpty || url.path == "/")'), "iOS native navigation must accept both root API-origin spellings");
  assert(iosNativeBridge.includes("isSecureOrLoopback(issuer)") && iosNativeBridge.includes("destination.scheme == issuer.scheme"), "iOS authentication must accept configured issuer paths and loopback HTTP while preserving same-origin navigation");
  assert(!iosNativeBridge.includes('issuer.path == ""'), "iOS authentication must not restrict configured issuer paths");
  assert(iosNativeBridge.includes("kSecAttrAccessGroup") && iosNativeBridge.includes("kSecAttrSynchronizable"), "iOS secrets must use the shared non-synchronizing Keychain group");
  assert(iosNativeBridge.includes("legacyAccessGroupKey") && iosNativeBridge.includes("for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey]"), "iOS must migrate and purge legacy application-group Keychain items");
  assert(readSecure.includes("markerStatus == errSecItemNotFound") && readSecure.includes("guard markerStatus == errSecSuccess"), "iOS GitHub PAT reads must require the matching API-origin scope marker");
  assert((iosNativeBridge.match(/rollbackCreatedGitHubPatScope\(createdMarker\)/gu) ?? []).length === 2 && iosNativeBridge.includes("github_pat_scope_rollback_failed"), "iOS failed PAT writes must roll back newly created scope markers");
  assert(writeSecure.includes("previousGitHubPatData = previousData") && writeSecure.includes("rollbackGitHubPatWrite(setting, previousData: previousGitHubPatData)") && writeSecure.includes("github_pat_write_rollback_failed"), "iOS failed legacy cleanup must restore or remove the shared GitHub PAT");
  assert(cleanupDiagnostics.includes("pendingDiagnosticsCleanup = target") && cleanupDiagnostics.includes("error.code == .fileNoSuchFile"), "iOS failed diagnostics cleanup must remain pending while missing files count as clean");
  assert(exportDiagnostics.includes("guard cleanupDiagnosticsTemporaryDirectory()") && finishDiagnosticsExport.includes("if failed || !cleanupSucceeded"), "iOS diagnostics exports must fail closed when temporary cleanup fails");
  assert(purgeSecure.includes('if scope == "logout" || scope == "account-deletion"'), "iOS API-origin changes must preserve pending diagnostics exports");
  assert(purgeSecure.includes("guard diagnosticsCleanupSucceeded else"), "iOS destructive purges must propagate diagnostics cleanup failures");
  assert(iosNativeBridge.includes('UserDefaults(suiteName: appGroup)'), "iOS must bind the contracted App Group");
}

export function assertMobileDependencyResolution(verifier) {
  assert(!verifier.includes('"--no-default-features"'), "mobile dependency closure must include production default features");
}

export function assertAndroidPermissions(androidManifest, androidDebugManifest) {
  assert((androidDebugManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidDebugManifest.includes("android.permission.INTERNET"), "debug Android manifest must grant only development networking");
  assert((androidManifest.match(/<uses-permission/gu) ?? []).length === 2 && androidManifest.includes("android.permission.INTERNET") && androidManifest.includes("android.permission.POST_NOTIFICATIONS"), "release Android must grant only System WebView networking and notifications");
}

export function mobileCargoTreeDigest(cargoTree, workspaceRoot) {
  const normalizedCargoTree = cargoTree.replaceAll("\\", "/");
  const normalizedRoot = workspaceRoot.replaceAll("\\", "/");
  const escapedRoot = normalizedRoot.replace(/[.*+?^${}()|[\]\\]/gu, "\\$&");
  const workspacePath = new RegExp(` \\(${escapedRoot}/([^)]*)\\)`, "gu");
  const packages = [];
  let skippedProcMacroDepth;
  for (const rawLine of normalizedCargoTree.split("\n")) {
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

export function assertAndroidArtifactEntries(entries, abi, format) {
  assert(format === "apk" || format === "aab", `unsupported Android artifact format: ${format}`);
  const prefix = format === "aab" ? "base/" : "";
  const expectedLibrary = `${prefix}lib/${abi}/libdevhud_lib.so`;
  const nativeEntries = entries.filter((entry) => entry.startsWith(`${prefix}lib/`));
  assert(nativeEntries.length === 1 && nativeEntries[0] === expectedLibrary, `Android artifact architecture changed: expected only ${expectedLibrary}`);
  assert(entries.includes(format === "aab" ? "base/dex/classes.dex" : "classes.dex"), "Android artifact classes.dex is missing");
  assert(entries.includes(format === "aab" ? "base/manifest/AndroidManifest.xml" : "AndroidManifest.xml"), "Android artifact manifest is missing");
  if (format === "aab") assert(entries.includes("BundleConfig.pb"), "Android App Bundle configuration is missing");
  assert(!entries.some((entry) => /cef|chromium|chrome-extension|browser-extension/iu.test(entry)), "CEF or browser-extension file leaked into the Android artifact");
}

function workflowJob(workflow, name) {
  return workflow.match(new RegExp(`\\n  ${name}:\\n([\\s\\S]*?)(?=\\n  [a-z0-9-]+:|$)`, "u"))?.[1] ?? "";
}

export function assertMobileCi(workflow) {
  const normalizedWorkflow = workflow.replace(/\r\n?/gu, "\n");
  const contractsJob = workflowJob(normalizedWorkflow, "devhud-mobile-contracts");
  const iosJob = workflowJob(normalizedWorkflow, "devhud-ios-simulator");
  const androidJob = workflowJob(normalizedWorkflow, "devhud-android-emulator");
  for (const job of [contractsJob, iosJob, androidJob]) {
    assert(job.includes("uses: dorny/paths-filter@v4") && job.includes("- apps/devhud/**"), "mobile CI job must filter relevant DevHUD paths");
    assert(job.includes("EVENT_NAME: ${{ github.event_name }}") && job.includes('if [ "${EVENT_NAME}" = "workflow_dispatch" ] || [ "${DEVHUD_CHANGED}" = "true" ]; then'), "mobile CI job must run for manual dispatch or relevant paths");
    assert(job.includes("Skip (DevHUD mobile unaffected)") && job.includes("if: ${{ steps.gate.outputs.run == 'true' }}\n        uses: pnpm/action-setup@v5"), "mobile CI job must skip expensive setup when DevHUD is unaffected");
  }
  for (const [target, runner] of [["aarch64", "macos-15"], ["aarch64-sim", "macos-15"], ["x86_64", "macos-15-intel"]]) {
    assert(iosJob.includes(`- target: ${target}\n            runner: ${runner}`), `iOS CI target ${target} must run on ${runner}`);
  }
  assert(iosJob.includes("if: ${{ steps.gate.outputs.run == 'true' && matrix.target == 'x86_64' }}\n        run: xcrun simctl list > /dev/null"), "Intel iOS CI must initialize simulator devices");
  assert(iosJob.includes("ios build --target ${{ matrix.target }} --ci --no-sign"), "iOS CI must build every matrix target without signing");
  for (const [target, artifacts] of [["aarch64", "--apk --aab"], ["armv7", "--apk --aab"], ["x86_64", "--apk"]]) {
    assert(androidJob.includes(`- target: ${target}\n            artifacts: ${artifacts}`), `Android CI target ${target} must build ${artifacts}`);
  }
  assert(androidJob.includes("android build --target ${{ matrix.target }} ${{ matrix.artifacts }} --ci"), "Android CI must build each matrix artifact set");
  assert(androidJob.includes("if: ${{ steps.gate.outputs.run == 'true' && matrix.production }}") && androidJob.includes('--android-artifact "${aab_artifacts[0]}"'), "Android production CI must inspect the generated App Bundle");
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

  assertAndroidPermissions(androidManifest, androidDebugManifest);
  assert(androidManifest.includes('android:scheme="market"'), "Android market handler visibility is missing");
  assert(!androidManifest.includes("LEANBACK") && !androidManifest.includes("FileProvider"), "unneeded Android surface was generated");
  assert((androidManifest.match(/android:scheme="devhud"/gu) ?? []).length === 1, "Android must register only one devhud scheme");
  assert(androidManifest.includes('android:host="auth" android:path="/callback"'), "Android auth callback filter changed");
  assertAndroidBackupExclusions({ androidManifest, androidBackupRules, androidDataExtractionRules });
  assertAndroidNativeBridge(androidNativeBridge);
  assert(androidChannelEnglish.includes("Deck changes") && androidChannelKorean.includes("Deck 변경사항"), "Android notification channel names must be bilingual");
  assert((androidPluginManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidPluginManifest.includes("android.permission.POST_NOTIFICATIONS"), "Android native bridge permissions are not least-privileged");
  assert((iosPlist.match(/<string>devhud<\/string>/gu) ?? []).length === 1, "iOS must register only one devhud scheme");
  assert(iosPlist.includes("DevHudLegacyKeychainAccessGroup") && iosPlist.includes("$(AppIdentifierPrefix)io.delino.devhud"), "iOS legacy Keychain migration group changed");
  assert(!/com\.apple\.developer\.|NSExtension/iu.test(iosPlist), "uncontracted iOS entitlement or extension detected");
  assertIosNativeBridge(iosNativeBridge);

  assert(packageJson.scripts["build:ios"] && packageJson.scripts["build:android"] && packageJson.scripts["mobile:generate"], "package-local mobile commands are incomplete");
  for (const operation of ["runtime.snapshot", "lifecycle.open-external", "auth.peek-pending-callback", "auth.take-pending-callback", "secure.read", "secure.write", "notifications.request-permission", "updates.status", "widgets.replace-deck-snapshot"]) assert(nativeBridge.includes(`\"${operation}\"`), `typed bridge operation missing: ${operation}`);
  assert(nativeBridge.includes("readonly widgets: false"), "widget scope must remain bridge-only");
  assert(app.includes("mobile &&") && app.includes("copy.realqaMobileTitle"), "mobile RealQA unavailable state is missing");
  assert(app.includes("!mobile") && app.includes("ExternalLinkTarget.Issue"), "issue creation is not explicitly desktop-only");
  assert(workflow.includes("devhud-mobile-contracts") && workflow.includes("devhud-android-emulator"), "mobile CI validation jobs are incomplete");
  assertMobileCi(workflow);
}
