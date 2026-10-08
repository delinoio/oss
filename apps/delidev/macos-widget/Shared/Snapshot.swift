// SPDX-License-Identifier: Apache-2.0
import Foundation

let widgetGroup = "group.io.delino.delidev"
let widgetKind = "io.delino.delidev.widget.status"
let widgetStaleAfter: TimeInterval = 45

enum SnapshotFailure: Error { case invalid, storage }
enum SnapshotState: String, Codable { case observed, stale, unavailable }
enum QuotaState: String, Codable { case observed, unknown, stale, failed, unsupported }

enum LanguagePreference: String, Codable { case system, english = "en", korean = "ko" }
enum WidgetLanguage: String { case english = "en", korean = "ko" }
struct WidgetLanguageDocument: Codable {
    let version: UInt32
    let language: LanguagePreference
    func validate() throws { guard version == 1 else { throw SnapshotFailure.invalid } }
}
func resolveWidgetLanguage(_ preference: LanguagePreference, languages: [String] = Locale.preferredLanguages) -> WidgetLanguage {
    switch preference { case .english: return .english; case .korean: return .korean; case .system: break }
    for locale in languages.prefix(64) where locale.utf8.count <= 128 {
        let base = locale.trimmingCharacters(in: .whitespacesAndNewlines).lowercased().split(whereSeparator: { $0 == "-" || $0 == "_" }).first
        if base == "en" { return .english }
        if base == "ko" { return .korean }
    }
    return .english
}

func widgetTimestamp(_ value: String) -> Date? {
    guard value.utf8.count <= 40 else { return nil }
    let parser = ISO8601DateFormatter()
    parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return parser.date(from: value) ?? ISO8601DateFormatter().date(from: value)
}

func aliasIsHidden(_ value: String) -> Bool {
    let lower = value.lowercased()
    return value.contains("@") || ["bearer ", "sk-", "ghp_", "github_pat_", "token=", "password", "api_key"].contains(where: lower.contains)
}
func safeAlias(_ value: String) -> String {
    if aliasIsHidden(value) {
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
    func label(at now: Date, snapshotStale: Bool, language: WidgetLanguage = .english) -> String {
        let effective = effectiveState(at: now, snapshotStale: snapshotStale)
        let state = widgetCopy(quotaMessage(effective), language)
        guard let remaining = remaining_basis_points, effective == .observed || effective == .stale else { return widgetCopy(.widgetQuota, language, ["state": state]) }
        return widgetCopy(.remaining, language, ["amount": String(format: "%d.%02d", remaining / 100, remaining % 100), "state": state])
    }
}
struct Account: Codable { var alias: String; var alias_hidden: Bool? = nil; let windows: [Quota]; let more: Bool }
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
            self.accounts?.entries = accounts.entries.map { var value = $0; if aliasIsHidden(value.alias) { value.alias_hidden = true }; value.alias = safeAlias(value.alias); return value }
        }
    }
}
struct ServerSnapshot: Codable, Identifiable {
    let id: String
    var name: String
    var name_hidden: Bool? = nil
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
        if aliasIsHidden(name) { name_hidden = true }; name = safeAlias(name)
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
func tokenLabel(_ usage: Usage?, language: WidgetLanguage = .english) -> String {
    guard let value = usage?.known_tokens else { return widgetCopy(.widgetTokensUnavailable, language) }
    return widgetCopy(.widgetTokens, language, ["tokens": value])
}
func estimateLabels(_ usage: Usage?, language: WidgetLanguage = .english) -> [String] {
    (usage?.estimates ?? []).map { estimate in
        if let amount = estimate.known_amount { return widgetCopy(.widgetEstimate, language, ["currency": estimate.currency, "amount": amount]) }
        return widgetCopy(.widgetEstimateUnavailable, language, ["currency": estimate.currency])
    }
}

func quotaMessage(_ state: QuotaState) -> WidgetMessage {
    switch state {
    case .observed: return .quotaObserved
    case .unknown: return .quotaUnknown
    case .stale: return .quotaStale
    case .failed: return .quotaFailed
    case .unsupported: return .quotaUnsupported
    }
}
func widgetNumber(_ value: String) -> String {
    let parts = value.split(separator: ".", omittingEmptySubsequences: false)
    let digits = Array(parts[0])
    let integer = digits.enumerated().map { index, digit in (index > 0 && (digits.count - index) % 3 == 0 ? "," : "") + String(digit) }.joined()
    return integer + (parts.count > 1 ? "." + parts[1] : "")
}
func widgetDate(_ value: Date, _ language: WidgetLanguage) -> String {
    let formatter = DateFormatter()
    formatter.locale = Locale(identifier: language == .korean ? "ko_KR" : "en_US")
    formatter.dateStyle = .medium
    formatter.timeStyle = .short
    return formatter.string(from: value)
}

// Validate reset wall components before countdown formatting. Foundation's
// ISO8601 parser can normalize impossible dates; they grant no duration proof.
func widgetResetInstant(_ value: String) -> Date? {
    guard value.utf8.count <= 35,
          let expression = try? NSRegularExpression(pattern: #"^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$"#),
          let match = expression.firstMatch(in: value, range: NSRange(value.startIndex..., in: value)) else { return nil }
    func part(_ index: Int) -> String { Range(match.range(at: index), in: value).map { String(value[$0]) } ?? "" }
    guard let year = Int(part(1)), let month = Int(part(2)), let day = Int(part(3)),
          let hour = Int(part(4)), let minute = Int(part(5)), let second = Int(part(6)),
          (1...12).contains(month), (0...23).contains(hour), (0...59).contains(minute), (0...59).contains(second) else { return nil }
    let leap = year % 4 == 0 && (year % 100 != 0 || year % 400 == 0)
    let monthDays = [31, leap ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
    guard (1...monthDays[month-1]).contains(day) else { return nil }
    let zone = part(8)
    var offset = 0
    if zone != "Z" {
        let digits = Array(zone)
        guard let hours = Int(String(digits[1...2])), let minutes = Int(String(digits[4...5])), hours <= 23, minutes <= 59 else { return nil }
        offset = (hours * 60 + minutes) * 60 * (digits[0] == "+" ? 1 : -1)
    }
    // Match proleptic Gregorian elapsed-day arithmetic in the other renderers.
    let adjustedYear = year - (month <= 2 ? 1 : 0)
    let era = Int(floor(Double(adjustedYear) / 400))
    let yearOfEra = adjustedYear - era * 400
    let shiftedMonth = month + (month > 2 ? -3 : 9)
    let dayOfYear = (153 * shiftedMonth + 2) / 5 + day - 1
    let days = era * 146097 + yearOfEra * 365 + yearOfEra / 4 - yearOfEra / 100 + dayOfYear - 719468
    let fraction = Double("0" + part(7)) ?? 0
    return Date(timeIntervalSince1970: Double(days * 86400 + hour * 3600 + minute * 60 + second - offset) + fraction)
}
func widgetQuotaReset(_ value: String, at now: Date, language: WidgetLanguage) -> String {
    guard let reset = widgetResetInstant(value) else {
        let at = widgetTimestamp(value).map { widgetDate($0, language) } ?? value
        return widgetCopy(.resetAt, language, ["at": at])
    }
    let remaining = reset.timeIntervalSince(now)
    guard remaining > 0 else { return widgetCopy(.resetAt, language, ["at": widgetDate(reset, language)]) }
    let days = Int(floor(remaining / 86400)), hours = Int(floor(remaining / 3600)) % 24, minutes = Int(floor(remaining / 60)) % 60
    let key: WidgetMessage
    if days > 0 {
        if hours == 0 { key = days == 1 ? .resetDay : .resetDays }
        else if days == 1 { key = hours == 1 ? .resetDayHour : .resetDayHours }
        else { key = hours == 1 ? .resetDaysHour : .resetDaysHours }
    } else if hours > 0 {
        if minutes == 0 { key = hours == 1 ? .resetHour : .resetHours }
        else if hours == 1 { key = minutes == 1 ? .resetHourMinute : .resetHourMinutes }
        else { key = minutes == 1 ? .resetHoursMinute : .resetHoursMinutes }
    } else { key = minutes == 0 ? .resetSoon : minutes == 1 ? .resetMinute : .resetMinutes }
    return widgetCopy(key, language, ["days": String(days), "hours": String(hours), "minutes": String(minutes)])
}
