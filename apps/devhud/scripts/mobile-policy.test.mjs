import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

import { assertAndroidArtifactEntries, assertAndroidBackupExclusions, assertAndroidNativeBridge, assertAndroidNativeLibrary, assertAndroidPermissions, assertAndroidWidgetJobService, assertIosNativeBridge, assertMobileCi, assertMobileContracts, assertMobileDependencyClosures, assertMobileDependencyResolution, assertMobileTargets, assertNativeWidgetPullRequestMetadata, mobileCargoTreeDigest } from "./mobile-policy.mjs";

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

test("mobile policy exports the permission-protected Android widget refresh JobService", () => {
  const manifests = {
    androidManifest: readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/AndroidManifest.xml"), "utf8"),
    androidPluginManifest: readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/AndroidManifest.xml"), "utf8"),
  };
  assert.doesNotThrow(() => assertAndroidWidgetJobService(manifests));
  for (const key of Object.keys(manifests)) {
    const manifest = manifests[key];
    const declaration = (manifest.match(/<service\b[^>]*>/gu) ?? [])
      .find((candidate) => candidate.includes('android:name="io.delino.devhud.widget.DevHudWidgetRefreshService"'));
    assert.ok(declaration);
    assert.throws(
      () => assertAndroidWidgetJobService({ ...manifests, [key]: manifest.replace(declaration, declaration.replace('android:exported="true"', 'android:exported="false"')) }),
      /must be exported/u,
    );
    assert.throws(
      () => assertAndroidWidgetJobService({ ...manifests, [key]: manifest.replace(declaration, declaration.replace("android.permission.BIND_JOB_SERVICE", "android.permission.BIND_NOT_JOB_SERVICE")) }),
      /must require BIND_JOB_SERVICE/u,
    );
  }
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
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('invoke.reject("not-configured", "not-configured")', 'invoke.reject("storage-failure", "storage-failure")')), /missing widget PATs must use not-configured/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace('if (!disabled) throw IllegalStateException("widget cleanup failed")', "if (false) Unit")), /cleanup failures must remain storage failures/u);
  const widgetCleanupAfterSecurePurge = androidNativeBridge
    .replace("            if (!DevHudWidgetStore(activity.applicationContext).clear()) return@persistSecure false\n", "")
    .replace("            editor.commit()\n        }\n    }\n\n    private fun widgetStatus", "            val secureCleared = editor.commit()\n            if (!DevHudWidgetStore(activity.applicationContext).clear()) return@persistSecure false\n            secureCleared\n        }\n    }\n\n    private fun widgetStatus");
  assert.throws(() => assertAndroidNativeBridge(widgetCleanupAfterSecurePurge), /before the authoritative secure store/u);
  assert.throws(() => assertAndroidNativeBridge(androidNativeBridge.replace("replaceProfileToken(profileId, scopeId, null)", "cancelProfileTokenReplacement()")), /profile-scope removal/u);
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
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replaceAll("rollbackGitHubPatWrite(setting, previousData: previousGitHubPatData)", "true")), /restore or remove the shared GitHub PAT/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("switch beginWidgetCredentialReplacement", "switch delayedWidgetCredentialReplacement")), /before changing the main PAT/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("for deckId in transaction.deckIds", "for deckId in transaction.deckIds.prefix(1)")), /update every recorded Deck/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("private func reconcileWidgetCredentialReplacement", "private func skipWidgetCredentialReplacement")), /authoritative profile scope and main PAT/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("githubPatScope(transaction.scopeId, transaction.profileId)", "githubPatScope(\"other\", transaction.profileId)")), /authoritative profile scope/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("replaceWidgetCredentials(widgetCredentialReplacement, data: nil)", "true")), /profile-scope removal/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("pendingDiagnosticsCleanup = target", "pendingDiagnosticsCleanup = nil")), /cleanup must remain pending/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("if failed || !cleanupSucceeded", "if failed")), /fail closed/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace('if scope == "logout" || scope == "account-deletion"', "if true")), /preserve pending diagnostics exports/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("guard diagnosticsCleanupSucceeded else", "guard true else")), /propagate diagnostics cleanup failures/u);
  const widgetCleanupAfterSecurePurge = iosNativeBridge.replace(
    "        guard clearWidgetState() else { rejectStorageFailure(invoke); return }\n        for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey] {\n            guard purgeSecureGroup(args, accessGroupKey: accessGroupKey) else { rejectStorageFailure(invoke); return }\n        }",
    "        for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey] {\n            guard purgeSecureGroup(args, accessGroupKey: accessGroupKey) else { rejectStorageFailure(invoke); return }\n        }\n        guard clearWidgetState() else { rejectStorageFailure(invoke); return }",
  );
  assert.throws(() => assertIosNativeBridge(widgetCleanupAfterSecurePurge), /before the authoritative secure store/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("previousCredentialData: previousCredentialData", "previousCredentialData: nil")), /retain prior state for rollback/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("defaults.removeObject(forKey: snapshotKey)\n                defaults.removeObject(forKey: deadlineKey)", "defaults.removeObject(forKey: snapshotKey)")), /selection changes must invalidate/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("storeWidgetCredential(previousCredentialData", "storeWidgetCredential(Data()")), /restore prior state before clearing/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("previous == configuration", "false")), /unchanged widget enablement/u);
  assert.throws(() => assertIosNativeBridge(iosNativeBridge.replace("widgetForegroundReloadDeadlinePrefix + snapshot.deckId", "legacyWidgetForegroundReloadDeadlineKey")), /Deck-scoped stored-only reload marker/u);
});

test("mobile open URL handling accepts only authentication callbacks and validated Deck links", () => {
  const nativePlugin = readFileSync(join(appRoot, "src-tauri/src/native_plugin.rs"), "utf8").replace(/\r\n?/gu, "\n");
  const start = nativePlugin.indexOf("#[cfg(mobile)]\n            if let tauri::RunEvent::Opened");
  const end = nativePlugin.indexOf("\n            }\n        })", start);
  const openedHandler = nativePlugin.slice(start, end);
  assert.ok(start >= 0 && end > start, "mobile opened handler must exist");
  assert.match(openedHandler, /offer_auth_callback/u);
  assert.match(openedHandler, /offer_deck_link/u);
});

test("widget targets preserve secure isolation, bilingual privacy warnings, and bounded previews", () => {
  const androidNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/java/io/delino/devhud/bridge/DevhudNativePlugin.kt"), "utf8");
  const androidStore = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/java/io/delino/devhud/widget/DevHudWidgetStore.kt"), "utf8");
  const androidProvider = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/java/io/delino/devhud/widget/DevHudWidgetProvider.kt"), "utf8");
  const androidWriteSecure = androidNativeBridge.slice(androidNativeBridge.indexOf("private fun writeSecure"), androidNativeBridge.indexOf("private fun encryptSecure"));
  const androidDisable = androidStore.slice(androidStore.indexOf("fun disable(deckId"), androidStore.indexOf("fun clear()"));
  const androidBeginCredentialReplacement = androidStore.slice(androidStore.indexOf("fun beginProfileTokenReplacement"), androidStore.indexOf("fun replaceProfileToken"));
  const androidApplyCredentialReplacement = androidStore.slice(androidStore.indexOf("private fun applyProfileTokenReplacement"), androidStore.indexOf("private fun credentialReplacement()"));
  const androidReconcileCredentialReplacement = androidStore.slice(androidStore.indexOf("private fun reconcileProfileTokenReplacement"), androidStore.indexOf("private fun applyProfileTokenReplacement"));
  const androidRefreshService = androidProvider.slice(androidProvider.indexOf("class DevHudWidgetRefreshService"), androidProvider.indexOf("class DevHudWidgetConfigureActivity"));
  const androidConfigureActivity = androidProvider.slice(androidProvider.indexOf("class DevHudWidgetConfigureActivity"));
  const androidManifest = readFileSync(join(appRoot, "mobile/overrides/android/app/src/main/AndroidManifest.xml"), "utf8");
  const androidEnglish = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/res/values/widget_strings.xml"), "utf8");
  const androidKorean = readFileSync(join(appRoot, "src-tauri/mobile/android/src/main/res/values-ko/widget_strings.xml"), "utf8");
  const iosNativeBridge = readFileSync(join(appRoot, "src-tauri/mobile/ios/Sources/DevhudNativePlugin.swift"), "utf8");
  const iosEnableWidgetDeck = iosNativeBridge.slice(iosNativeBridge.indexOf("private func enableWidgetDeck"), iosNativeBridge.indexOf("private func widgetSelectionChanged"));
  const iosRemoveWidgetDeck = iosNativeBridge.slice(iosNativeBridge.indexOf("private func removeWidgetDeck"), iosNativeBridge.indexOf("private func clearWidgetState"));
  const iosWidget = readFileSync(join(appRoot, "mobile/overrides/ios/DevHudWidget/DevHudWidget.swift"), "utf8");
  const iosIntent = readFileSync(join(appRoot, "mobile/overrides/ios/DevHudWidgetShared/SelectDeck.intentdefinition"), "utf8");
  const iosIntentEnglish = readFileSync(join(appRoot, "mobile/overrides/ios/DevHudWidgetShared/en.lproj/SelectDeck.strings"), "utf8");
  const iosIntentKorean = readFileSync(join(appRoot, "mobile/overrides/ios/DevHudWidgetShared/ko.lproj/SelectDeck.strings"), "utf8");
  const iosIntentHandler = readFileSync(join(appRoot, "mobile/overrides/ios/DevHudWidgetIntent/IntentHandler.swift"), "utf8");
  const iosApplicationEntitlements = readFileSync(join(appRoot, "mobile/overrides/ios/DevHud.entitlements"), "utf8");
  const iosEntitlements = readFileSync(join(appRoot, "mobile/overrides/ios/DevHudWidget/DevHudWidget.entitlements"), "utf8");
  assert.doesNotThrow(() => assertNativeWidgetPullRequestMetadata(androidProvider, iosWidget));
  for (const [exact, coercing] of [
    ['item.opt("node_id") as? String', 'item.optString("node_id")'],
    ['item.opt("number") as? Int', 'item.getInt("number")'],
    ['item.opt("title") as? String', 'item.getString("title")'],
    ['item.opt("repository_url") as? String', 'item.getString("repository_url")'],
  ]) assert.throws(() => assertNativeWidgetPullRequestMetadata(androidProvider.replace(exact, coercing), iosWidget), /exact result field types/u);
  assert.throws(() => assertNativeWidgetPullRequestMetadata(androidProvider.replace('.put("nodeId", nodeId)', '.put("nodeId", item.optString("node_id"))'), iosWidget), /only validated result fields/u);
  assert.throws(() => assertNativeWidgetPullRequestMetadata(androidProvider.replace('item.optJSONObject("pull_request")', "JSONObject()"), iosWidget), /missing or non-object pull_request/u);
  assert.throws(() => assertNativeWidgetPullRequestMetadata(androidProvider.replace("mergedAt !== JSONObject.NULL && mergedAt !is String", "false"), iosWidget), /merged_at to be a string or null/u);
  assert.throws(() => assertNativeWidgetPullRequestMetadata(androidProvider, iosWidget.replace('item["pull_request"] as? [String: Any]', "[String: Any]()")), /missing or non-object pull_request/u);
  assert.throws(() => assertNativeWidgetPullRequestMetadata(androidProvider, iosWidget.replace("mergedAt is String || mergedAt is NSNull", "mergedAt is String")), /merged_at to be a string or null/u);
  assert.match(androidStore, /widgetKeyAlias = "io\.delino\.devhud\.widget-credential\.v1"/u);
  assert.match(androidStore, /private val widgetStoreMutationLock = Any\(\)/u);
  assert.equal((androidStore.match(/synchronized\(widgetStoreMutationLock\)/gu) ?? []).length, 8);
  assert.match(androidStore, /transactionPrefix = "transaction:"[\s\S]*init \{\s*reconcile\(\)/u);
  assert.match(androidStore, /disableTransactionPrefix = "disable-transaction:"/u);
  assert.ok(androidStore.indexOf("putBoolean(transactionKey, true).commit()") < androidStore.indexOf("putString(deckId, encrypted).commit()"));
  assert.ok(androidDisable.indexOf("putBoolean(transactionKey, true).commit()") < androidDisable.indexOf("secrets.edit().remove(deckId).commit()"));
  assert.ok(androidDisable.indexOf("secrets.edit().remove(deckId).commit()") < androidDisable.indexOf("remove(configurationPrefix + deckId)"));
  assert.match(androidStore, /enabledDeckIds[\s\S]*filterNot \{ entries\.containsKey\(disableTransactionPrefix \+ it\) \}/u);
  assert.match(androidStore, /fun configuration\(deckId: String\)[\s\S]*state\.contains\(disableTransactionPrefix \+ deckId\)[\s\S]*return null/u);
  assert.match(androidStore, /fun snapshot\(deckId: String\)[\s\S]*state\.contains\(disableTransactionPrefix \+ deckId\)[\s\S]*return null/u);
  assert.match(androidStore, /sealed class WidgetCredential[\s\S]*object Missing[\s\S]*data class Readable[\s\S]*data class Unreadable/u);
  assert.match(androidStore, /fun credential\(deckId: String\)[\s\S]*state\.contains\(transactionPrefix \+ deckId\)[\s\S]*return WidgetCredential\.Missing[\s\S]*return WidgetCredential\.Unreadable\(revision\)[\s\S]*WidgetCredential\.Readable\(token, revision\)/u);
  assert.match(androidStore, /private fun reconcile\(\)[\s\S]*pendingEnableDeckIds[\s\S]*pendingDisableDeckIds[\s\S]*credentialDeckIds\.filterNot\(configuredDeckIds::contains\)[\s\S]*removedDeckIds\.forEach \{ editor\.remove\(it\) \}[\s\S]*pendingDisableDeckIds\.forEach[\s\S]*remove\(configurationPrefix \+ deckId\)[\s\S]*remove\(disableTransactionPrefix \+ deckId\)/u);
  assert.match(androidStore, /private fun abortEnable[\s\S]*rollback\.commit\(\)[\s\S]*remove\(transactionPrefix \+ deckId\)\.commit\(\)/u);
  assert.match(androidBeginCredentialReplacement, /profileId[\s\S]*scopeId[\s\S]*\.sorted\(\)[\s\S]*putString\(credentialReplacementKey, transaction\.toString\(\)\)\.commit\(\)/u);
  assert.ok(androidWriteSecure.indexOf("beginProfileTokenReplacement(profileId, scopeId)") < androidWriteSecure.indexOf(".putString(marker, encryptSecure"));
  assert.match(androidApplyCredentialReplacement, /for \(index in 0 until deckIds\.length\(\)\)[\s\S]*encrypt\(token, deckId\)[\s\S]*editor\.commit\(\)[\s\S]*remove\(credentialReplacementKey\)\.commit\(\)/u);
  assert.match(androidReconcileCredentialReplacement, /mainSecretStore[\s\S]*github-pat-scope:[\s\S]*decryptMainSecure[\s\S]*applyProfileTokenReplacement\(transaction, token\)/u);
  assert.match(androidStore, /fun credential\(deckId: String\)[\s\S]*credentialReplacementBlocks\(deckId\)[\s\S]*return WidgetCredential\.Missing/u);
  assert.match(androidStore, /replaceSnapshot\(snapshot: JSONObject, credentialRevision: String\?, verifyCredential: Boolean\)[\s\S]*credentialReplacementBlocks\(deckId\)/u);
  assert.match(androidStore, /replaceSnapshot\(snapshot: JSONObject, credentialRevision: String\?\)[\s\S]*secrets\.getString\(deckId, null\) != credentialRevision/u);
  assert.match(androidStore, /mergeSnapshot\(current, snapshot\)[\s\S]*lastAttemptedAt[\s\S]*lastSuccessfulAt[\s\S]*put\("counts", success[\s\S]*put\("state", attempt/u);
  assert.match(androidStore, /previousSecret[\s\S]*rollback\.putString\(deckId, previousSecret\)/u);
  assert.match(androidWriteSecure, /widgetStore\.replaceProfileToken\(profileId, scopeId, value\)/u);
  assert.match(androidProvider, /JobService[\s\S]*setRequiredNetworkType/u);
  assert.match(androidProvider, /credential is WidgetCredential\.Missing[\s\S]*failure\(configuration, previous, "missing-token"[\s\S]*replaceSnapshot\(snapshot, null\)/u);
  assert.match(androidProvider, /credential is WidgetCredential\.Unreadable[\s\S]*failure\(configuration, previous, "error"[\s\S]*replaceSnapshot\(snapshot, credential\.revision\)/u);
  assert.equal((androidProvider.match(/replaceSnapshot\(snapshot, credential\.revision\)/gu) ?? []).length, 2);
  assert.match(androidProvider, /groupBy \{ store\.selectedDeckId\(it\) \}[\s\S]*refresh\(applicationContext, manager, deckId, appWidgetIds, session\)/u);
  assert.match(androidProvider, /repositoryValidationConcurrency = 3/u);
  assert.match(androidProvider, /refreshDeadlineMillis = 20_000L/u);
  assert.match(androidProvider, /ExecutorCompletionService[\s\S]*repeat\(minOf\(repositoryValidationConcurrency, repositories\.length\(\)\)\)[\s\S]*completion\.poll\(session\.remainingMillis\(\)/u);
  assert.match(androidProvider, /connectTimeout = minOf\(15_000, session\.remainingMillis\(\)\)[\s\S]*readTimeout = minOf\(20_000, session\.remainingMillis\(\)\)/u);
  assert.match(androidProvider, /widgetDeadlineExecutor = Executors\.newSingleThreadScheduledExecutor\(\)[\s\S]*deadlineCancellation = widgetDeadlineExecutor\.schedule\([\s\S]*cancel\(WidgetRefreshCancellation\.DEADLINE\)[\s\S]*deadlineCancellation\.cancel\(false\)/u);
  assert.match(androidRefreshService, /private class WidgetRefreshRun[\s\S]*private val stopped = AtomicBoolean\(false\)[\s\S]*private val activeSession = AtomicReference<WidgetRefreshSession\?>\(null\)/u);
  assert.match(androidRefreshService, /activeRun\?\.stop\(\)[\s\S]*activeRun = run[\s\S]*run\.attach\(session\)[\s\S]*!run\.isStopped\(\) && activeRun === run[\s\S]*jobFinished\(run\.parameters, retry\)/u);
  assert.match(androidRefreshService, /onStopJob[\s\S]*activeRun\?\.stop\(\)[\s\S]*activeRun = null/u);
  assert.doesNotMatch(androidRefreshService, /stopped\.set\(false\)/u);
  assert.match(androidProvider, /fun cancel[\s\S]*connections\.forEach \{ it\.disconnect\(\) \}/u);
  assert.match(androidProvider, /ScrollView/u);
  assert.match(androidProvider, /incomplete_results/u);
  assert.match(androidProvider, /item\.opt\("draft"\) as\? Boolean[\s\S]*item\.opt\("state"\) as\? String[\s\S]*it == "open" \|\| it == "closed"/u);
  assert.ok(androidProvider.indexOf('item.opt("state") as? String') < androidProvider.indexOf("when { isDraft"));
  assert.doesNotMatch(androidProvider, /item\.optBoolean\("draft"|item\.optString\("state"/u);
  assert.match(androidProvider, /val isMerged = mergedAt is String/u);
  assert.ok(androidProvider.indexOf("validateRepositories(configuration, token, session)") < androidProvider.indexOf('github("/search/issues'));
  assert.match(androidProvider, /\/pulls\?state=open&per_page=1[\s\S]*\/issues\?state=open&per_page=1[\s\S]*\/contents/u);
  assert.match(androidProvider, /metadataPayload\.has\("pushed_at"\) && metadataPayload\.opt\("pushed_at"\) === JSONObject\.NULL/u);
  assert.doesNotMatch(androidProvider, /metadataPayload\.isNull\("pushed_at"\)/u);
  assert.match(androidProvider, /private fun responseIsRateLimited[\s\S]*status == 429[\s\S]*status != 403[\s\S]*X-RateLimit-Remaining[\s\S]*Retry-After[\s\S]*errorStream[\s\S]*JSONObject\(reader\.readText\(\)\)\.opt\("message"\) as\? String[\s\S]*lowercase\(Locale\.ROOT\)[\s\S]*contains\("rate limit"\)/u);
  assert.equal((androidProvider.match(/responseIsRateLimited\(connection\)/gu) ?? []).length, 2);
  assert.match(androidProvider, /status == 401[\s\S]*"missing-token"[\s\S]*status == 403 \|\| status == 404[\s\S]*"permission"/u);
  assert.match(androidProvider, /val stored = store\.replaceSnapshot\(snapshot, credential\.revision\)\s+val renderConfiguration = store\.configuration\(deckId\)\s+if \(renderConfiguration == null\)[\s\S]*renderStored\(context, manager, it\)[\s\S]*val rendered = store\.snapshot\(deckId\)[\s\S]*renderSelected\(context, manager, store, deckId, renderConfiguration/u);
  assert.match(androidManifest, /DevHudWidgetProvider"\s+android:exported="false"\s+android:label="@string\/devhud_widget_name"/u);
  assert.match(androidProvider, /results.*prefix|prefix\(3\)|minOf\(results\?\.length\(\) \?: 0, 3\)/su);
  assert.match(androidProvider, /devhud:\/\/deck\//u);
  assert.match(androidNativeBridge, /private fun renderWidgets\(\)[\s\S]*ids\.forEach \{ DevHudWidgetProvider\.renderStored/u);
  assert.doesNotMatch(androidNativeBridge, /ACTION_APPWIDGET_UPDATE/u);
  assert.match(androidProvider, /localizedContext\(context, configuration\)[\s\S]*copyContext\.getString/u);
  assert.match(androidProvider, /"en" -> Locale\.ENGLISH[\s\S]*"ko" -> Locale\.KOREAN/u);
  assert.match(androidConfigureActivity, /val configurations = store\.enabledDeckIds\(\)\.mapNotNull\(store::configuration\)[\s\S]*val copyContext = localizedContext\(this, configurations\.firstOrNull\(\)\)/u);
  assert.match(androidConfigureActivity, /copyContext\.getString\(R\.string\.devhud_widget_choose_deck\)/u);
  assert.match(androidConfigureActivity, /copyContext\.getString\(R\.string\.devhud_widget_privacy_warning\)/u);
  assert.match(androidConfigureActivity, /copyContext\.getString\(R\.string\.devhud_widget_setup\)/u);
  assert.match(androidConfigureActivity, /copyContext\.getString\(R\.string\.devhud_widget_select_deck, text\)/u);
  assert.match(iosWidget, /IntentConfiguration/u);
  assert.match(iosIntent, /INIntentEligibleForWidgets[\s\S]*INIntentParameterSupportsDynamicEnumeration/u);
  assert.match(iosIntentHandler, /SelectDeckIntentHandling[\s\S]*provideDeckOptionsCollection/u);
  assert.match(iosIntentHandler, /defaultDeck[\s\S]*nil/u);
  assert.match(iosWidget, /\.prefix\(3\)/u);
  assert.match(iosWidget, /func save\(_ snapshot: DeckSnapshot, whileEnabled configuration: DeckConfiguration, credentialRevision: Data\?\)[\s\S]*credentialMatches\(deckId: snapshot\.deckId, revision: credentialRevision\)[\s\S]*defaults\.set/u);
  assert.equal((iosWidget.match(/credentialMatches\(deckId: snapshot\.deckId, revision: credentialRevision\)/gu) ?? []).length, 2);
  assert.match(iosWidget, /private enum WidgetCredential[\s\S]*case missing[\s\S]*case readable\(token: String, revision: Data\)[\s\S]*case unreadable\(revision: Data\)/u);
  assert.match(iosWidget, /legacyWidgetToken[\s\S]*\["ghp_", "github_pat_"\][\s\S]*token\.utf8\.count > prefix\.utf8\.count[\s\S]*token\.utf8\.allSatisfy/u);
  assert.match(iosWidget, /func credential\(_ deckId: String\)[\s\S]*stored\.version == 1[\s\S]*legacyWidgetToken\(data\)[\s\S]*\.unreadable\(revision: data\)/u);
  assert.doesNotMatch(iosWidget, /decode\(StoredWidgetCredential\.self, from: data\)\)\?\.token \?\? String/u);
  assert.match(iosWidget, /case \.unreadable\(let revision\):[\s\S]*state: "error"[\s\S]*credentialRevision = revision[\s\S]*store\.save\(snapshot, whileEnabled: current, credentialRevision: credentialRevision\)/u);
  assert.match(iosWidget, /repositoryValidationConcurrency = 3/u);
  assert.match(iosWidget, /refreshDeadlineNanoseconds: UInt64 = 20 \* 1_000_000_000/u);
  assert.match(iosWidget, /private actor DeckRefreshCoordinator[\s\S]*private var inFlight: \[String: InFlightRefresh\][\s\S]*while let existing = inFlight\[deck\.deckId\][\s\S]*sameSelection\(existing\.configuration, deck\) && existing\.credentialRevision == credentialRevision[\s\S]*inFlight\[deck\.deckId\]\?\.id == existing\.id[\s\S]*inFlight\[deck\.deckId\] = InFlightRefresh/u);
  assert.match(iosWidget, /private let deckRefreshCoordinator = DeckRefreshCoordinator\(\)/u);
  assert.equal((iosWidget.match(/deckRefreshCoordinator\.refresh\(deck: deck, credentialRevision: credentialRevision\)/gu) ?? []).length, 2);
  assert.match(iosWidget, /refreshWithDeadline[\s\S]*withTaskGroup\(of: DeckSnapshot\.self\)[\s\S]*Task\.sleep\(nanoseconds: refreshDeadlineNanoseconds\)[\s\S]*group\.cancelAll\(\)/u);
  assert.match(iosWidget, /validateRepositories[\s\S]*withTaskGroup\(of: RepositoryValidationFailure\?\.self\)[\s\S]*repositories\.prefix\(initialCount\)[\s\S]*group\.cancelAll\(\)/u);
  assert.match(iosWidget, /try Task\.checkCancellation\(\)/u);
  assert.match(iosWidget, /URLSessionConfiguration\.ephemeral[\s\S]*requestCachePolicy = \.reloadIgnoringLocalCacheData[\s\S]*urlCache = nil[\s\S]*URLSession\(configuration: configuration\)/u);
  assert.equal((iosWidget.match(/githubSession\.data\(for: request\)/gu) ?? []).length, 2);
  assert.doesNotMatch(iosWidget, /URLSession\.shared/u);
  assert.match(iosWidget, /staleDate[\s\S]*entries\.append/u);
  assert.match(iosWidget, /mergeDeckSnapshot\(current: self\.snapshot\(snapshot\.deckId\), incoming: snapshot\)/u);
  assert.match(iosWidget, /defaults\.data\(forKey: key\) == data \{ defaults\.removeObject\(forKey: key\) \}/u);
  assert.ok(iosWidget.indexOf("if store.shouldRenderForegroundSnapshot(deck.deckId)") < iosWidget.indexOf("guard let credential = store.credential"));
  assert.ok(iosWidget.indexOf("if store.shouldRenderForegroundSnapshot(deck.deckId)") < iosWidget.indexOf("Self.refreshWithDeadline"));
  assert.match(iosWidget, /foregroundReloadDeadlinePrefix \+ deckId/u);
  assert.match(iosWidget, /parseWidgetTimestamp[\s\S]*withInternetDateTime, \.withFractionalSeconds[\s\S]*fractional\.date\(from: value\) \?\? ISO8601DateFormatter\(\)\.date\(from: value\)/u);
  assert.equal((iosWidget.match(/parseWidgetTimestamp\(value\)/gu) ?? []).length, 3);
  assert.match(iosWidget, /widgetAttemptTimestamp[\s\S]*withInternetDateTime, \.withFractionalSeconds[\s\S]*fractional\.string\(from: date\)/u);
  assert.equal((iosWidget.match(/widgetAttemptTimestamp\(\)/gu) ?? []).length, 3);
  assert.match(iosWidget, /incomplete_results/u);
  assert.doesNotMatch(iosWidget, /items\.prefix\(100\)\.compactMap/u);
  assert.match(iosWidget, /for item in items\.prefix\(100\)[\s\S]*guard let nodeId[\s\S]*return failure\(deck: deck, previous: previous, state: "error", attempted: attempted, rate: rate\)/u);
  assert.match(iosWidget, /item\["draft"\] as\? Bool[\s\S]*item\["state"\] as\? String[\s\S]*itemState == "open" \|\| itemState == "closed"/u);
  assert.ok(iosWidget.indexOf('item["state"] as? String') < iosWidget.indexOf("if isDraft"));
  assert.doesNotMatch(iosWidget, /item\["draft"\] as\? Bool \?\?|item\["state"\] as\? String \?\?/u);
  assert.match(iosWidget, /retained\.rate = rate/u);
  assert.match(androidProvider, /put\("rate", responseRate \?: JSONObject\.NULL\)/u);
  assert.ok(iosWidget.indexOf("validateRepositories(deck: deck, token: token)") < iosWidget.indexOf('URLComponents(string: "https://api.github.com/search/issues")'));
  assert.match(iosWidget, /\/pulls\?state=open&per_page=1[\s\S]*\/issues\?state=open&per_page=1[\s\S]*\/contents/u);
  assert.match(iosWidget, /private static func responseIsRateLimited[\s\S]*root\["message"\] as\? String[\s\S]*message\.lowercased\(\)\.contains\("rate limit"\)/u);
  assert.match(iosWidget, /let rateLimited = responseIsRateLimited\(http, data: data, rate: rate\)/u);
  assert.match(iosWidget, /private static func responseFailure[\s\S]*responseIsRateLimited\(response, data: data, rate: rate\)[\s\S]*return "rate-limit"/u);
  assert.equal((iosWidget.match(/if let state = responseFailure\([^\n]+data: [^\n]+rate:/gu) ?? []).length, 4);
  assert.match(iosWidget, /statusCode == 401[\s\S]*"missing-token"[\s\S]*statusCode == 403 \|\| .*statusCode == 404[\s\S]*"permission"/u);
  assert.match(iosWidget, /#available\(iOS 17\.0, \*\)[\s\S]*containerBackground\(for: \.widget\)[\s\S]*else[\s\S]*background\(Color\(red: 0\.11/u);
  assert.ok(iosNativeBridge.indexOf("beginWidgetCredentialReplacement(profileId: setting.profileId") < iosNativeBridge.indexOf("guard storeData(data, setting: setting"));
  assert.match(iosNativeBridge, /WidgetCredential\(version: 1, revision: UUID\(\)\.uuidString\.lowercased\(\), token: token\)/u);
  assert.match(iosNativeBridge, /WidgetCredentialReplacement\(version: 1, profileId: profileId, scopeId: scopeId, deckIds: deckIds\)[\s\S]*defaults\.set\(encoded, forKey: widgetCredentialReplacementKey\)[\s\S]*defaults\.synchronize\(\)/u);
  assert.match(iosNativeBridge, /replaceWidgetCredentials[\s\S]*for deckId in transaction\.deckIds[\s\S]*storeWidgetCredential\(\$0, deckId: deckId\)[\s\S]*removeObject\(forKey: widgetCredentialReplacementKey\)/u);
  assert.match(iosNativeBridge, /reconcileWidgetCredentialReplacement[\s\S]*readDataMigratingLegacy\(setting\)[\s\S]*replaceWidgetCredentials\(transaction, data:/u);
  assert.match(iosNativeBridge, /for key in keys where key\.hasPrefix\(widgetConfigurationPrefix\)[\s\S]*for key in keys where key\.hasPrefix\(widgetSnapshotPrefix\)/u);
  assert.match(iosNativeBridge, /private func clearWidgetState\(\)[\s\S]*widgetTransactionPrefix[\s\S]*guard defaults\.synchronize\(\) else \{ return false \}[\s\S]*guard removeAllWidgetCredentials\(\) else \{ return false \}[\s\S]*reloadAllTimelines/u);
  assert.match(iosNativeBridge, /private func disableWidgetDeck[\s\S]*guard removeWidgetDeck\(defaults, deckId: deckId\)[\s\S]*invoke\.resolve/u);
  assert.ok(iosRemoveWidgetDeck.indexOf("defaults.synchronize()") < iosRemoveWidgetDeck.indexOf("removeWidgetCredential(deckId)"));
  assert.ok(iosEnableWidgetDeck.indexOf("removeWidgetDeck(defaults, deckId: configuration.deckId)") < iosEnableWidgetDeck.indexOf('invoke.reject("not-configured"'));
  assert.match(iosNativeBridge, /pendingDeckIds[\s\S]*removeWidgetCredential\(deckId\)[\s\S]*widgetCredentialDeckIds\(\)/u);
  assert.match(iosNativeBridge, /private func replaceWidgetSnapshot[\s\S]*mergeWidgetSnapshot\(current: previousSnapshot, incoming: snapshot\)[\s\S]*widgetForegroundReloadDeadlinePrefix \+ snapshot\.deckId[\s\S]*defaults\.synchronize\(\)[\s\S]*reloadAllTimelines/u);
  assert.match(iosWidget, /func credential\(_ deckId: String\)[\s\S]*!credentialReplacementBlocks\(deckId\)[\s\S]*StoredWidgetCredential[\s\S]*revision: data/u);
  assert.match(iosWidget, /guard result\.status == errSecSuccess, let data = result\.data else \{ return \.unavailable \}/u);
  assert.match(iosWidget, /case \.unavailable:[\s\S]*Self\.failure\(deck: current, previous: store\.snapshot\(current\.deckId\), state: "error"[\s\S]*completion\(timeline\(deck: current, snapshot: snapshot\)\)/u);
  assert.match(iosWidget, /credentialReplacementBlocks[\s\S]*credentialReplacementKey[\s\S]*transaction\.deckIds\.contains\(deckId\)/u);
  assert.match(iosWidget, /devhud:\/\/deck\//u);
  assert.match(iosApplicationEntitlements, /group\.io\.delino\.devhud/u);
  assert.match(iosApplicationEntitlements, /\$\(AppIdentifierPrefix\)io\.delino\.devhud<\/string>[\s\S]*\$\(AppIdentifierPrefix\)io\.delino\.devhud\.shared/u);
  assert.match(iosEntitlements, /group\.io\.delino\.devhud/u);
  assert.match(iosEntitlements, /\$\(AppIdentifierPrefix\)io\.delino\.devhud\.shared/u);
  for (const warning of [androidEnglish, androidKorean]) assert.match(warning, /launchers|런처/iu);
  assert.match(iosIntentEnglish, /Select Deck|Choose one Deck/u);
  assert.match(iosIntentKorean, /덱/u);
  for (const source of [androidStore, androidProvider, iosWidget]) assert.doesNotMatch(source, /devhud-api|println|print\(/iu);
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
    platforms: { schemaVersion: 1, identity: "io.delino.devhud", deepLinkScheme: "devhud", authCallback: "devhud://auth/callback", frontendDist: "../dist", minimumVersions: { ios: "16.0", androidApi: 29 }, widgets: mobilePlatforms.widgets, targets: mobileTargets },
    tauri: { identifier: "io.delino.devhud", build: { frontendDist: "../dist" } },
    ios: { bundle: { iOS: { minimumSystemVersion: "16.0" } } },
    android: { bundle: { android: { minSdkVersion: 29 } } }, cargo: "", androidManifest: "android.permission.INTERNET", androidPluginManifest: "", androidNativeBridge: "", iosPlist: "", packageJson: { scripts: {} }, nativeBridge: "", app: "", workflow: "",
  };
  assert.throws(() => assertMobileContracts(base), /system-webview features/u);
});
