// SPDX-License-Identifier: Apache-2.0
import Foundation

let widgetGroup = "group.io.delino.delidev"
let widgetKind = "io.delino.delidev.widget.status"
let widgetStaleAfter: TimeInterval = 45

enum SnapshotFailure: Error { case invalid, storage }
enum SnapshotState: String, Codable { case observed, stale, unavailable }
enum QuotaState: String, Codable { case observed, unknown, stale, failed, unsupported }

func widgetTimestamp(_ value: String) -> Date? {
    guard value.utf8.count <= 40 else { return nil }
    let parser = ISO8601DateFormatter()
    parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return parser.date(from: value) ?? ISO8601DateFormatter().date(from: value)
}

func safeAlias(_ value: String) -> String {
    let lower = value.lowercased()
    if value.contains("@") || ["bearer ", "sk-", "ghp_", "github_pat_", "token=", "password", "api_key"].contains(where: lower.contains) {
        return "Alias hidden"
    }
    return String(value.unicodeScalars.map { CharacterSet.controlCharacters.contains($0) ? " " : String($0) }.joined().prefix(128))
}

func canonicalServer(_ value: String) -> Bool {
    guard let id = UUID(uuidString: value), id.uuidString.lowercased() == value else { return false }
    let bytes = Array(value.utf8)
    return bytes[14] == 55 && [56, 57, 97, 98].contains(bytes[19])
}

func exactDecimal(_ value: String, fractional: Bool = false) -> Bool {
    let pattern = fractional ? "^(0|[1-9][0-9]{0,79})(\\.[0-9]{1,9})?$" : "^(0|[1-9][0-9]{0,79})$"
    return value.utf8.count <= 90 && value.range(of: pattern, options: .regularExpression) != nil
}

struct Overview: Codable {
    let observed_at: String
    let stale: Bool
    let active_sessions: String
    let pending_interactions: String
    let registered_workers: String
    let connected_workers: String
    func validate() throws {
        guard widgetTimestamp(observed_at) != nil,
              [active_sessions, pending_interactions, registered_workers, connected_workers].allSatisfy({ exactDecimal($0) && UInt64($0) != nil }),
              UInt64(connected_workers)! <= UInt64(registered_workers)! else { throw SnapshotFailure.invalid }
    }
}
struct Estimate: Codable {
    let currency: String
    let known_amount: String?
}
struct Usage: Codable {
    let known_tokens: String?
    let incomplete: Bool
    let estimates: [Estimate]
    func validate() throws {
        guard incomplete, known_tokens.map({ exactDecimal($0) }) ?? true,
              estimates.count <= 32, Set(estimates.map(\.currency)).count == estimates.count,
              estimates.allSatisfy({ $0.currency.range(of: "^[A-Z]{3}$", options: .regularExpression) != nil && ($0.known_amount.map({ exactDecimal($0, fractional: true) }) ?? true) }) else { throw SnapshotFailure.invalid }
    }
}
struct Quota: Codable {
    var state: QuotaState
    let remaining_basis_points: UInt16?
    let observed_at: String?
    let reset_at: String?
    func effectiveState(at now: Date, snapshotStale: Bool) -> QuotaState {
        guard state == .observed else { return state }
        guard remaining_basis_points != nil, let observation = observed_at.flatMap(widgetTimestamp) else { return .unknown }
        if snapshotStale || observation > now || now.timeIntervalSince(observation) > 300 || reset_at.flatMap(widgetTimestamp).map({ $0 <= now }) == true { return .stale }
        return .observed
    }
    func label(at now: Date, snapshotStale: Bool) -> String {
        let effective = effectiveState(at: now, snapshotStale: snapshotStale)
        guard let remaining = remaining_basis_points, effective == .observed || effective == .stale else { return "Quota \(effective.rawValue)" }
        return String(format: "%d.%02d%% remaining · %@", remaining / 100, remaining % 100, effective.rawValue)
    }
}
struct Account: Codable { var alias: String; let windows: [Quota]; let more: Bool }
struct Accounts: Codable { var entries: [Account]; let more: Bool }
struct Summary: Codable {
    let overview: Overview?
    let usage: Usage?
    var accounts: Accounts?
    mutating func validateAndMask() throws {
        try overview?.validate()
        try usage?.validate()
        if let accounts {
            guard accounts.entries.count <= 20 else { throw SnapshotFailure.invalid }
            for account in accounts.entries {
                guard !account.alias.isEmpty, account.alias.utf8.count <= 256, account.windows.count <= 8 else { throw SnapshotFailure.invalid }
                for quota in account.windows {
                    guard quota.remaining_basis_points.map({ $0 <= 10000 }) ?? true,
                          [quota.observed_at, quota.reset_at].allSatisfy({ $0.map({ widgetTimestamp($0) != nil }) ?? true }) else { throw SnapshotFailure.invalid }
                }
            }
            self.accounts?.entries = accounts.entries.map { var value = $0; value.alias = safeAlias(value.alias); return value }
        }
    }
}
struct ServerSnapshot: Codable, Identifiable {
    let id: String
    var name: String
    var state: SnapshotState
    var last_successful_at: String?
    var last_attempted_at: String
    var summary: Summary?
    func isStale(at now: Date) -> Bool {
        guard state == .observed, let success = last_successful_at.flatMap(widgetTimestamp) else { return true }
        return success > now || now.timeIntervalSince(success) >= widgetStaleAfter
    }
    mutating func validate() throws {
        guard canonicalServer(id), !name.isEmpty, name.utf8.count <= 256,
              widgetTimestamp(last_attempted_at) != nil,
              last_successful_at.map({ widgetTimestamp($0) != nil }) ?? true else { throw SnapshotFailure.invalid }
        name = safeAlias(name)
        try summary?.validateAndMask()
        if let success = last_successful_at {
            guard let overview = summary?.overview, !overview.stale,
                  success == overview.observed_at, state != .unavailable else { throw SnapshotFailure.invalid }
        } else if summary != nil || state == .observed { throw SnapshotFailure.invalid }
    }
}
struct SnapshotFile: Codable {
    let version: Int
    var servers: [ServerSnapshot]
    mutating func validate() throws {
        guard version == 1, servers.count <= 32, Set(servers.map(\.id)).count == servers.count else { throw SnapshotFailure.invalid }
        for index in servers.indices { try servers[index].validate() }
    }
}
enum PublicationAction: String, Codable { case publish, remove, stop }
struct Publication: Decodable {
    let action: PublicationAction
    let id: String?
    let name: String?
    var summary: Summary?
}

// Formatting never converts token/currency strings to a floating-point value,
// pools currencies or treats an absent observation as measured zero.
func tokenLabel(_ usage: Usage?) -> String {
    guard let value = usage?.known_tokens else { return "Today's tokens unavailable" }
    return "\(value) known tokens · incomplete"
}
func estimateLabels(_ usage: Usage?) -> [String] {
    (usage?.estimates ?? []).map { "\($0.currency) \($0.known_amount ?? "unavailable") · estimate" }
}
