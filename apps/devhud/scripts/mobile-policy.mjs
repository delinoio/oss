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
    '<exclude domain="sharedpref" path="devhud-widget-state-v1.xml" />',
    '<exclude domain="sharedpref" path="devhud-widget-secret-v1.xml" />',
  ];
  const webViewExclusion = '<exclude domain="root" path="app_webview/" />';
  assert(androidManifest.includes('android:fullBackupContent="@xml/backup_rules"'), "Android full-backup policy is missing");
  assert(androidManifest.includes('android:dataExtractionRules="@xml/data_extraction_rules"'), "Android data-extraction policy is missing");
  for (const exclusion of privatePreferenceExclusions) {
    assert(androidBackupRules.includes(exclusion), `Android full-backup exclusion changed: ${exclusion}`);
  }
  assert(androidBackupRules.includes(webViewExclusion), "Android full-backup WebView exclusion changed");
  for (const section of ["cloud-backup", "device-transfer"]) {
    const content = androidDataExtractionRules.match(new RegExp(`<${section}>([\\s\\S]*?)<\\/${section}>`, "u"))?.[1] ?? "";
    for (const exclusion of privatePreferenceExclusions) {
      assert(content.includes(exclusion), `Android ${section} exclusion changed: ${exclusion}`);
    }
    assert(content.includes(webViewExclusion), `Android ${section} WebView exclusion changed`);
  }
}

export function assertAndroidWidgetStore(androidWidgetStoreInput) {
  const androidWidgetStore = androidWidgetStoreInput.replaceAll("\r\n", "\n");
  const disable = androidWidgetStore.match(/fun disable\(deckId: String\)[\s\S]*?(?=\n    fun clear\(\))/u)?.[0] ?? "";
  assert(disable.includes("val blockCommitted = state.edit().putBoolean(transactionKey, true).commit()"), "Android widget cleanup must retain the disable marker result");
  assert(disable.indexOf("val blockCommitted") < disable.indexOf("secrets.edit().remove(deckId).commit()"), "Android widget cleanup must attempt credential removal after blocking");
  assert(disable.includes("val stateRemoved = editor.commit()") && disable.includes("blockCommitted && stateRemoved"), "Android widget cleanup must propagate marker and state persistence failures");
  assert(!disable.includes("state.edit().remove(transactionKey).commit()"), "Android widget cleanup must not unblock a failed disable transaction before credential removal");
}

export function assertAndroidNativeBridge(androidNativeBridgeInput) {
  const androidNativeBridge = androidNativeBridgeInput.replaceAll("\r\n", "\n");
  const onDestroy = androidNativeBridge.match(/override fun onDestroy\(activity: AppCompatActivity\)[\s\S]*?(?=\n    @Command)/u)?.[0] ?? "";
  const exportDiagnostics = androidNativeBridge.match(/private fun exportDiagnostics\(invoke: Invoke\)[\s\S]*?(?=\n    @ActivityCallback)/u)?.[0] ?? "";
  const diagnosticsExportResult = androidNativeBridge.match(/private fun diagnosticsExportResult\(invoke: Invoke, result: ActivityResult\)[\s\S]*?(?=\n    private fun retainDiagnosticsCleanup)/u)?.[0] ?? "";
  const forgetDiagnosticsCleanup = androidNativeBridge.match(/private fun forgetDiagnosticsCleanup\(\): Boolean[\s\S]*?(?=\n    private fun cleanupPendingDiagnosticsExport)/u)?.[0] ?? "";
  const cleanupPendingDiagnosticsExport = androidNativeBridge.match(/private fun cleanupPendingDiagnosticsExport\(\): Boolean[\s\S]*?(?=\n    private fun hasPersistedDiagnosticsWriteGrant)/u)?.[0] ?? "";
  const removeSecure = androidNativeBridge.match(/private fun removeSecure\(invoke: Invoke\)[\s\S]*?(?=\n    private fun removeGitHubPatScope)/u)?.[0] ?? "";
  const removeGitHubPatScope = androidNativeBridge.match(/private fun removeGitHubPatScope[\s\S]*?(?=\n    private fun reconcileGitHubPats)/u)?.[0] ?? "";
  const reconcileGitHubPats = androidNativeBridge.match(/private fun reconcileGitHubPats[\s\S]*?(?=\n    private fun purgeSecure)/u)?.[0] ?? "";
  const purgeSecure = androidNativeBridge.match(/private fun purgeSecure\(invoke: Invoke\)[\s\S]*?(?=\n    private fun persistSecure)/u)?.[0] ?? "";
  const enableWidgetDeck = androidNativeBridge.match(/private fun enableWidgetDeck\(invoke: Invoke\)[\s\S]*?(?=\n    private fun replaceWidgetSnapshot)/u)?.[0] ?? "";
  const widgetStatus = androidNativeBridge.match(/private fun widgetStatus\(invoke: Invoke\)[\s\S]*?(?=\n    private fun enableWidgetDeck)/u)?.[0] ?? "";
  const persistSecure = androidNativeBridge.match(/private fun persistSecure\(invoke: Invoke[\s\S]*?(?=\n    private fun permissionValue)/u)?.[0] ?? "";
  assert(androidNativeBridge.includes("Executors.newSingleThreadExecutor()"), "Android secure-setting persistence must run off the command thread");
  assert(onDestroy.includes("secureSettingsExecutor.shutdown()"), "Android secure-setting executor must stop with the plugin lifecycle");
  assert((androidNativeBridge.match(/\.commit\(\)/gu) ?? []).length === 11, "Android secure-setting and diagnostics-cleanup writes must confirm persistence");
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
  assert(exportDiagnostics.includes("if (diagnosticsExportPickerActive)") && exportDiagnostics.indexOf("if (diagnosticsExportPickerActive)") < exportDiagnostics.indexOf("diagnosticsExportPickerActive = true"), "Android diagnostics exports must reject a concurrent picker before reserving another one");
  assert(exportDiagnostics.includes("if (diagnosticsPurgesInProgress.get() > 0)"), "Android diagnostics exports must remain blocked until destructive secure purges finish");
  assert(exportDiagnostics.indexOf("diagnosticsExportPickerActive = true") >= 0 && exportDiagnostics.indexOf("diagnosticsExportPickerActive = true") < exportDiagnostics.indexOf("startActivityForResult"), "Android diagnostics exports must record the active picker before launch");
  assert(diagnosticsExportResult.includes("if (!diagnosticsExportPickerActive)") && diagnosticsExportResult.indexOf("if (!diagnosticsExportPickerActive)") < diagnosticsExportResult.indexOf("val destination"), "Android invalidated diagnostics picker callbacks must stop before destination access");
  assert(androidNativeBridge.includes("pendingDiagnosticsCleanup") && androidNativeBridge.includes("takePersistableUriPermission"), "Android failed diagnostics cleanup must retain a persistable destination URI");
  const grantFailureBoundary = diagnosticsExportResult.slice(diagnosticsExportResult.indexOf("takePersistableUriPermission"), diagnosticsExportResult.indexOf("retainDiagnosticsCleanup"));
  assert(grantFailureBoundary.includes('invoke.reject("storage-failure", "storage-failure")') && grantFailureBoundary.includes("return"), "Android diagnostics exports must reject failed URI grants before retaining cleanup state");
  assert(!diagnosticsExportResult.includes("if (pendingDiagnosticsCleanup == null) retainDiagnosticsCleanup(destination)"), "Android diagnostics exports must not retain cleanup state for a URI grant that was not acquired");
  assert(forgetDiagnosticsCleanup.includes("putBoolean(diagnosticsCleanupReleaseOnlyKey, true).commit()") && forgetDiagnosticsCleanup.indexOf("putBoolean(diagnosticsCleanupReleaseOnlyKey, true).commit()") < forgetDiagnosticsCleanup.indexOf("releasePersistableUriPermission"), "Android diagnostics cleanup must persist its release-only transition before releasing the URI grant");
  assert(forgetDiagnosticsCleanup.indexOf("releasePersistableUriPermission") >= 0 && forgetDiagnosticsCleanup.indexOf("releasePersistableUriPermission") < forgetDiagnosticsCleanup.indexOf("remove(diagnosticsCleanupUriKey)"), "Android diagnostics cleanup must clear its retry state after releasing the URI grant");
  assert(forgetDiagnosticsCleanup.includes("if (hasPersistedDiagnosticsWriteGrant(destination))") && forgetDiagnosticsCleanup.indexOf("hasPersistedDiagnosticsWriteGrant(destination)") < forgetDiagnosticsCleanup.indexOf("releasePersistableUriPermission"), "Android diagnostics cleanup must not release an already-released URI grant");
  assert(forgetDiagnosticsCleanup.includes("catch (_: Exception) {\n                return false\n            }"), "Android diagnostics cleanup must preserve retry state when URI grant release fails");
  assert(cleanupPendingDiagnosticsExport.includes("if (diagnosticsCleanupReleaseOnly) return forgetDiagnosticsCleanup()") && cleanupPendingDiagnosticsExport.indexOf("diagnosticsCleanupReleaseOnly") < cleanupPendingDiagnosticsExport.indexOf("hasPersistedDiagnosticsWriteGrant(destination)"), "Android release-only cleanup must not require an already-released URI grant");
  assert(cleanupPendingDiagnosticsExport.includes("if (!hasPersistedDiagnosticsWriteGrant(destination)) return false") && cleanupPendingDiagnosticsExport.indexOf("hasPersistedDiagnosticsWriteGrant(destination)") < cleanupPendingDiagnosticsExport.indexOf("contentResolver.delete"), "Android byte cleanup must preserve retry state when its destination grant is missing");
  assert(cleanupPendingDiagnosticsExport.includes('requireNotNull(activity.contentResolver.openFileDescriptor(destination, "wt")).use { true }'), "Android diagnostics cleanup must explicitly truncate the destination and treat success as complete");
  assert(androidNativeBridge.includes("cleanupPendingDiagnosticsExport()") && androidNativeBridge.includes("FileNotFoundException"), "Android diagnostics cleanup must retry and confirm destination absence");
  assert(removeSecure.includes("removeGitHubPatScope(preferences") && reconcileGitHubPats.includes("removeGitHubPatScope(preferences"), "Android explicit and reconciled profile removal must share widget credential cleanup");
  assert(removeGitHubPatScope.includes("beginProfileTokenReplacement(profileId, scopeId)") && removeGitHubPatScope.indexOf("beginProfileTokenReplacement(profileId, scopeId)") < removeGitHubPatScope.indexOf("editor.commit()") && removeGitHubPatScope.indexOf("editor.commit()") < removeGitHubPatScope.indexOf("replaceProfileToken(profileId, scopeId, null)"), "Android profile-scope removal must durably block and remove copied widget credentials around the authoritative deletion");
  assert(purgeSecure.includes('val destructivePurge = scope in setOf("logout", "account-deletion")') && purgeSecure.indexOf("diagnosticsPurgesInProgress.incrementAndGet()") < purgeSecure.indexOf("diagnosticsExportPickerActive = false") && purgeSecure.indexOf("diagnosticsExportPickerActive = false") < purgeSecure.indexOf("cleanupPendingDiagnosticsExport()"), "Android destructive purges must reserve invalidation before invalidating active diagnostics pickers and cleanup");
  assert(purgeSecure.includes("diagnosticsPurgesInProgress.incrementAndGet()") && (purgeSecure.match(/diagnosticsPurgesInProgress\.decrementAndGet\(\)/gu) ?? []).length === 3, "Android destructive purges must retain and release export invalidation across queued persistence and failures");
  assert(persistSecure.includes("finally") && persistSecure.includes("onComplete()"), "Android secure persistence must release purge state after executor completion");
  assert(enableWidgetDeck.includes("val disabled = widgetStore.disable(deckId)") && enableWidgetDeck.indexOf("widgetStore.disable(deckId)") < enableWidgetDeck.indexOf("renderWidgets()") && enableWidgetDeck.indexOf("renderWidgets()") < enableWidgetDeck.indexOf("throw MissingWidgetCredentialException()"), "Android missing widget PAT rejection must follow widget cleanup and rendering");
  assert(enableWidgetDeck.includes('if (!disabled) throw IllegalStateException("widget cleanup failed")'), "Android missing widget PAT cleanup failures must remain storage failures");
  assert(enableWidgetDeck.includes('if (!disabled) throw IllegalStateException("widget cleanup failed", error)'), "Android unreadable widget PAT cleanup failures must remain storage failures");
  assert(widgetStatus.includes("if (enabled == null)") && widgetStatus.includes('invoke.reject("storage-failure", "storage-failure")') && widgetStatus.indexOf("if (enabled == null)") < widgetStatus.indexOf("invoke.resolve"), "Android widget status must fail closed when reconciliation cannot recover");
  assert(persistSecure.includes("catch (_: MissingWidgetCredentialException)") && persistSecure.includes('invoke.reject("not-configured", "not-configured")') && persistSecure.indexOf("catch (_: MissingWidgetCredentialException)") < persistSecure.indexOf("catch (error: Exception)"), "Android missing widget PATs must use not-configured before generic storage-failure mapping");
  assert(purgeSecure.includes("if (!cleanupPendingDiagnosticsExport())"), "Android destructive purges must propagate diagnostics cleanup failures");
  assert(purgeSecure.includes("DevHudWidgetStore(activity.applicationContext).clear()") && purgeSecure.indexOf("DevHudWidgetStore(activity.applicationContext).clear()") < purgeSecure.indexOf("editor.commit()"), "Android destructive purges must clear widget state before the authoritative secure store");
  assert(androidNativeBridge.includes("storeIntent().resolveActivity(activity.packageManager)"), "Android update status must resolve a market handler");
}

export function assertIosWidgetStateStore(iosWidgetStateStoreInput) {
  const store = iosWidgetStateStoreInput.replaceAll("\r\n", "\n");
  const migration = store.match(/private func migrateLegacyDefaults[\s\S]*?(?=\n    private func removeLegacyDefaults)/u)?.[0] ?? "";
  const write = store.match(/private func write<Value: Encodable>[\s\S]*?(?=\n    private func excludeFromBackup)/u)?.[0] ?? "";
  assert(store.includes('widget-state-v2') && store.includes("containerURL(forSecurityApplicationGroupIdentifier: appGroup)"), "iOS widget state must use the contracted App Group file container");
  assert(store.includes("NSFileCoordinator(filePresenter: nil).coordinate(readingItemAt:") && store.includes("NSFileCoordinator(filePresenter: nil).coordinate(writingItemAt:"), "iOS app and extensions must coordinate shared widget-state file access");
  assert(write.includes(".write(to: url, options: .atomic)") && write.includes("excludeFromBackup(url)") && write.includes("isExcludedFromBackup") && write.includes("guard excluded == true"), "iOS widget-state files must be atomically written and verifiably excluded from backup after every save");
  assert(store.includes("try excludeFromBackup(root)") && store.includes("try excludeFromBackup(decks)"), "iOS widget-state directories must be excluded from backup");
  for (const key of ["widget.configuration.", "widget.snapshot.", "widget.transaction.", "widget.credential-replacement.v1", "widget.foreground-reload-deadline.v1"]) assert(store.includes(key), `iOS widget-state migration is missing ${key}`);
  const removeLegacy = migration.indexOf("removeLegacyDefaults(defaults)");
  const completeMigration = migration.indexOf("metadata.legacyMigrationCompleted = true");
  assert(migration.indexOf("try write(state") < removeLegacy && migration.indexOf("try write(metadata") < removeLegacy && migration.indexOf("defaults.synchronize()") < completeMigration && completeMigration < migration.lastIndexOf("try write(metadata"), "iOS widget-state migration must persist excluded files before deleting defaults and recording completion");
  assert(store.includes("else {\n            // A second process may still have cached legacy preferences") && (store.match(/removeLegacyDefaults\(defaults\)/gu) ?? []).length >= 3, "iOS widget-state migration must idempotently erase cached legacy defaults");
}

export function assertIosNativeBridge(iosNativeBridgeInput, iosWidgetStateStoreInput) {
  const iosNativeBridge = iosNativeBridgeInput.replaceAll("\r\n", "\n");
  assertIosWidgetStateStore(iosWidgetStateStoreInput);
  const exportDiagnostics = iosNativeBridge.match(/private func exportDiagnostics[\s\S]*?(?=\n    @discardableResult\n    private func cleanupDiagnosticsTemporaryDirectory)/u)?.[0] ?? "";
  const cleanupDiagnostics = iosNativeBridge.match(/private func cleanupDiagnosticsTemporaryDirectory[\s\S]*?(?=\n    @discardableResult\n    private func finishDiagnosticsExport)/u)?.[0] ?? "";
  const finishDiagnosticsExport = iosNativeBridge.match(/private func finishDiagnosticsExport[\s\S]*?(?=\n    func documentPicker)/u)?.[0] ?? "";
  const readSecure = iosNativeBridge.match(/private func readSecure[\s\S]*?(?=\n    private func writeSecure)/u)?.[0] ?? "";
  const writeSecure = iosNativeBridge.match(/private func writeSecure[\s\S]*?(?=\n    private func removeSecure)/u)?.[0] ?? "";
  const removeGitHubPatScope = iosNativeBridge.match(/private func removeGitHubPatScope[\s\S]*?(?=\n    private func reconcileGitHubPats)/u)?.[0] ?? "";
  const purgeSecure = iosNativeBridge.match(/private func purgeSecure[\s\S]*?(?=\n    private func permissionName)/u)?.[0] ?? "";
  const beginWidgetCredentialReplacement = iosNativeBridge.match(/private func beginWidgetCredentialReplacement[\s\S]*?(?=\n    private func replaceWidgetCredentials)/u)?.[0] ?? "";
  const replaceWidgetCredentials = iosNativeBridge.match(/private func replaceWidgetCredentials[\s\S]*?(?=\n    private func cancelWidgetCredentialReplacement)/u)?.[0] ?? "";
  const reconcileWidgetCredentialReplacement = iosNativeBridge.match(/private func reconcileWidgetCredentialReplacement[\s\S]*?(?=\n    private func removeWidgetCredential)/u)?.[0] ?? "";
  const abortWidgetTransaction = iosNativeBridge.match(/private func abortWidgetTransaction[\s\S]*?(?=\n    private func widgetStatus)/u)?.[0] ?? "";
  const enableWidgetDeck = iosNativeBridge.match(/private func enableWidgetDeck[\s\S]*?(?=\n    private func widgetSelectionChanged)/u)?.[0] ?? "";
  const replaceWidgetSnapshot = iosNativeBridge.match(/private func replaceWidgetSnapshot[\s\S]*?(?=\n    private func disableWidgetDeck)/u)?.[0] ?? "";
  const coordinatedSnapshotUpdate = replaceWidgetSnapshot.match(/widgetStateStore\.updateDeckState\(snapshot\.deckId, \{ current in[\s\S]*?\n        \}\) else \{/u)?.[0] ?? "";
  const removeWidgetDeck = iosNativeBridge.match(/private func removeWidgetDeck[\s\S]*?(?=\n    private func clearWidgetState)/u)?.[0] ?? "";
  assert(iosNativeBridge.includes('invoke.reject("storage-failure", code: "storage-failure")'), "iOS Keychain failures must use storage-failure");
  assert(iosNativeBridge.includes('invoke.reject("permission-denied", code: "permission-denied")'), "iOS notification publication must honor authorization");
  assert(iosNativeBridge.includes('case "runtime.snapshot":') && iosNativeBridge.includes("UIDevice.current.systemVersion"), "iOS runtime diagnostics must use the installed native OS version");
  assert(iosNativeBridge.includes("UNUserNotificationCenterDelegate") && iosNativeBridge.includes("willPresent notification"), "iOS foreground Deck notifications must be presented by a delegate");
  assert(iosNativeBridge.includes('(url.path.isEmpty || url.path == "/")'), "iOS native navigation must accept both root API-origin spellings");
  assert(iosNativeBridge.includes("isSecureOrLoopback(issuer)") && iosNativeBridge.includes("destination.scheme == issuer.scheme"), "iOS authentication must accept configured issuer paths and loopback HTTP while preserving same-origin navigation");
  assert(!iosNativeBridge.includes('issuer.path == ""'), "iOS authentication must not restrict configured issuer paths");
  assert(iosNativeBridge.includes("kSecAttrAccessGroup") && iosNativeBridge.includes("kSecAttrSynchronizable"), "iOS secrets must use the shared non-synchronizing Keychain group");
  assert(iosNativeBridge.includes("legacyAccessGroupKey") && iosNativeBridge.includes("for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey]"), "iOS must migrate and purge legacy application-group Keychain items");
  assert(readSecure.includes("markerStatus == errSecItemNotFound") && readSecure.includes("guard markerStatus == errSecSuccess"), "iOS GitHub PAT reads must require the matching API-origin scope marker");
  assert((iosNativeBridge.match(/rollbackCreatedGitHubPatScope\(createdMarker\)/gu) ?? []).length === 5 && iosNativeBridge.includes("github_pat_scope_rollback_failed"), "iOS failed PAT writes must roll back newly created scope markers");
  assert(writeSecure.includes("previousGitHubPatData = previousData") && writeSecure.includes("rollbackGitHubPatWrite(setting, previousData: previousGitHubPatData)") && writeSecure.includes("github_pat_write_rollback_failed"), "iOS failed legacy cleanup must restore or remove the shared GitHub PAT");
  assert(cleanupDiagnostics.includes("pendingDiagnosticsCleanup = target") && cleanupDiagnostics.includes("error.code == .fileNoSuchFile"), "iOS failed diagnostics cleanup must remain pending while missing files count as clean");
  assert(exportDiagnostics.includes("guard cleanupDiagnosticsTemporaryDirectory()") && finishDiagnosticsExport.includes("if failed || !cleanupSucceeded"), "iOS diagnostics exports must fail closed when temporary cleanup fails");
  assert(purgeSecure.includes('if scope == "logout" || scope == "account-deletion"'), "iOS API-origin changes must preserve pending diagnostics exports");
  assert(purgeSecure.includes("guard diagnosticsCleanupSucceeded else"), "iOS destructive purges must propagate diagnostics cleanup failures");
  assert(iosNativeBridge.includes("WidgetStateStore(appGroup: appGroup)") && !iosNativeBridge.includes("UserDefaults(suiteName: appGroup)"), "iOS live widget state must use the backup-excluded App Group file store");
  assert(iosNativeBridge.includes('widgetKeychainService = "io.delino.devhud.widget-credential.v1"') && iosNativeBridge.includes("widgetAccessGroupKey"), "iOS widget credentials must use a distinct selected-only Keychain service");
  assert(purgeSecure.includes("clearWidgetState()") && purgeSecure.indexOf("clearWidgetState()") < purgeSecure.indexOf("purgeSecureGroup(args"), "iOS destructive purges must clear widget state before the authoritative secure store");
  assert(iosNativeBridge.includes("reconcileWidgetCredentials()") && iosNativeBridge.includes("widgetCredentialDeckIds()"), "iOS must reconcile interrupted or orphaned widget credentials");
  assert(writeSecure.indexOf("beginWidgetCredentialReplacement") >= 0 && writeSecure.indexOf("beginWidgetCredentialReplacement") < writeSecure.indexOf("guard storeData(data"), "iOS PAT replacement must persist its widget transaction before changing the main PAT");
  assert(beginWidgetCredentialReplacement.includes(".sorted()") && beginWidgetCredentialReplacement.includes("widgetStateStore.updateMetadata") && beginWidgetCredentialReplacement.includes("metadata.credentialReplacement = encoded"), "iOS PAT replacement must durably record the complete ordered Deck set");
  assert(replaceWidgetCredentials.includes("for deckId in transaction.deckIds {") && replaceWidgetCredentials.indexOf("for deckId in transaction.deckIds {") < replaceWidgetCredentials.indexOf("metadata.credentialReplacement = nil"), "iOS PAT replacement must update every recorded Deck before clearing its transaction");
  assert(reconcileWidgetCredentialReplacement.includes("githubPatScope(transaction.scopeId, transaction.profileId)") && reconcileWidgetCredentialReplacement.indexOf("githubPatScope(transaction.scopeId, transaction.profileId)") < reconcileWidgetCredentialReplacement.indexOf("readDataMigratingLegacy(setting)") && reconcileWidgetCredentialReplacement.includes("replaceWidgetCredentials(transaction, data:"), "iOS must reconcile interrupted widget replacements from the authoritative profile scope and main PAT");
  assert(removeGitHubPatScope.includes("switch beginWidgetCredentialReplacement(profileId: profileId, scopeId: scopeId)") && removeGitHubPatScope.indexOf("switch beginWidgetCredentialReplacement") < removeGitHubPatScope.indexOf("deleteData(pat") && removeGitHubPatScope.includes("replaceWidgetCredentials(widgetCredentialReplacement, data: nil)"), "iOS profile-scope removal must durably block and remove copied widget credentials around the authoritative deletion");
  assert(purgeSecure.includes("clearWidgetState()"), "iOS destructive cleanup must remove widget replacement transactions");
  const transactionWrite = "state?.transactionPending = true";
  assert(enableWidgetDeck.includes(transactionWrite) && enableWidgetDeck.indexOf(transactionWrite) < enableWidgetDeck.indexOf("storeWidgetCredential(widgetCredential"), "iOS widget enablement must persist its transaction marker before Keychain mutation");
  assert(enableWidgetDeck.includes("previous == configuration") && enableWidgetDeck.includes("widgetCredentialMatchesAuthoritative(previousCredentialData, authoritative: patData)") && enableWidgetDeck.indexOf("previous == configuration") < enableWidgetDeck.indexOf(transactionWrite), "iOS unchanged widget enablement must avoid reloading before foreground snapshot publication");
  assert(enableWidgetDeck.includes("previousState: previousState") && enableWidgetDeck.includes("previousCredentialData: previousCredentialData"), "iOS widget updates must retain prior state for rollback");
  assert(enableWidgetDeck.includes("state?.snapshot = nil") && enableWidgetDeck.includes("state?.foregroundReloadDeadline = nil"), "iOS widget selection changes must invalidate the Deck-scoped stored-only reload marker");
  assert(enableWidgetDeck.includes("guard removeWidgetDeck(configuration.deckId)") && enableWidgetDeck.indexOf("removeWidgetDeck(configuration.deckId)") < enableWidgetDeck.indexOf('invoke.reject("not-configured"'), "iOS missing-PAT rejection must follow durable widget cleanup");
  assert(coordinatedSnapshotUpdate.includes("current?.snapshot.flatMap") && coordinatedSnapshotUpdate.includes("mergeWidgetSnapshot(current: latestSnapshot, incoming: snapshot)") && coordinatedSnapshotUpdate.indexOf("current?.snapshot.flatMap") < coordinatedSnapshotUpdate.indexOf("mergeWidgetSnapshot") && coordinatedSnapshotUpdate.indexOf("mergeWidgetSnapshot") < coordinatedSnapshotUpdate.indexOf("current?.snapshot = encoded"), "iOS foreground widget snapshots must merge the latest stored snapshot inside the coordinated write");
  assert((replaceWidgetSnapshot.match(/incomingWidgetTimestampIsNewer\(/gu) ?? []).length === 3 && replaceWidgetSnapshot.includes("currentIsFuture != incomingIsFuture") && replaceWidgetSnapshot.includes("return currentIsFuture"), "iOS foreground widget snapshot ordering must survive backward clock corrections");
  assert(replaceWidgetSnapshot.includes("current?.foregroundReloadDeadline = Date().addingTimeInterval") && replaceWidgetSnapshot.indexOf("widgetStateStore.updateDeckState") < replaceWidgetSnapshot.indexOf("reloadAllTimelines()"), "iOS foreground widget snapshots must persist a Deck-scoped stored-only reload marker before reloading WidgetKit");
  assert(removeWidgetDeck.includes("widgetStateStore.updateDeckState") && removeWidgetDeck.includes("removeWidgetCredential(deckId)") && removeWidgetDeck.indexOf("widgetStateStore.updateDeckState") < removeWidgetDeck.indexOf("removeWidgetCredential(deckId)"), "iOS widget cleanup must persist App Group deletion before Keychain deletion");
  assert(abortWidgetTransaction.includes("storeWidgetCredential(previousCredentialData") && abortWidgetTransaction.includes("state = previousState") && abortWidgetTransaction.indexOf("storeWidgetCredential(previousCredentialData") < abortWidgetTransaction.indexOf("state = previousState"), "iOS widget update rollback must restore prior credential and file state");
}

export function assertNativeWidgetPullRequestMetadata(androidProvider, iosWidget) {
  const androidRefresh = androidProvider.match(/private fun refreshGitHub[\s\S]*?(?=\n        private fun validateRepositories)/u)?.[0] ?? "";
  assert(androidRefresh.includes('val incompleteResults = payload.opt("incomplete_results") as? Boolean') && androidRefresh.includes("if (incompleteResults) return@github failure"), "Android widget refresh must require an exact false incomplete_results flag");
  assert(androidRefresh.includes('val nodeId = item.opt("node_id") as? String') && androidRefresh.includes('val number = item.opt("number") as? Int') && androidRefresh.includes('val title = item.opt("title") as? String') && androidRefresh.includes('val repositoryUrl = item.opt("repository_url") as? String'), "Android widget refresh must require exact result field types");
  assert(androidRefresh.includes('.put("nodeId", nodeId)') && androidRefresh.includes('.put("number", number)') && androidRefresh.includes('.put("title", title)') && androidRefresh.includes('.put("repository", repositoryName(repositoryUrl))'), "Android widget refresh must publish only validated result fields");
  assert(/item\.optJSONObject\("pull_request"\)[\s\S]*?\?: return@github failure/u.test(androidRefresh), "Android widget refresh must reject a missing or non-object pull_request");
  assert(androidRefresh.includes('!pullRequest.has("merged_at")') && androidRefresh.includes("mergedAt !== JSONObject.NULL && mergedAt !is String"), "Android widget refresh must require merged_at to be a string or null");
  assert(androidRefresh.includes("val isMerged = mergedAt is String"), "Android widget refresh must derive merged state from validated metadata");

  const iosRefresh = iosWidget.match(/private static func refresh\(deck:[\s\S]*?(?=\n    private static func validateRepositories)/u)?.[0] ?? "";
  assert(iosRefresh.includes('exactWidgetBoolean(root["incomplete_results"]) == false'), "iOS widget refresh must require an exact false incomplete_results flag");
  assert(iosRefresh.includes('item["pull_request"] as? [String: Any]') && iosRefresh.includes('let mergedAt = pull["merged_at"]'), "iOS widget refresh must reject a missing or non-object pull_request");
  assert(iosRefresh.includes("mergedAt is String || mergedAt is NSNull"), "iOS widget refresh must require merged_at to be a string or null");
  assert(iosRefresh.includes("let isMerged = mergedAt is String"), "iOS widget refresh must derive merged state from validated metadata");
}

export function assertMobileDependencyResolution(verifier) {
  assert(!verifier.includes('"--no-default-features"'), "mobile dependency closure must include production default features");
}

export function assertAndroidPermissions(androidManifest, androidDebugManifest) {
  assert((androidDebugManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidDebugManifest.includes("android.permission.INTERNET"), "debug Android manifest must grant only development networking");
  assert((androidManifest.match(/<uses-permission/gu) ?? []).length === 2 && androidManifest.includes("android.permission.INTERNET") && androidManifest.includes("android.permission.POST_NOTIFICATIONS"), "release Android must grant only System WebView networking and notifications");
}

export function assertAndroidWidgetJobService({ androidManifest, androidPluginManifest }) {
  for (const [source, manifest] of [["app override", androidManifest], ["plugin", androidPluginManifest]]) {
    const declarations = (manifest.match(/<service\b[^>]*>/gu) ?? [])
      .filter((declaration) => declaration.includes('android:name="io.delino.devhud.widget.DevHudWidgetRefreshService"'));
    assert(declarations.length === 1, `Android ${source} manifest must declare exactly one widget refresh JobService`);
    assert(declarations[0].includes('android:exported="true"'), `Android ${source} widget refresh JobService must be exported`);
    assert(declarations[0].includes('android:permission="android.permission.BIND_JOB_SERVICE"'), `Android ${source} widget refresh JobService must require BIND_JOB_SERVICE`);
  }
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

export function assertAndroidNativeLibrary(nativeLibrary) {
  // The shared frontend embeds the pinned desktop Chromium revision for validation;
  // native CEF exports, rather than that inert metadata, identify a leaked runtime.
  assert(!/libcef|cef_initialize/iu.test(nativeLibrary), "CEF symbols leaked into the Android native library");
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

export function assertMobileContracts({ platforms, tauri, ios, android, cargo, androidManifest, androidDebugManifest, androidBackupRules, androidDataExtractionRules, androidPluginManifest, androidNativeBridge, androidWidgetStore, androidChannelEnglish, androidChannelKorean, iosAppEntitlements, iosNativeBridge, iosWidgetStateStore, iosPlist, packageJson, nativeBridge, app, workflow }) {
  assert(platforms.schemaVersion === 1, "unsupported mobile platform schema");
  assert(platforms.identity === "io.delino.devhud" && tauri.identifier === platforms.identity, "mobile identity changed");
  assert(platforms.deepLinkScheme === "devhud", "deep-link scheme changed");
  assert(platforms.authCallback === "devhud://auth/callback", "auth callback changed");
  assert(platforms.frontendDist === "../dist" && tauri.build.frontendDist === platforms.frontendDist, "mobile frontend is not shared");
  assert(platforms.minimumVersions.ios === "16.0" && ios.bundle.iOS.minimumSystemVersion === "16.0", "iOS minimum must be 16.0");
  assert(platforms.minimumVersions.androidApi === 29 && android.bundle.android.minSdkVersion === 29, "Android minimum must be API 29");
  assert(platforms.widgets?.iosBundle === "io.delino.devhud.widget" && platforms.widgets?.iosAppGroup === "group.io.delino.devhud" && platforms.widgets?.iosKeychainGroup === "$(AppIdentifierPrefix)io.delino.devhud.shared", "iOS widget identity or secure groups changed");
  assert(platforms.widgets?.androidProvider === "io.delino.devhud.widget.DevHudWidgetProvider" && platforms.widgets?.deepLinkTemplate === "devhud://deck/<deck-id>", "Android widget provider or Deck deep link changed");
  assert(platforms.widgets?.refreshMinutes === 30 && platforms.widgets?.staleMinutes === 60 && platforms.widgets?.resultLimit === 100 && platforms.widgets?.previewLimit === 3, "widget refresh or result bounds changed");

  assertMobileTargets(platforms.targets);

  const mobileCargo = cargo.match(/\[target\.'cfg\(any\(target_os = "android", target_os = "ios"\)\)'\.dependencies\]([\s\S]*?)(?=\n\[|$)/u)?.[1] ?? "";
  assert(mobileCargo.includes('features = ["wry"]'), "mobile Tauri system-webview features changed");
  assert(!/cef|chromium|chrome-extension/iu.test(mobileCargo), "CEF or browser-extension dependency leaked into the mobile dependency set");
  assert(/features = \["cef"/u.test(cargo), "desktop CEF contract was lost");

  assertAndroidPermissions(androidManifest, androidDebugManifest);
  assert(androidManifest.includes('android:scheme="market"'), "Android market handler visibility is missing");
  for (const manifest of [androidManifest, androidPluginManifest]) assert(manifest.includes('android.intent.category.HOME'), "Android launcher visibility for trusted widget configuration is missing");
  assert(!androidManifest.includes("LEANBACK") && !androidManifest.includes("FileProvider"), "unneeded Android surface was generated");
  assert((androidManifest.match(/android:scheme="devhud"/gu) ?? []).length === 2, "Android must register only the auth and Deck devhud routes");
  assert(androidManifest.includes('android:host="auth" android:path="/callback"'), "Android auth callback filter changed");
  assert(androidManifest.includes('android:host="deck" android:pathPattern="/.*"'), "Android Deck widget deep-link filter is missing");
  assertAndroidBackupExclusions({ androidManifest, androidBackupRules, androidDataExtractionRules });
  assertAndroidWidgetJobService({ androidManifest, androidPluginManifest });
  assertAndroidNativeBridge(androidNativeBridge);
  assertAndroidWidgetStore(androidWidgetStore);
  assert(androidChannelEnglish.includes("Deck changes") && androidChannelKorean.includes("Deck 변경사항"), "Android notification channel names must be bilingual");
  assert((androidPluginManifest.match(/<uses-permission/gu) ?? []).length === 1 && androidPluginManifest.includes("android.permission.POST_NOTIFICATIONS"), "Android native bridge permissions are not least-privileged");
  assert(androidPluginManifest.includes("DevHudWidgetProvider") && androidPluginManifest.includes("DevHudWidgetConfigureActivity"), "Android AppWidgetProvider and one-Deck configuration activity are missing");
  assert((iosPlist.match(/<string>devhud<\/string>/gu) ?? []).length === 1, "iOS must register only one devhud scheme");
  assert(iosPlist.includes("DevHudLegacyKeychainAccessGroup") && iosPlist.includes("DevHudWidgetKeychainAccessGroup") && iosPlist.includes("$(AppIdentifierPrefix)io.delino.devhud.shared"), "iOS widget and migration Keychain groups changed");
  assert(iosAppEntitlements.includes("group.io.delino.devhud") && iosAppEntitlements.includes("$(AppIdentifierPrefix)io.delino.devhud") && iosAppEntitlements.includes("$(AppIdentifierPrefix)io.delino.devhud.shared"), "iOS application widget-sharing entitlements changed");
  assert(!/com\.apple\.developer\.|NSExtension/iu.test(iosPlist), "uncontracted iOS entitlement or extension detected");
  assertIosNativeBridge(iosNativeBridge, iosWidgetStateStore);

  assert(packageJson.scripts["build:ios"] && packageJson.scripts["build:android"] && packageJson.scripts["mobile:generate"], "package-local mobile commands are incomplete");
  for (const operation of ["runtime.snapshot", "lifecycle.open-external", "auth.peek-pending-callback", "auth.take-pending-callback", "secure.read", "secure.write", "notifications.request-permission", "updates.status", "widgets.replace-deck-snapshot"]) assert(nativeBridge.includes(`\"${operation}\"`), `typed bridge operation missing: ${operation}`);
  assert(nativeBridge.includes("readonly widgets: boolean"), "runtime widget capability must be platform-reported");
  assert(app.includes("mobile &&") && app.includes("copy.realqaMobileTitle"), "mobile RealQA unavailable state is missing");
  assert(app.includes("!mobile") && app.includes("ExternalLinkTarget.Issue"), "issue creation is not explicitly desktop-only");
  assert(workflow.includes("devhud-mobile-contracts") && workflow.includes("devhud-android-emulator"), "mobile CI validation jobs are incomplete");
  assertMobileCi(workflow);
}
