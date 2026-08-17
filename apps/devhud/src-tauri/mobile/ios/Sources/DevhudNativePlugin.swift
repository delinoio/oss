import Foundation
import Security
import Tauri
import UIKit
import UserNotifications
import WebKit

private let keychainService = "io.delino.devhud.secure-settings.v1"
private let appGroup = "group.io.delino.devhud"

private struct SecureSetting: Decodable {
    let kind: String
    let profileId: String
    var account: String { "\(kind):\(profileId)" }
}

private struct DeckNotification: Decodable {
    let id: String
    let deckId: String
    let title: String
    let body: String
}

private struct RequestArgs: Decodable {
    let operation: String
    let target: String?
    let apiOrigin: String?
    let url: String?
    let issuer: String?
    let setting: SecureSetting?
    let value: String?
    let scope: String?
    let profileId: String?
    let notification: DeckNotification?
    let deckId: String?
}

final class DevhudNativePlugin: Plugin, UNUserNotificationCenterDelegate {
    @objc public override func load(webview: WKWebView) {
        guard UserDefaults(suiteName: appGroup) != nil else { return }
        UNUserNotificationCenter.current().delegate = self
    }

    @objc func request(_ invoke: Invoke) throws {
        let args = try invoke.parseArgs(RequestArgs.self)
        switch args.operation {
        case "lifecycle.open-external": try openExternal(args, invoke)
        case "auth.open-system-browser": try openAuthenticationBrowser(args, invoke)
        case "secure.read": try readSecure(args, invoke)
        case "secure.write": try writeSecure(args, invoke)
        case "secure.remove": try removeSecure(args, invoke)
        case "secure.purge": try purgeSecure(args, invoke)
        case "notifications.permission": notificationPermission(invoke)
        case "notifications.request-permission": requestNotificationPermission(invoke)
        case "notifications.publish-deck-change": try publishNotification(args, invoke)
        case "notifications.cancel-deck": try cancelNotifications(args, invoke)
        case "updates.status":
            let version = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0"
            invoke.resolve(["kind": "update-status", "store": "app-store", "installedVersion": version, "configured": false])
        case "updates.open-store": invoke.reject("not-configured", code: "not-configured")
        default: invoke.reject("invalid-argument", code: "invalid-argument")
        }
    }

    private func openAuthenticationBrowser(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let value = args.url, let issuerValue = args.issuer,
              let destination = URL(string: value), let issuer = URL(string: issuerValue),
              issuer.scheme == "https", issuer.path == "", issuer.query == nil, issuer.fragment == nil,
              destination.scheme == "https", destination.host == issuer.host,
              destination.port == issuer.port, destination.user == nil, destination.password == nil,
              destination.fragment == nil else { throw NativeError.invalidArgument }
        UIApplication.shared.open(destination, options: [:]) { opened in
            if opened { invoke.resolve(["kind": "ok"]) }
            else { invoke.reject("platform-failure", code: "platform-failure") }
        }
    }

    private func openExternal(_ args: RequestArgs, _ invoke: Invoke) throws {
        let destination: URL
        if args.target == "pat" {
            destination = URL(string: "https://github.com/settings/personal-access-tokens/new")!
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

    private func query(_ setting: SecureSetting) -> [String: Any] {
        var item: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: keychainService,
         kSecAttrAccount as String: setting.account,
         kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
         kSecAttrSynchronizable as String: false]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: "DevHudKeychainAccessGroup") as? String {
            item[kSecAttrAccessGroup as String] = accessGroup
        }
        return item
    }

    private func rejectStorageFailure(_ invoke: Invoke) {
        invoke.reject("storage-failure", code: "storage-failure")
    }

    private func readSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let setting = args.setting else { throw NativeError.invalidArgument }
        var itemQuery = query(setting)
        itemQuery[kSecReturnData as String] = true
        itemQuery[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(itemQuery as CFDictionary, &item)
        if status == errSecItemNotFound { invoke.resolve(["kind": "secure-value", "value": NSNull()]); return }
        guard status == errSecSuccess, let data = item as? Data, let value = String(data: data, encoding: .utf8) else {
            rejectStorageFailure(invoke)
            return
        }
        invoke.resolve(["kind": "secure-value", "value": value])
    }

    private func writeSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let setting = args.setting, let value = args.value, let data = value.data(using: .utf8) else { throw NativeError.invalidArgument }
        let itemQuery = query(setting)
        let status = SecItemUpdate(itemQuery as CFDictionary, [kSecValueData as String: data] as CFDictionary)
        if status == errSecItemNotFound {
            var item = itemQuery
            item[kSecValueData as String] = data
            guard SecItemAdd(item as CFDictionary, nil) == errSecSuccess else {
                rejectStorageFailure(invoke)
                return
            }
        } else if status != errSecSuccess {
            rejectStorageFailure(invoke)
            return
        }
        invoke.resolve(["kind": "ok"])
    }

    private func removeSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let setting = args.setting else { throw NativeError.invalidArgument }
        let status = SecItemDelete(query(setting) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            rejectStorageFailure(invoke)
            return
        }
        invoke.resolve(["kind": "ok"])
    }

    private func purgeSecure(_ args: RequestArgs, _ invoke: Invoke) throws {
        guard let scope = args.scope, ["logout", "account-deletion", "api-change"].contains(scope),
              scope == "logout" || args.profileId != nil else { throw NativeError.invalidArgument }
        var all: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
                                  kSecAttrService as String: keychainService,
                                  kSecReturnAttributes as String: true,
                                  kSecMatchLimit as String: kSecMatchLimitAll]
        if let accessGroup = Bundle.main.object(forInfoDictionaryKey: "DevHudKeychainAccessGroup") as? String {
            all[kSecAttrAccessGroup as String] = accessGroup
        }
        var result: CFTypeRef?
        let status = SecItemCopyMatching(all as CFDictionary, &result)
        if status == errSecItemNotFound { invoke.resolve(["kind": "ok"]); return }
        guard status == errSecSuccess, let items = result as? [[String: Any]] else { rejectStorageFailure(invoke); return }
        for item in items {
            guard let account = item[kSecAttrAccount as String] as? String else { continue }
            let remove = scope == "logout"
                || (scope == "account-deletion" && account != "logto-session:\(args.profileId!)")
                || (scope == "api-change" && account == "logto-session:\(args.profileId!)")
            if remove {
                var query = all
                query.removeValue(forKey: kSecReturnAttributes as String)
                query.removeValue(forKey: kSecMatchLimit as String)
                query[kSecAttrAccount as String] = account
                let deletion = SecItemDelete(query as CFDictionary)
                guard deletion == errSecSuccess || deletion == errSecItemNotFound else { rejectStorageFailure(invoke); return }
            }
        }
        invoke.resolve(["kind": "ok"])
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

private enum NativeError: Error { case invalidArgument }

@_cdecl("init_plugin_devhud_native")
func initPlugin() -> Plugin { DevhudNativePlugin() }
