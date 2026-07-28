import Foundation
import Security
import Tauri
import UIKit

private final class SessionArgs: Decodable { let record: String }
private final class AuthorizationArgs: Decodable { let url: String }
private struct OperationResponse: Encodable { let completed: Bool }
private struct SessionResponse: Encodable { let record: String? }
private struct CallbackResponse: Encodable { let url: String? }

final class DevHudAuthPlugin: Plugin {
    private let service = "dev.deli.devhud.auth"
    private let account = "active-session"

    @objc func readSession(_ invoke: Invoke) {
        guarded(invoke) {
            invoke.resolve(SessionResponse(record: try self.read()))
        }
    }

    @objc func writeSession(_ invoke: Invoke) {
        guarded(invoke) {
            let value = try invoke.parseArgs(SessionArgs.self).record
            guard !value.isEmpty, value.utf8.count <= 32_768 else { throw VaultError.invalid }
            try self.write(value)
            invoke.resolve(OperationResponse(completed: true))
        }
    }

    @objc func clearSession(_ invoke: Invoke) {
        guarded(invoke) {
            try self.clear()
            invoke.resolve(OperationResponse(completed: true))
        }
    }

    @objc func openAuthorization(_ invoke: Invoke) {
        guarded(invoke) {
            let value = try invoke.parseArgs(AuthorizationArgs.self).url
            guard let target = URL(string: value),
                  target.scheme == "https",
                  target.host != nil,
                  target.path == "/oidc/auth",
                  target.user == nil,
                  target.password == nil,
                  target.fragment == nil else { throw VaultError.invalid }
            DispatchQueue.main.async {
                UIApplication.shared.open(target, options: [:]) { opened in
                    if opened {
                        invoke.resolve(OperationResponse(completed: true))
                    } else {
                        invoke.reject("The system browser is unavailable.", code: "browser-unavailable")
                    }
                }
            }
        }
    }

    @objc func takeCallback(_ invoke: Invoke) {
        // The verified universal-link delivery is consumed by the Tauri host
        // lifecycle. No custom scheme or arbitrary URL is accepted here.
        invoke.resolve(CallbackResponse(url: nil))
    }

    private func read() throws -> String? {
        var query = baseQuery()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess,
              let data = item as? Data,
              let value = String(data: data, encoding: .utf8) else { throw VaultError.storage }
        return value
    }

    private func write(_ value: String) throws {
        try clear()
        var query = baseQuery()
        query[kSecValueData as String] = Data(value.utf8)
        query[kSecAttrAccessible as String] = kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        guard SecItemAdd(query as CFDictionary, nil) == errSecSuccess else {
            throw VaultError.storage
        }
    }

    private func clear() throws {
        let status = SecItemDelete(baseQuery() as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw VaultError.storage
        }
    }

    private func baseQuery() -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecAttrSynchronizable as String: false,
        ]
    }

    private func guarded(_ invoke: Invoke, operation: () throws -> Void) {
        do { try operation() }
        catch {
            invoke.reject("The DevHud secure authentication operation failed.", code: "secure-vault-unavailable")
        }
    }
}

private enum VaultError: Error { case invalid, storage }

@_cdecl("init_plugin_devhud_auth")
func initPlugin() -> Plugin { DevHudAuthPlugin() }
