import Foundation
import OSLog
import Security
import Tauri
import UIKit
import UserNotifications
import WebKit
import WidgetKit

private let keychainService = "io.delino.devhud.secure-settings.v1"
private let widgetKeychainService = "io.delino.devhud.widget-credential.v1"
private let appGroup = "group.io.delino.devhud"
private let sharedAccessGroupKey = "DevHudKeychainAccessGroup"
private let legacyAccessGroupKey = "DevHudLegacyKeychainAccessGroup"
private let widgetAccessGroupKey = "DevHudWidgetKeychainAccessGroup"
private let widgetConfigurationPrefix = "widget.configuration."
private let widgetSnapshotPrefix = "widget.snapshot."
private let widgetTransactionPrefix = "widget.transaction."
private let secureStoreLogger = Logger(subsystem: "io.delino.devhud", category: "secure-store")

private struct SecureSetting: Decodable {
    let kind: String
    let profileId: String
    let scopeId: String?
    var account: String { "\(kind):\(profileId)" }
}

private struct DeckNotification: Decodable {
    let id: String
    let deckId: String
    let title: String
    let body: String
}

private struct WidgetDeckConfiguration: Codable {
    let version: Int
    let deckId: String
    let name: String
    let query: String
    let repositories: [WidgetRepository]
    let profileId: String
    let profileKind: String
    let scopeId: String
    let language: String
}

private struct WidgetRepository: Codable { let owner: String; let name: String }

private struct WidgetDeckCounts: Codable { let total: Int; let open: Int; let draft: Int; let merged: Int; let closed: Int; let bounded: Bool }
private struct WidgetPullRequest: Codable { let nodeId: String; let number: Int; let title: String; let repository: String; let state: String; let draft: Bool }
private struct WidgetRate: Codable { let limit: Int?; let remaining: Int?; let used: Int?; let resetAt: String?; let resource: String?; let retryAfterSeconds: Int? }
private struct WidgetDeckSnapshot: Codable {
    let version: Int
    let deckId: String
    let query: String
    let counts: WidgetDeckCounts
    let results: [WidgetPullRequest]
    let state: String
    let lastSuccessfulAt: String?
    let lastAttemptedAt: String
    let rate: WidgetRate?
}

private struct RequestArgs: Decodable {
    let operation: String
    let target: String?
    let apiOrigin: String?
    let url: String?
    let issuer: String?
    let suggestedName: String?
    let contents: String?
    let setting: SecureSetting?
    let value: String?
    let scope: String?
    let profileId: String?
    let scopeId: String?
    let profileIds: [String]?
    let notification: DeckNotification?
    let deckId: String?
    let configuration: WidgetDeckConfiguration?
    let snapshot: WidgetDeckSnapshot?
}

final class DevhudNativePlugin: Plugin, UNUserNotificationCenterDelegate, UIDocumentPickerDelegate {
    private var pendingDiagnosticsExport: (Invoke, URL)?
    private var pendingDiagnosticsCleanup: URL?

    @objc public override func load(webview: WKWebView) {
        cleanupDiagnosticsTemporaryDirectory()
        if !migrateLegacySecureStore() {
            secureStoreLogger.error("event=legacy_secure_store_migration_failed")
        }
        guard UserDefaults(suiteName: appGroup) != nil else { return }
        if !reconcileWidgetCredentials() {
            secureStoreLogger.error("event=widget_credential_reconciliation_failed")
        }
        UNUserNotificationCenter.current().delegate = self
    }

    @objc func request(_ invoke: Invoke) throws {
        let args = try invoke.parseArgs(RequestArgs.self)
        switch args.operation {
        case "runtime.snapshot":
            invoke.resolve(["kind": "runtime-os-version", "osVersion": UIDevice.current.systemVersion])
        case "lifecycle.open-external": try openExternal(args, invoke)
        case "auth.open-system-browser": try openAuthenticationBrowser(args, invoke)
        case "diagnostics.export": try exportDiagnostics(args, invoke)
        case "secure.read": try readSecure(args, invoke)
        case "secure.write": try writeSecure(args, invoke)
        case "secure.remove": try removeSecure(args, invoke)
        case "secure.reconcile-github-pats": try reconcileGitHubPats(args, invoke)
        case "secure.purge": try purgeSecure(args, invoke)
        case "notifications.permission": notificationPermission(invoke)
        case "notifications.request-permission": requestNotificationPermission(invoke)
        case "notifications.publish-deck-change": try publishNotification(args, invoke)
        case "notifications.cancel-deck": try cancelNotifications(args, invoke)
        case "updates.status":
            let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
            invoke.resolve(["kind": "update-status", "store": "app-store", "installedVersion": version, "configured": false])
        case "updates.open-store": invoke.reject("not-configured", code: "not-configured")
        case "widgets.status": widgetStatus(invoke)
        case "widgets.enable-deck": try enableWidgetDeck(args, invoke)
        case "widgets.replace-deck-snapshot": try replaceWidgetSnapshot(args, invoke)
        case "widgets.disable-deck": try disableWidgetDeck(args, invoke)
        default: invoke.reject("invalid-argument", code: "invalid-argument")
        }
    }

    private func openAuthenticationBrowser(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let value = args.url, let issuerValue = args.issuer,
              let destination = URL(string: value), let issuer = URL(string: issuerValue),
              issuer.user == nil, issuer.password == nil, issuer.query == nil, issuer.fragment == nil,
              isSecureOrLoopback(issuer), destination.scheme == issuer.scheme, destination.host == issuer.host,
              destination.port == issuer.port, destination.user == nil, destination.password == nil,
              destination.fragment == nil else { throw NativeError.invalidArgument }
        UIApplication.shared.open(destination, options: [:]) { opened in
            if opened { invoke.resolve(["kind": "ok"]) }
            else { invoke.reject("platform-failure", code: "platform-failure") }
        }
    }

    private func exportDiagnostics(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard pendingDiagnosticsExport == nil,
              let suggestedName = args.suggestedName,
              let contents = args.contents,
              contents.lengthOfBytes(using: .utf8) <= 1024 * 1024,
              suggestedName.range(
                of: #"^devhud-diagnostics-[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.json$"#,
                options: .regularExpression
              ) != nil,
              let data = contents.data(using: .utf8),
              (try JSONSerialization.jsonObject(with: data)) is [String: Any]
        else { throw NativeError.invalidArgument }

        let fileManager = FileManager.default
        let directory = diagnosticsTemporaryDirectory()
        guard cleanupDiagnosticsTemporaryDirectory() else {
            invoke.reject("storage-failure", code: "storage-failure")
            return
        }
        do {
            try fileManager.createDirectory(at: directory, withIntermediateDirectories: true)
            let source = directory.appendingPathComponent(suggestedName, isDirectory: false)
            try data.write(to: source, options: .atomic)
            pendingDiagnosticsExport = (invoke, source)
            DispatchQueue.main.async {
                guard let viewController = self.manager.viewController else {
                    self.finishDiagnosticsExport(saved: false, failed: true)
                    return
                }
                let picker = UIDocumentPickerViewController(url: source, in: .exportToService)
                picker.delegate = self
                picker.modalPresentationStyle = .fullScreen
                viewController.present(picker, animated: true)
            }
        } catch {
            cleanupDiagnosticsTemporaryDirectory(at: directory)
            invoke.reject("storage-failure", code: "storage-failure")
        }
    }

    @discardableResult
    private func cleanupDiagnosticsTemporaryDirectory(at directory: URL? = nil) -> Bool {
        let target = directory ?? pendingDiagnosticsCleanup ?? diagnosticsTemporaryDirectory()
        do {
            try FileManager.default.removeItem(at: target)
            pendingDiagnosticsCleanup = nil
            return true
        } catch let error as CocoaError where error.code == .fileNoSuchFile {
            pendingDiagnosticsCleanup = nil
            return true
        } catch {
            pendingDiagnosticsCleanup = target
            return false
        }
    }

    @discardableResult
    private func finishDiagnosticsExport(saved: Bool, failed: Bool = false) -> Bool {
        guard let (invoke, source) = pendingDiagnosticsExport else { return true }
        pendingDiagnosticsExport = nil
        let cleanupSucceeded = cleanupDiagnosticsTemporaryDirectory(at: source.deletingLastPathComponent())
        if failed || !cleanupSucceeded {
            invoke.reject("storage-failure", code: "storage-failure")
        } else {
            invoke.resolve(["kind": "diagnostics-export", "outcome": saved ? "saved" : "cancelled"])
        }
        return cleanupSucceeded
    }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        finishDiagnosticsExport(saved: !urls.isEmpty)
    }

    func documentPickerWasCancelled(_ controller: UIDocumentPickerViewController) {
        finishDiagnosticsExport(saved: false)
    }

    private func isSecureOrLoopback(_ url: URL) -> Bool {
        if url.scheme == "https" { return true }
        let host = url.host ?? ""
        return url.scheme == "http" && (host == "localhost" || host == "::1" || host.hasPrefix("127."))
    }

    private func openExternal(_ args: RequestArgs, _ invoke: Invoke) throws {
        let destination: URL
        if args.target == "fine-grained-pat" {
            destination = URL(string: "https://github.com/settings/personal-access-tokens/new?contents=read&issues=write&metadata=read&pull_requests=read")!
        } else if args.target == "classic-pat" {
            destination = URL(string: "https://github.com/settings/tokens/new?scopes=repo")!
        } else if args.target == "authentication", let origin = args.apiOrigin, let url = URL(string: origin) {
            let host = url.host ?? ""
            let loopback = host == "localhost" || host == "::1" || host.hasPrefix("127.")
            guard (url.scheme == "https" || (url.scheme == "http" && loopback)), (url.path.isEmpty || url.path == "/"), url.query == nil, url.fragment == nil, url.user == nil, url.password == nil else { throw NativeError.invalidArgument }
            destination = url
        } else {
            throw NativeError.invalidArgument
        }
        UIApplication.shared.open(destination, options: [:]) { opened in
            if opened { invoke.resolve(["kind": "ok"]) }
            else { invoke.reject("platform-failure", code: "platform-failure") }
        }
    }

    private func query(_ setting: SecureSetting, accessGroupKey: String) -> [String: Any] {
        var item: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: keychainService,
         kSecAttrAccount as String: setting.account,
         kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
         kSecAttrSynchronizable as String: false]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: accessGroupKey) as? String {
            item[kSecAttrAccessGroup as String] = accessGroup
        }
        return item
    }

    private func readData(_ setting: SecureSetting, accessGroupKey: String) -> (OSStatus, Data?) {
        var itemQuery = query(setting, accessGroupKey: accessGroupKey)
        itemQuery[kSecReturnData as String] = true
        itemQuery[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(itemQuery as CFDictionary, &item)
        return (status, item as? Data)
    }

    private func storeData(_ data: Data, setting: SecureSetting, accessGroupKey: String) -> OSStatus {
        let itemQuery = query(setting, accessGroupKey: accessGroupKey)
        let status = SecItemUpdate(itemQuery as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status != errSecItemNotFound { return status }
        var item = itemQuery
        item[kSecValueData as String] = data
        return SecItemAdd(item as CFDictionary, nil)
    }

    private func deleteData(_ setting: SecureSetting, accessGroupKey: String) -> OSStatus {
        SecItemDelete(query(setting, accessGroupKey: accessGroupKey) as CFDictionary)
    }

    private func migrateLegacySecureStore() -> Bool {
        var all: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                  kSecAttrService as String: keychainService,
                                  kSecReturnAttributes as String: true,
                                  kSecReturnData as String: true,
                                  kSecMatchLimit as String: kSecMatchLimitAll]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: legacyAccessGroupKey) as? String {
            all[kSecAttrAccessGroup as String] = accessGroup
        }
        var result: CFTypeRef?
        let status = SecItemCopyMatching(all as CFDictionary, &result)
        if status == errSecItemNotFound { return true }
        guard status == errSecSuccess, let items = result as? [[String: Any]] else { return false }
        for item in items {
            guard let account = item[kSecAttrAccount as String] as? String,
                  let separator = account.firstIndex(of: ":"),
                  let data = item[kSecValueData as String] as? Data else { return false }
            let kind = String(account[..<separator])
            let profileId = String(account[account.index(after: separator)...])
            let setting = SecureSetting(kind: kind, profileId: profileId, scopeId: nil)
            let primary = readData(setting, accessGroupKey: sharedAccessGroupKey)
            guard primary.0 == errSecSuccess || primary.0 == errSecItemNotFound else { return false }
            if primary.0 == errSecItemNotFound,
               storeData(data, setting: setting, accessGroupKey: sharedAccessGroupKey) != errSecSuccess { return false }
            let deletion = deleteData(setting, accessGroupKey: legacyAccessGroupKey)
            guard deletion == errSecSuccess || deletion == errSecItemNotFound else { return false }
        }
        return true
    }

    private func readDataMigratingLegacy(_ setting: SecureSetting) -> (OSStatus, Data?) {
        let primary = readData(setting, accessGroupKey: sharedAccessGroupKey)
        if primary.0 != errSecItemNotFound { return primary }

        let legacy = readData(setting, accessGroupKey: legacyAccessGroupKey)
        guard legacy.0 == errSecSuccess, let data = legacy.1 else { return legacy }
        let stored = storeData(data, setting: setting, accessGroupKey: sharedAccessGroupKey)
        guard stored == errSecSuccess else { return (stored, nil) }
        let deleted = deleteData(setting, accessGroupKey: legacyAccessGroupKey)
        guard deleted == errSecSuccess || deleted == errSecItemNotFound else { return (deleted, nil) }
        return (errSecSuccess, data)
    }

    private func rejectStorageFailure(_ invoke: Invoke) {
        invoke.reject("storage-failure", code: "storage-failure")
    }

    private func readSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let setting = args.setting else { throw NativeError.invalidArgument }
        if setting.kind == "github-pat" {
            guard let scopeId = setting.scopeId else { throw NativeError.invalidArgument }
            let (markerStatus, _) = readDataMigratingLegacy(githubPatScope(scopeId, setting.profileId))
            if markerStatus == errSecItemNotFound {
                invoke.resolve(["kind": "secure-value", "value": NSNull()])
                return
            }
            guard markerStatus == errSecSuccess else {
                rejectStorageFailure(invoke)
                return
            }
        }
        let (status, storedData) = readDataMigratingLegacy(setting)
        if status == errSecSuccess, let data = storedData, let value = String(data: data, encoding: .utf8) {
            invoke.resolve(["kind": "secure-value", "value": value])
            return
        }
        if status == errSecItemNotFound {
            invoke.resolve(["kind": "secure-value", "value": NSNull()])
        } else {
            rejectStorageFailure(invoke)
        }
    }

    private func writeSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let setting = args.setting, let value = args.value, let data = value.data(using: .utf8) else { throw NativeError.invalidArgument }
        var createdMarker: SecureSetting?
        var previousGitHubPatData: Data?
        if setting.kind == "github-pat" {
            guard let scopeId = setting.scopeId, let markerData = "1".data(using: .utf8) else { throw NativeError.invalidArgument }
            let (previousStatus, previousData) = readDataMigratingLegacy(setting)
            guard previousStatus == errSecItemNotFound || (previousStatus == errSecSuccess && previousData != nil) else {
                rejectStorageFailure(invoke)
                return
            }
            previousGitHubPatData = previousData
            let marker = githubPatScope(scopeId, setting.profileId)
            let (markerStatus, _) = readDataMigratingLegacy(marker)
            guard markerStatus == errSecSuccess || markerStatus == errSecItemNotFound else {
                rejectStorageFailure(invoke)
                return
            }
            if markerStatus == errSecItemNotFound {
                guard storeData(markerData, setting: marker, accessGroupKey: sharedAccessGroupKey) == errSecSuccess else {
                    rejectStorageFailure(invoke)
                    return
                }
                createdMarker = marker
            }
        }
        guard storeData(data, setting: setting, accessGroupKey: sharedAccessGroupKey) == errSecSuccess else {
            rollbackCreatedGitHubPatScope(createdMarker)
            rejectStorageFailure(invoke)
            return
        }
        let legacyDeletion = deleteData(setting, accessGroupKey: legacyAccessGroupKey)
        guard legacyDeletion == errSecSuccess || legacyDeletion == errSecItemNotFound else {
            if setting.kind != "github-pat" || rollbackGitHubPatWrite(setting, previousData: previousGitHubPatData) {
                rollbackCreatedGitHubPatScope(createdMarker)
            }
            rejectStorageFailure(invoke)
            return
        }
        if setting.kind == "github-pat" {
            guard let scopeId = setting.scopeId,
                  replaceWidgetCredentials(profileId: setting.profileId, scopeId: scopeId, data: data) else {
                _ = rollbackGitHubPatWrite(setting, previousData: previousGitHubPatData)
                rollbackCreatedGitHubPatScope(createdMarker)
                rejectStorageFailure(invoke)
                return
            }
            WidgetCenter.shared.reloadAllTimelines()
        }
        invoke.resolve(["kind": "ok"])
    }

    private func rollbackGitHubPatWrite(_ setting: SecureSetting, previousData: Data?) -> Bool {
        let rollback: OSStatus
        if let previousData {
            rollback = storeData(previousData, setting: setting, accessGroupKey: sharedAccessGroupKey)
        } else {
            rollback = deleteData(setting, accessGroupKey: sharedAccessGroupKey)
        }
        if rollback != errSecSuccess && rollback != errSecItemNotFound {
            secureStoreLogger.error("event=github_pat_write_rollback_failed")
            return false
        }
        return true
    }

    private func rollbackCreatedGitHubPatScope(_ marker: SecureSetting?) {
        guard let marker else { return }
        let rollback = deleteData(marker, accessGroupKey: sharedAccessGroupKey)
        if rollback != errSecSuccess && rollback != errSecItemNotFound {
            secureStoreLogger.error("event=github_pat_scope_rollback_failed")
        }
    }

    private func removeSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let setting = args.setting else { throw NativeError.invalidArgument }
        if setting.kind == "github-pat" {
            guard let scopeId = setting.scopeId else { throw NativeError.invalidArgument }
            guard removeGitHubPatScope(scopeId, setting.profileId) else { rejectStorageFailure(invoke); return }
            invoke.resolve(["kind": "ok"])
            return
        }
        for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey] {
            let status = deleteData(setting, accessGroupKey: accessGroupKey)
            guard status == errSecSuccess || status == errSecItemNotFound else { rejectStorageFailure(invoke); return }
        }
        invoke.resolve(["kind": "ok"])
    }

    private func githubPatScope(_ scopeId: String, _ profileId: String) -> SecureSetting {
        SecureSetting(kind: "github-pat-scope", profileId: "\(scopeId):\(profileId)", scopeId: nil)
    }

    private func secureAccounts(accessGroupKey: String) -> [String]? {
        var all: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                  kSecAttrService as String: keychainService,
                                  kSecReturnAttributes as String: true,
                                  kSecMatchLimit as String: kSecMatchLimitAll]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: accessGroupKey) as? String {
            all[kSecAttrAccessGroup as String] = accessGroup
        }
        var result: CFTypeRef?
        let status = SecItemCopyMatching(all as CFDictionary, &result)
        if status == errSecItemNotFound { return [] }
        guard status == errSecSuccess, let items = result as? [[String: Any]] else { return nil }
        return items.compactMap { $0[kSecAttrAccount as String] as? String }
    }

    private func removeGitHubPatScope(_ scopeId: String, _ profileId: String) -> Bool {
        guard let primaryAccounts = secureAccounts(accessGroupKey: sharedAccessGroupKey),
              let legacyAccounts = secureAccounts(accessGroupKey: legacyAccessGroupKey) else { return false }
        let accounts = Set(primaryAccounts + legacyAccounts)
        let marker = githubPatScope(scopeId, profileId)
        let markerSuffix = ":\(profileId)"
        let retainedElsewhere = accounts.contains { account in
            account.hasPrefix("github-pat-scope:") && account.hasSuffix(markerSuffix) && account != marker.account
        }
        if !retainedElsewhere {
            let pat = SecureSetting(kind: "github-pat", profileId: profileId, scopeId: nil)
            for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey] {
                let deletion = deleteData(pat, accessGroupKey: accessGroupKey)
                if deletion != errSecSuccess && deletion != errSecItemNotFound { return false }
            }
        }
        for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey] {
            let deletion = deleteData(marker, accessGroupKey: accessGroupKey)
            if deletion != errSecSuccess && deletion != errSecItemNotFound { return false }
        }
        return true
    }

    private func reconcileGitHubPats(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let scopeId = args.scopeId, let profileIds = args.profileIds, profileIds.count <= 25,
              Set(profileIds).count == profileIds.count else { throw NativeError.invalidArgument }
        guard migrateLegacySecureStore() else { rejectStorageFailure(invoke); return }
        guard let sharedAccounts = secureAccounts(accessGroupKey: sharedAccessGroupKey),
              let legacyAccounts = secureAccounts(accessGroupKey: legacyAccessGroupKey) else {
            rejectStorageFailure(invoke)
            return
        }
        let retained = Set(profileIds)
        for profileId in retained where sharedAccounts.contains("github-pat:\(profileId)") || legacyAccounts.contains("github-pat:\(profileId)") {
            let marker = githubPatScope(scopeId, profileId)
            if !sharedAccounts.contains(marker.account) {
                guard let markerData = "1".data(using: .utf8), storeData(markerData, setting: marker, accessGroupKey: sharedAccessGroupKey) == errSecSuccess else {
                    rejectStorageFailure(invoke)
                    return
                }
            }
        }
        let prefix = "github-pat-scope:\(scopeId):"
        for account in Set(sharedAccounts + legacyAccounts) where account.hasPrefix(prefix) {
            let profileId = String(account.dropFirst(prefix.count))
            if retained.contains(profileId) { continue }
            guard removeGitHubPatScope(scopeId, profileId) else {
                rejectStorageFailure(invoke)
                return
            }
        }
        invoke.resolve(["kind": "ok"])
    }

    private func purgeSecureGroup(_ args: RequestArgs, accessGroupKey: String) -> Bool {
        let scope = args.scope!
        var all: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                  kSecAttrService as String: keychainService,
                                  kSecReturnAttributes as String: true,
                                  kSecMatchLimit as String: kSecMatchLimitAll]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: accessGroupKey) as? String {
            all[kSecAttrAccessGroup as String] = accessGroup
        }
        var result: CFTypeRef?
        let status = SecItemCopyMatching(all as CFDictionary, &result)
        if status == errSecItemNotFound { return true }
        guard status == errSecSuccess, let items = result as? [[String: Any]] else { return false }
        for item in items {
            guard let account = item[kSecAttrAccount as String] as? String else { continue }
            let remove = scope == "logout"
                || (scope == "account-deletion" && account != "logto-session:\(args.profileId!)")
                || (scope == "api-change" && account == "logto-session:\(args.profileId!)")
            if remove {
                var itemQuery = all
                itemQuery.removeValue(forKey: kSecReturnAttributes as String)
                itemQuery.removeValue(forKey: kSecMatchLimit as String)
                itemQuery[kSecAttrAccount as String] = account
                let deletion = SecItemDelete(itemQuery as CFDictionary)
                if deletion != errSecSuccess && deletion != errSecItemNotFound { return false }
            }
        }
        return true
    }

    private func purgeSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let scope = args.scope, ["logout", "account-deletion", "api-change"].contains(scope),
              scope == "logout" || args.profileId != nil else { throw NativeError.invalidArgument }
        if scope == "logout" || scope == "account-deletion" {
            let diagnosticsCleanupSucceeded: Bool
            if pendingDiagnosticsExport != nil {
                diagnosticsCleanupSucceeded = finishDiagnosticsExport(saved: false)
            } else {
                diagnosticsCleanupSucceeded = cleanupDiagnosticsTemporaryDirectory()
            }
            guard diagnosticsCleanupSucceeded else { rejectStorageFailure(invoke); return }
        }
        for accessGroupKey in [sharedAccessGroupKey, legacyAccessGroupKey] {
            guard purgeSecureGroup(args, accessGroupKey: accessGroupKey) else { rejectStorageFailure(invoke); return }
        }
        guard clearWidgetState() else { rejectStorageFailure(invoke); return }
        invoke.resolve(["kind": "ok"])
    }

    private func widgetCredentialQuery(_ deckId: String) -> [String: Any] {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                    kSecAttrService as String: widgetKeychainService,
                                    kSecAttrAccount as String: deckId,
                                    kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
                                    kSecAttrSynchronizable as String: false]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: widgetAccessGroupKey) as? String {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        return query
    }

    private func storeWidgetCredential(_ data: Data, deckId: String) -> OSStatus {
        let query = widgetCredentialQuery(deckId)
        let status = SecItemUpdate(query as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status != errSecItemNotFound { return status }
        var item = query
        item[kSecValueData as String] = data
        return SecItemAdd(item as CFDictionary, nil)
    }

    private func readWidgetCredential(_ deckId: String) -> (OSStatus, Data?) {
        var query = widgetCredentialQuery(deckId)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        return (status, result as? Data)
    }

    private func replaceWidgetCredentials(profileId: String, scopeId: String, data: Data) -> Bool {
        guard let defaults = UserDefaults(suiteName: appGroup) else { return false }
        let deckIds = defaults.dictionaryRepresentation().compactMap { key, value -> String? in
            guard key.hasPrefix(widgetConfigurationPrefix), let encoded = value as? Data,
                  let configuration = try? JSONDecoder().decode(WidgetDeckConfiguration.self, from: encoded),
                  configuration.profileId == profileId, configuration.scopeId == scopeId else { return nil }
            return String(key.dropFirst(widgetConfigurationPrefix.count))
        }
        var previous: [(deckId: String, data: Data?)] = []
        for deckId in deckIds {
            let (status, stored) = readWidgetCredential(deckId)
            guard status == errSecSuccess || status == errSecItemNotFound else { return false }
            previous.append((deckId, stored))
        }
        var updated: [(deckId: String, data: Data?)] = []
        for item in previous {
            if storeWidgetCredential(data, deckId: item.deckId) == errSecSuccess {
                updated.append(item)
                continue
            }
            for rollbackItem in updated {
                let rollbackSucceeded: Bool
                if let stored = rollbackItem.data {
                    rollbackSucceeded = storeWidgetCredential(stored, deckId: rollbackItem.deckId) == errSecSuccess
                } else {
                    rollbackSucceeded = removeWidgetCredential(rollbackItem.deckId)
                }
                if !rollbackSucceeded {
                    secureStoreLogger.error("event=widget_credential_write_rollback_failed")
                }
            }
            return false
        }
        return true
    }

    private func removeWidgetCredential(_ deckId: String) -> Bool {
        let status = SecItemDelete(widgetCredentialQuery(deckId) as CFDictionary)
        return status == errSecSuccess || status == errSecItemNotFound
    }

    private func removeAllWidgetCredentials() -> Bool {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                    kSecAttrService as String: widgetKeychainService,
                                    kSecAttrSynchronizable as String: false]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: widgetAccessGroupKey) as? String {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        let status = SecItemDelete(query as CFDictionary)
        return status == errSecSuccess || status == errSecItemNotFound
    }

    private func widgetCredentialDeckIds() -> (OSStatus, Set<String>) {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                    kSecAttrService as String: widgetKeychainService,
                                    kSecAttrSynchronizable as String: false,
                                    kSecReturnAttributes as String: true,
                                    kSecMatchLimit as String: kSecMatchLimitAll]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: widgetAccessGroupKey) as? String {
            query[kSecAttrAccessGroup as String] = accessGroup
        }
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return (status, []) }
        guard status == errSecSuccess else { return (status, []) }
        let items: [[String: Any]]
        if let values = result as? [[String: Any]] { items = values }
        else if let value = result as? [String: Any] { items = [value] }
        else { return (errSecDecode, []) }
        return (status, Set(items.compactMap { $0[kSecAttrAccount as String] as? String }))
    }

    private func reconcileWidgetCredentials() -> Bool {
        guard let defaults = UserDefaults(suiteName: appGroup) else { return false }
        let keys = defaults.dictionaryRepresentation().keys
        let pendingDeckIds = Set(keys.filter { $0.hasPrefix(widgetTransactionPrefix) }.map { String($0.dropFirst(widgetTransactionPrefix.count)) })
        var changed = false
        for deckId in pendingDeckIds {
            guard removeWidgetCredential(deckId) else { return false }
            defaults.removeObject(forKey: widgetTransactionPrefix + deckId)
            changed = true
        }
        let configuredDeckIds = Set(defaults.dictionaryRepresentation().compactMap { key, value -> String? in
            guard key.hasPrefix(widgetConfigurationPrefix), let encoded = value as? Data,
                  let configuration = try? JSONDecoder().decode(WidgetDeckConfiguration.self, from: encoded) else { return nil }
            let deckId = String(key.dropFirst(widgetConfigurationPrefix.count))
            return configuration.deckId == deckId ? deckId : nil
        })
        let (status, credentialDeckIds) = widgetCredentialDeckIds()
        guard status == errSecSuccess || status == errSecItemNotFound else { return false }
        for deckId in credentialDeckIds where !configuredDeckIds.contains(deckId) {
            guard removeWidgetCredential(deckId) else { return false }
            changed = true
        }
        guard defaults.synchronize() else { return false }
        if changed { WidgetCenter.shared.reloadAllTimelines() }
        return true
    }

    private func abortWidgetTransaction(_ defaults: UserDefaults, deckId: String) -> Bool {
        // Leave the marker in place when Keychain cleanup fails so the widget
        // cannot consume a credential whose matching configuration is unknown.
        guard removeWidgetCredential(deckId) else { return false }
        defaults.removeObject(forKey: widgetTransactionPrefix + deckId)
        return defaults.synchronize()
    }

    private func widgetStatus(_ invoke: Invoke) {
        guard let defaults = UserDefaults(suiteName: appGroup), reconcileWidgetCredentials() else { rejectStorageFailure(invoke); return }
        let enabled = defaults.dictionaryRepresentation().keys.filter { $0.hasPrefix(widgetConfigurationPrefix) }.map { String($0.dropFirst(widgetConfigurationPrefix.count)) }.sorted()
        invoke.resolve(["kind": "widget-status", "enabledDeckIds": enabled])
    }

    private func enableWidgetDeck(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let configuration = args.configuration, let defaults = UserDefaults(suiteName: appGroup) else { throw NativeError.invalidArgument }
        let setting = SecureSetting(kind: "github-pat", profileId: configuration.profileId, scopeId: configuration.scopeId)
        let marker = githubPatScope(configuration.scopeId, configuration.profileId)
        let (markerStatus, _) = readDataMigratingLegacy(marker)
        let (patStatus, patData) = readDataMigratingLegacy(setting)
        guard markerStatus == errSecSuccess, patStatus == errSecSuccess, let patData else {
            defaults.removeObject(forKey: widgetConfigurationPrefix + configuration.deckId)
            defaults.removeObject(forKey: widgetSnapshotPrefix + configuration.deckId)
            _ = removeWidgetCredential(configuration.deckId)
            WidgetCenter.shared.reloadAllTimelines()
            if markerStatus == errSecItemNotFound || patStatus == errSecItemNotFound { invoke.reject("not-configured", code: "not-configured") }
            else { rejectStorageFailure(invoke) }
            return
        }
        do {
            let key = widgetConfigurationPrefix + configuration.deckId
            let transactionKey = widgetTransactionPrefix + configuration.deckId
            let previous = defaults.data(forKey: key).flatMap { try? JSONDecoder().decode(WidgetDeckConfiguration.self, from: $0) }
            let encoded = try JSONEncoder().encode(configuration)
            defaults.set(true, forKey: transactionKey)
            guard defaults.synchronize() else {
                defaults.removeObject(forKey: transactionKey)
                rejectStorageFailure(invoke)
                return
            }
            guard storeWidgetCredential(patData, deckId: configuration.deckId) == errSecSuccess else {
                defaults.removeObject(forKey: transactionKey)
                _ = defaults.synchronize()
                rejectStorageFailure(invoke)
                return
            }
            defaults.set(encoded, forKey: key)
            if let previous, widgetSelectionChanged(previous, configuration) {
                defaults.removeObject(forKey: widgetSnapshotPrefix + configuration.deckId)
            }
            guard defaults.synchronize() else {
                _ = abortWidgetTransaction(defaults, deckId: configuration.deckId)
                rejectStorageFailure(invoke)
                return
            }
            defaults.removeObject(forKey: transactionKey)
            guard defaults.synchronize() else {
                rejectStorageFailure(invoke)
                return
            }
            WidgetCenter.shared.reloadAllTimelines()
            invoke.resolve(["kind": "ok"])
        } catch {
            rejectStorageFailure(invoke)
        }
    }

    private func widgetSelectionChanged(_ left: WidgetDeckConfiguration, _ right: WidgetDeckConfiguration) -> Bool {
        left.query != right.query || left.repositories.map { "\($0.owner.lowercased())/\($0.name.lowercased())" } != right.repositories.map { "\($0.owner.lowercased())/\($0.name.lowercased())" } || left.profileId != right.profileId || left.profileKind != right.profileKind || left.scopeId != right.scopeId
    }

    private func replaceWidgetSnapshot(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let snapshot = args.snapshot, let defaults = UserDefaults(suiteName: appGroup),
              let configurationData = defaults.data(forKey: widgetConfigurationPrefix + snapshot.deckId),
              let configuration = try? JSONDecoder().decode(WidgetDeckConfiguration.self, from: configurationData),
              configuration.query == snapshot.query else { throw NativeError.invalidArgument }
        defaults.set(try JSONEncoder().encode(snapshot), forKey: widgetSnapshotPrefix + snapshot.deckId)
        WidgetCenter.shared.reloadAllTimelines()
        invoke.resolve(["kind": "ok"])
    }

    private func disableWidgetDeck(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let deckId = args.deckId, let defaults = UserDefaults(suiteName: appGroup) else { throw NativeError.invalidArgument }
        guard removeWidgetCredential(deckId) else { rejectStorageFailure(invoke); return }
        defaults.removeObject(forKey: widgetConfigurationPrefix + deckId)
        defaults.removeObject(forKey: widgetSnapshotPrefix + deckId)
        WidgetCenter.shared.reloadAllTimelines()
        invoke.resolve(["kind": "ok"])
    }

    private func clearWidgetState() -> Bool {
        guard let defaults = UserDefaults(suiteName: appGroup) else { return false }
        let keys = defaults.dictionaryRepresentation().keys
        for key in keys where key.hasPrefix(widgetConfigurationPrefix) {
            defaults.removeObject(forKey: key)
        }
        for key in keys where key.hasPrefix(widgetSnapshotPrefix) {
            defaults.removeObject(forKey: key)
        }
        for key in keys where key.hasPrefix(widgetTransactionPrefix) {
            defaults.removeObject(forKey: key)
        }
        guard removeAllWidgetCredentials() else { return false }
        WidgetCenter.shared.reloadAllTimelines()
        return true
    }

    private func permissionName(_ status: UNAuthorizationStatus) -> String {
        switch status {
        case .authorized, .provisional, .ephemeral: return "authorized"
        case .denied: return "denied"
        default: return "not-determined"
        }
    }

    private func notificationPermission(_ invoke: Invoke) {
        UNUserNotificationCenter.current().getNotificationSettings { settings in
            invoke.resolve(["kind": "notification-permission", "permission": self.permissionName(settings.authorizationStatus)])
        }
    }

    private func requestNotificationPermission(_ invoke: Invoke) {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound]) { _, error in
            if error != nil { invoke.reject("platform-failure", code: "platform-failure") }
            else { self.notificationPermission(invoke) }
        }
    }

    private func publishNotification(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let notification = args.notification else { throw NativeError.invalidArgument }
        let center = UNUserNotificationCenter.current()
        center.getNotificationSettings { settings in
            guard self.permissionName(settings.authorizationStatus) == "authorized" else {
                invoke.reject("permission-denied", code: "permission-denied")
                return
            }
            let content = UNMutableNotificationContent()
            content.title = notification.title
            content.body = notification.body
            content.sound = .default
            content.userInfo = ["deckId": notification.deckId]
            let request = UNNotificationRequest(identifier: notification.id, content: content, trigger: nil)
            center.add(request) { error in
                if error != nil { invoke.reject("platform-failure", code: "platform-failure") }
                else { invoke.resolve(["kind": "ok"]) }
            }
        }
    }

    func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        let deckNotification = notification.request.content.userInfo["deckId"] is String
        completionHandler(deckNotification ? [.banner, .sound] : [])
    }

    private func cancelNotifications(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let deckId = args.deckId else { throw NativeError.invalidArgument }
        let center = UNUserNotificationCenter.current()
        center.getPendingNotificationRequests { requests in
            let pendingIdentifiers = requests.filter { ($0.content.userInfo["deckId"] as? String) == deckId }.map(\.identifier)
            center.removePendingNotificationRequests(withIdentifiers: pendingIdentifiers)
            center.getDeliveredNotifications { notifications in
                let deliveredIdentifiers = notifications.filter { ($0.request.content.userInfo["deckId"] as? String) == deckId }.map { $0.request.identifier }
                center.removeDeliveredNotifications(withIdentifiers: deliveredIdentifiers)
                invoke.resolve(["kind": "ok"])
            }
        }
    }
}

private func diagnosticsTemporaryDirectory() -> URL {
    FileManager.default.temporaryDirectory.appendingPathComponent("devhud-diagnostics-v1", isDirectory: true)
}

private enum NativeError: Error { case invalidArgument }

@_cdecl("init_plugin_devhud_native")
func initPlugin() -> Plugin { DevhudNativePlugin() }
