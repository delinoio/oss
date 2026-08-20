import Foundation
import Intents
import Security
import SwiftUI
import WidgetKit

private let appGroup = "group.io.delino.devhud"
private let credentialService = "io.delino.devhud.widget-credential.v1"
private let configurationPrefix = "widget.configuration."
private let snapshotPrefix = "widget.snapshot."
private let staleAfter: TimeInterval = 60 * 60

struct DeckConfiguration: Codable, Identifiable {
    let version: Int; let deckId: String; let name: String; let query: String; let profileId: String; let profileKind: String; let scopeId: String; let language: String
    var id: String { deckId }
}
struct DeckCounts: Codable { let total: Int; let open: Int; let draft: Int; let merged: Int; let closed: Int; let bounded: Bool }
struct DeckPullRequest: Codable, Identifiable { let nodeId: String; let number: Int; let title: String; let repository: String; let state: String; let draft: Bool; var id: String { nodeId } }
struct DeckRate: Codable { let limit: Int?; let remaining: Int?; let used: Int?; let resetAt: String?; let resource: String?; let retryAfterSeconds: Int? }
struct DeckSnapshot: Codable {
    let version: Int; let deckId: String; let query: String; let counts: DeckCounts; let results: [DeckPullRequest]; var state: String; let lastSuccessfulAt: String?; var lastAttemptedAt: String; var rate: DeckRate?
}

private struct WidgetStore {
    let defaults = UserDefaults(suiteName: appGroup)

    func configurations() -> [DeckConfiguration] {
        guard let defaults else { return [] }
        return defaults.dictionaryRepresentation().keys.filter { $0.hasPrefix(configurationPrefix) }.compactMap { key in
            defaults.data(forKey: key).flatMap { try? JSONDecoder().decode(DeckConfiguration.self, from: $0) }
        }.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending }
    }
    func configuration(_ deckId: String?) -> DeckConfiguration? {
        guard let deckId, let data = defaults?.data(forKey: configurationPrefix + deckId) else { return nil }
        return try? JSONDecoder().decode(DeckConfiguration.self, from: data)
    }
    func snapshot(_ deckId: String) -> DeckSnapshot? {
        guard let data = defaults?.data(forKey: snapshotPrefix + deckId) else { return nil }
        return try? JSONDecoder().decode(DeckSnapshot.self, from: data)
    }
    func save(_ snapshot: DeckSnapshot) {
        guard let data = try? JSONEncoder().encode(snapshot) else { return }
        defaults?.set(data, forKey: snapshotPrefix + snapshot.deckId)
    }
    func token(_ deckId: String) -> String? {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: credentialService,
                                    kSecAttrAccount as String: deckId, kSecReturnData as String: true, kSecMatchLimit as String: kSecMatchLimitOne,
                                    kSecAttrSynchronizable as String: false]
        if let group = Bundle.main.object(forInfoDictionaryKey: "DevHudWidgetKeychainAccessGroup") as? String { query[kSecAttrAccessGroup as String] = group }
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess, let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }
}

private struct DeckEntry: TimelineEntry {
    let date: Date
    let configuration: DeckConfiguration?
    let snapshot: DeckSnapshot?
}

private struct DeckTimelineProvider: IntentTimelineProvider {
    typealias Intent = SelectDeckIntent
    typealias Entry = DeckEntry
    private let store = WidgetStore()

    func placeholder(in context: Context) -> DeckEntry { DeckEntry(date: Date(), configuration: nil, snapshot: nil) }
    func getSnapshot(for configuration: SelectDeckIntent, in context: Context, completion: @escaping (DeckEntry) -> Void) {
        let deck = store.configuration(configuration.deck?.identifier)
        completion(DeckEntry(date: Date(), configuration: deck, snapshot: deck.flatMap { store.snapshot($0.deckId) }))
    }
    func getTimeline(for configuration: SelectDeckIntent, in context: Context, completion: @escaping (Timeline<DeckEntry>) -> Void) {
        guard let deck = store.configuration(configuration.deck?.identifier) else {
            completion(Timeline(entries: [DeckEntry(date: Date(), configuration: nil, snapshot: nil)], policy: .after(Date().addingTimeInterval(30 * 60))))
            return
        }
        Task {
            let snapshot = await refresh(deck: deck, previous: store.snapshot(deck.deckId), token: store.token(deck.deckId))
            store.save(snapshot)
            completion(Timeline(entries: [DeckEntry(date: Date(), configuration: deck, snapshot: snapshot)], policy: .after(Date().addingTimeInterval(30 * 60))))
        }
    }

    private func refresh(deck: DeckConfiguration, previous: DeckSnapshot?, token: String?) async -> DeckSnapshot {
        let attempted = ISO8601DateFormatter().string(from: Date())
        guard let token else { return failure(deck: deck, previous: previous, state: "missing-token", attempted: attempted, rate: nil) }
        var components = URLComponents(string: "https://api.github.com/search/issues")!
        components.queryItems = [URLQueryItem(name: "q", value: deck.query), URLQueryItem(name: "per_page", value: "100"), URLQueryItem(name: "page", value: "1")]
        var request = URLRequest(url: components.url!, timeoutInterval: 20)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("2026-03-10", forHTTPHeaderField: "X-GitHub-Api-Version")
        do {
            let (data, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse else { return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: nil) }
            let rate = responseRate(http)
            if http.statusCode == 401 || (http.statusCode == 403 && rate.remaining != 0) { return failure(deck: deck, previous: previous, state: "missing-token", attempted: attempted, rate: rate) }
            if http.statusCode == 429 || (http.statusCode == 403 && rate.remaining == 0) { return failure(deck: deck, previous: previous, state: "rate-limit", attempted: attempted, rate: rate) }
            guard (200...299).contains(http.statusCode), let root = try JSONSerialization.jsonObject(with: data) as? [String: Any], let items = root["items"] as? [[String: Any]], let total = root["total_count"] as? Int else {
                return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: rate)
            }
            var open = 0, draft = 0, merged = 0, closed = 0
            let results: [DeckPullRequest] = items.prefix(100).compactMap { item in
                guard let nodeId = item["node_id"] as? String, let number = item["number"] as? Int, let title = item["title"] as? String, let repositoryURL = item["repository_url"] as? String else { return nil }
                let isDraft = item["draft"] as? Bool ?? false
                let pull = item["pull_request"] as? [String: Any]
                let isMerged = pull?["merged_at"] is String
                let state = isMerged ? "merged" : item["state"] as? String ?? "open"
                if isDraft { draft += 1 } else if isMerged { merged += 1 } else if state == "closed" { closed += 1 } else { open += 1 }
                return DeckPullRequest(nodeId: nodeId, number: number, title: title, repository: repositoryURL.components(separatedBy: "/repos/").last ?? repositoryURL, state: state, draft: isDraft)
            }
            return DeckSnapshot(version: 1, deckId: deck.deckId, query: deck.query, counts: DeckCounts(total: total, open: open, draft: draft, merged: merged, closed: closed, bounded: total > 100), results: results, state: "fresh", lastSuccessfulAt: attempted, lastAttemptedAt: attempted, rate: rate)
        } catch { return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: nil) }
    }

    private func failure(deck: DeckConfiguration, previous: DeckSnapshot?, state: String, attempted: String, rate: DeckRate?) -> DeckSnapshot {
        var retained = previous ?? DeckSnapshot(version: 1, deckId: deck.deckId, query: deck.query, counts: DeckCounts(total: 0, open: 0, draft: 0, merged: 0, closed: 0, bounded: false), results: [], state: state, lastSuccessfulAt: nil, lastAttemptedAt: attempted, rate: rate)
        retained.state = state; retained.lastAttemptedAt = attempted; retained.rate = rate ?? retained.rate
        return retained
    }
    private func responseRate(_ response: HTTPURLResponse) -> DeckRate {
        func int(_ key: String) -> Int? { response.value(forHTTPHeaderField: key).flatMap(Int.init) }
        let reset = int("X-RateLimit-Reset").map { ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: TimeInterval($0))) }
        return DeckRate(limit: int("X-RateLimit-Limit"), remaining: int("X-RateLimit-Remaining"), used: int("X-RateLimit-Used"), resetAt: reset, resource: response.value(forHTTPHeaderField: "X-RateLimit-Resource"), retryAfterSeconds: int("Retry-After"))
    }
}

private struct DeckWidgetView: View {
    let entry: DeckEntry
    private var korean: Bool { entry.configuration?.language == "ko" }
    private var state: String {
        guard let snapshot = entry.snapshot else { return "missing-token" }
        if snapshot.state == "fresh", let value = snapshot.lastSuccessfulAt, let date = ISO8601DateFormatter().date(from: value), Date().timeIntervalSince(date) >= staleAfter { return "stale" }
        return snapshot.state
    }
    private var stale: Bool { guard let value = entry.snapshot?.lastSuccessfulAt, let date = ISO8601DateFormatter().date(from: value) else { return false }; return Date().timeIntervalSince(date) >= staleAfter }
    private func text(_ en: String, _ ko: String) -> String { korean ? ko : en }
    private var status: String {
        let primary: String = switch state { case "stale": text("Stale", "오래됨"); case "missing-token": text("Setup required", "설정 필요"); case "rate-limit": text("Rate limited", "요청 제한됨"); case "error": text("Refresh failed", "새로 고침 실패"); default: text("Current", "최신") }
        return stale && state != "stale" ? "\(primary) · \(text("Stale", "오래됨"))" : primary
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(entry.configuration?.name ?? text("Open DevHUD to set up", "DevHUD에서 설정하세요")).font(.headline).lineLimit(1)
            if let counts = entry.snapshot?.counts { Text(text("Total \(counts.total) · Open \(counts.open) · Draft \(counts.draft) · Merged \(counts.merged) · Closed \(counts.closed)\(counts.bounded ? " · state counts: first 100" : "")", "전체 \(counts.total) · 열림 \(counts.open) · 초안 \(counts.draft) · 병합 \(counts.merged) · 닫힘 \(counts.closed)\(counts.bounded ? " · 상태 개수: 처음 100개" : "")")).font(.caption).lineLimit(2) }
            ForEach(Array((entry.snapshot?.results ?? []).prefix(3))) { item in Text("\(item.repository)#\(item.number) · \(item.title)").font(.caption).lineLimit(1) }
            Spacer(minLength: 0)
            Text("\(status) · \(text("Last success", "마지막 성공")) \(entry.snapshot?.lastSuccessfulAt ?? text("Never", "없음"))").font(.caption2).foregroundStyle(.secondary).lineLimit(1)
        }
        .padding().background(Color(red: 0.11, green: 0.15, blue: 0.19))
        .widgetURL(entry.configuration.flatMap { URL(string: "devhud://deck/\($0.deckId)") })
        .accessibilityElement(children: .combine)
    }
}

@main
struct DevHudWidgetBundle: WidgetBundle {
    var body: some Widget { DevHudDeckWidget() }
}

private struct DevHudDeckWidget: Widget {
    let kind = "io.delino.devhud.widget.deck"
    var body: some WidgetConfiguration {
        IntentConfiguration(kind: kind, intent: SelectDeckIntent.self, provider: DeckTimelineProvider()) { entry in DeckWidgetView(entry: entry) }
            .configurationDisplayName("DevHUD Deck")
            .description("One explicitly selected Deck. Private titles may be visible.")
            .supportedFamilies([.systemMedium, .systemLarge])
    }
}
