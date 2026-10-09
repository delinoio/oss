// SPDX-License-Identifier: Apache-2.0
import Foundation
import Security
import Tauri
import UIKit
import UserNotifications
import WebKit

private let service = "io.delino.delidev.mobile.state.v1"
private struct Request: Decodable { let operation: String; let value: String?; let profile_id: String?; let inbox_id: String?; let language: String?; let origin: String? }
final class DeliDevMobilePlugin: Plugin, UNUserNotificationCenterDelegate, URLSessionTaskDelegate {
    private let queue = DispatchQueue(label: "io.delino.delidev.mobile.protected-state")
    @objc public override func load(webview: WKWebView) { UNUserNotificationCenter.current().delegate = self }
    private func item() -> [String: Any] { [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service, kSecAttrAccount as String: "state", kSecAttrSynchronizable as String: false] }
    private func read() throws -> String? {
        var query = item(); query[kSecReturnData as String] = true; query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?; let status = SecItemCopyMatching(query as CFDictionary, &result)
        if status == errSecItemNotFound { return nil }
        guard status == errSecSuccess, let data = result as? Data, data.count <= 4*1024*1024, let value = String(data: data, encoding: .utf8) else { throw Failure.storage }
        return value
    }
    private func write(_ value: String) throws {
        let bytes = Data(value.utf8); guard bytes.count <= 4*1024*1024 else { throw Failure.storage }
        let update: [String: Any] = [kSecValueData as String: bytes, kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        let status = SecItemUpdate(item() as CFDictionary, update as CFDictionary)
        if status == errSecItemNotFound { var query = item(); update.forEach { query[$0.key] = $0.value }; guard SecItemAdd(query as CFDictionary, nil) == errSecSuccess else { throw Failure.storage } }
        else if status != errSecSuccess { throw Failure.storage }
    }
    @objc func request(_ invoke: Invoke) throws {
        let args = try invoke.parseArgs(Request.self)
        switch args.operation {
        case "read-state": queue.async { do { invoke.resolve(["value": try self.read() as Any? ?? NSNull()]) } catch { invoke.reject("Protected state is unavailable", code: "storage-failure") } }
        case "write-state": guard let value = args.value else { invoke.reject("Invalid state", code: "invalid-argument"); return }; queue.async { do { try self.write(value); invoke.resolve(["ok": true]) } catch { invoke.reject("Protected state is unavailable", code: "storage-failure") } }
        case "tls":
            guard let origin = args.origin, let url = URL(string: origin), url.scheme == "https", url.user == nil, url.password == nil, url.query == nil, url.fragment == nil, url.path.isEmpty else { invoke.reject("Invalid HTTPS origin", code: "invalid-argument"); return }
            let config = URLSessionConfiguration.ephemeral; config.timeoutIntervalForRequest = 5; config.timeoutIntervalForResource = 5
            let session = URLSession(configuration: config, delegate: self, delegateQueue: nil)
            var request = URLRequest(url: url); request.httpMethod = "HEAD"
            session.dataTask(with: request) { _, _, error in
                let code = (error as? URLError)?.code
                let certificate: Set<URLError.Code> = [.serverCertificateHasBadDate, .serverCertificateUntrusted, .serverCertificateHasUnknownRoot, .serverCertificateNotYetValid, .secureConnectionFailed]
                invoke.resolve(["status": error == nil ? "ok" : code.map { certificate.contains($0) } == true ? "certificate" : "network"])
                session.finishTasksAndInvalidate()
            }.resume()
        case "permission": UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { granted, _ in invoke.resolve(["granted": granted]) }
        case "notify":
            queue.async {
                do {
                    guard let raw = try self.read(), let data = raw.data(using: .utf8), let state = try JSONSerialization.jsonObject(with: data) as? [String: Any], state["selectedProfile"] as? String == args.profile_id else { throw Failure.storage }
                    DispatchQueue.main.async {
                        guard UIApplication.shared.applicationState == .active, let id = args.inbox_id else { invoke.resolve(["submitted": false]); return }
                        let content = UNMutableNotificationContent(); content.title = "DeliDev"; content.body = args.language == "ko" ? "세션에 확인이 필요합니다." : "A session needs attention."
                        UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id, content: content, trigger: nil)) { error in invoke.resolve(["submitted": error == nil]) }
                    }
                } catch { invoke.reject("Notification authority is unavailable", code: "storage-failure") }
            }
        default: invoke.reject("Unsupported operation", code: "unsupported")
        }
    }
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification, withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) { completionHandler([.banner, .sound]) }
}
private enum Failure: Error { case storage }
@_cdecl("init_plugin_delidev_mobile_native")
func initPlugin() -> Plugin { DeliDevMobilePlugin() }
