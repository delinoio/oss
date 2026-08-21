import Foundation
import Intents
import Security
import SwiftUI
import WidgetKit

private let appGroup = "group.io.delino.devhud"
private let credentialService = "io.delino.devhud.widget-credential.v1"
private let configurationPrefix = "widget.configuration."
private let snapshotPrefix = "widget.snapshot."
private let transactionPrefix = "widget.transaction."
private let credentialReplacementKey = "widget.credential-replacement.v1"
private let foregroundReloadDeadlinePrefix = "widget.foreground-reload-deadline.v1."
private let staleAfter: TimeInterval = 60 * 60
private let repositoryValidationConcurrency = 3
private let refreshDeadlineNanoseconds: UInt64 = 20 * 1_000_000_000
private let githubSession: URLSession = {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
    configuration.urlCache = nil
    return URLSession(configuration: configuration)
}()

private func parseWidgetTimestamp(_ value: String) -> Date? {
    let fractional = ISO8601DateFormatter()
    fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return fractional.date(from: value) ?? ISO8601DateFormatter().date(from: value)
}

private func widgetAttemptTimestamp(_ date: Date = Date()) -> String {
    let fractional = ISO8601DateFormatter()
    fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return fractional.string(from: date)
}

private func mergeDeckSnapshot(current: DeckSnapshot?, incoming: DeckSnapshot) -> DeckSnapshot {
    guard let current, current.deckId == incoming.deckId, current.query == incoming.query,
          let currentAttempt = parseWidgetTimestamp(current.lastAttemptedAt),
          let incomingAttempt = parseWidgetTimestamp(incoming.lastAttemptedAt) else { return incoming }
    let attempt = incomingAttempt > currentAttempt ? incoming : current
    let success: DeckSnapshot
    switch (current.lastSuccessfulAt.flatMap(parseWidgetTimestamp), incoming.lastSuccessfulAt.flatMap(parseWidgetTimestamp)) {
    case (_, nil): success = current
    case (nil, _): success = incoming
    case (let currentSuccess?, let incomingSuccess?): success = incomingSuccess > currentSuccess ? incoming : current
    }
    return DeckSnapshot(version: incoming.version, deckId: incoming.deckId, query: incoming.query,
                        counts: success.counts, results: success.results, state: attempt.state,
                        lastSuccessfulAt: success.lastSuccessfulAt, lastAttemptedAt: attempt.lastAttemptedAt,
                        rate: attempt.rate)
}

struct DeckConfiguration: Codable, Identifiable, Sendable {
    let version: Int; let deckId: String; let name: String; let query: String; let repositories: [DeckRepository]; let profileId: String; let profileKind: String; let scopeId: String; let language: String
    var id: String { deckId }
}
struct DeckRepository: Codable, Sendable { let owner: String; let name: String }
struct DeckCounts: Codable, Sendable { let total: Int; let open: Int; let draft: Int; let merged: Int; let closed: Int; let bounded: Bool }
struct DeckPullRequest: Codable, Identifiable, Sendable { let nodeId: String; let number: Int; let title: String; let repository: String; let state: String; let draft: Bool; var id: String { nodeId } }
struct DeckRate: Codable, Sendable { let limit: Int?; let remaining: Int?; let used: Int?; let resetAt: String?; let resource: String?; let retryAfterSeconds: Int? }
private struct StoredWidgetCredential: Codable, Sendable { let version: Int; let revision: String; let token: String }
private enum WidgetCredential: Sendable {
    case missing
    case readable(token: String, revision: Data)
    case unreadable(revision: Data)
    case unavailable
}
private struct WidgetCredentialReplacement: Codable, Sendable { let version: Int; let profileId: String; let scopeId: String; let deckIds: [String] }
struct DeckSnapshot: Codable, Sendable {
    let version: Int; let deckId: String; let query: String; let counts: DeckCounts; let results: [DeckPullRequest]; var state: String; let lastSuccessfulAt: String?; var lastAttemptedAt: String; var rate: DeckRate?
}
private struct RepositoryValidationFailure: Sendable { let state: String; let rate: DeckRate? }

private struct WidgetBackground: ViewModifier {
    @ViewBuilder func body(content: Content) -> some View {
        if #available(iOS 17.0, *) {
            content.containerBackground(for: .widget) { Color(red: 0.11, green: 0.15, blue: 0.19) }
        } else {
            content.background(Color(red: 0.11, green: 0.15, blue: 0.19))
        }
    }
}

private func sameSelection(_ left: DeckConfiguration, _ right: DeckConfiguration) -> Bool {
    left.deckId == right.deckId && left.query == right.query && left.profileId == right.profileId && left.profileKind == right.profileKind && left.scopeId == right.scopeId
}

private func legacyWidgetToken(_ data: Data) -> String? {
    guard let token = String(data: data, encoding: .utf8),
          let prefix = ["ghp_", "github_pat_"].first(where: token.hasPrefix),
          token.utf8.count > prefix.utf8.count,
          token.utf8.allSatisfy({ byte in
              byte == 95 || (48...57).contains(byte) || (65...90).contains(byte) || (97...122).contains(byte)
          }) else { return nil }
    return token
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
    func save(_ snapshot: DeckSnapshot, whileEnabled configuration: DeckConfiguration, credentialRevision: Data?) -> Bool {
        guard let defaults else { return false }
        let key = snapshotPrefix + snapshot.deckId
        guard let current = self.configuration(snapshot.deckId), sameSelection(current, configuration), credentialMatches(deckId: snapshot.deckId, revision: credentialRevision) else { return false }
        let merged = mergeDeckSnapshot(current: self.snapshot(snapshot.deckId), incoming: snapshot)
        guard let data = try? JSONEncoder().encode(merged) else { return false }
        defaults.set(data, forKey: key)
        guard let current = self.configuration(snapshot.deckId), sameSelection(current, configuration), credentialMatches(deckId: snapshot.deckId, revision: credentialRevision) else {
            if defaults.data(forKey: key) == data { defaults.removeObject(forKey: key) }
            return false
        }
        return true
    }
    func shouldRenderForegroundSnapshot(_ deckId: String) -> Bool {
        let key = foregroundReloadDeadlinePrefix + deckId
        guard let defaults, let deadline = defaults.object(forKey: key) as? Date else { return false }
        if deadline > Date() { return true }
        defaults.removeObject(forKey: key)
        return false
    }
    func credential(_ deckId: String) -> WidgetCredential? {
        guard defaults?.bool(forKey: transactionPrefix + deckId) != true, !credentialReplacementBlocks(deckId) else { return nil }
        let result = credentialData(deckId)
        if result.status == errSecItemNotFound { return .missing }
        guard result.status == errSecSuccess, let data = result.data else { return .unavailable }
        if let stored = try? JSONDecoder().decode(StoredWidgetCredential.self, from: data) {
            guard stored.version == 1, !stored.token.isEmpty else { return .unreadable(revision: data) }
            return .readable(token: stored.token, revision: data)
        }
        guard let token = legacyWidgetToken(data) else { return .unreadable(revision: data) }
        return .readable(token: token, revision: data)
    }
    private func credentialMatches(deckId: String, revision: Data?) -> Bool {
        guard defaults?.bool(forKey: transactionPrefix + deckId) != true, !credentialReplacementBlocks(deckId) else { return false }
        let current = credentialData(deckId)
        if current.status == errSecItemNotFound { return revision == nil }
        return current.status == errSecSuccess && current.data == revision
    }
    private func credentialReplacementBlocks(_ deckId: String) -> Bool {
        guard let data = defaults?.data(forKey: credentialReplacementKey) else { return false }
        guard let transaction = try? JSONDecoder().decode(WidgetCredentialReplacement.self, from: data), transaction.version == 1 else { return true }
        return transaction.deckIds.contains(deckId)
    }
    private func credentialData(_ deckId: String) -> (status: OSStatus, data: Data?) {
        var query: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: credentialService,
                                    kSecAttrAccount as String: deckId, kSecReturnData as String: true, kSecMatchLimit as String: kSecMatchLimitOne,
                                    kSecAttrSynchronizable as String: false]
        if let group = Bundle.main.object(forInfoDictionaryKey: "DevHudWidgetKeychainAccessGroup") as? String { query[kSecAttrAccessGroup as String] = group }
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        return (status, item as? Data)
    }
}

private struct DeckEntry: TimelineEntry {
    let date: Date
    let configuration: DeckConfiguration?
    let snapshot: DeckSnapshot?
}

private actor DeckRefreshCoordinator {
    private struct InFlightRefresh {
        let id: UUID
        let configuration: DeckConfiguration
        let credentialRevision: Data?
        let task: Task<DeckSnapshot, Never>
    }

    private var inFlight: [String: InFlightRefresh] = [:]

    func refresh(deck: DeckConfiguration, credentialRevision: Data?, operation: @escaping @Sendable () async -> DeckSnapshot) async -> DeckSnapshot {
        while let existing = inFlight[deck.deckId] {
            let sharesRequest = sameSelection(existing.configuration, deck) && existing.credentialRevision == credentialRevision
            let snapshot = await existing.task.value
            if inFlight[deck.deckId]?.id == existing.id { inFlight.removeValue(forKey: deck.deckId) }
            if sharesRequest { return snapshot }
        }
        let id = UUID()
        let task = Task { await operation() }
        inFlight[deck.deckId] = InFlightRefresh(id: id, configuration: deck, credentialRevision: credentialRevision, task: task)
        let snapshot = await task.value
        if inFlight[deck.deckId]?.id == id { inFlight.removeValue(forKey: deck.deckId) }
        return snapshot
    }
}

private let deckRefreshCoordinator = DeckRefreshCoordinator()

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
        if store.shouldRenderForegroundSnapshot(deck.deckId) {
            completion(timeline(deck: deck, snapshot: store.snapshot(deck.deckId)))
            return
        }
        guard let credential = store.credential(deck.deckId) else {
            completion(timeline(deck: deck, snapshot: store.snapshot(deck.deckId)))
            return
        }
        Task {
            let previous = store.snapshot(deck.deckId)
            let snapshot: DeckSnapshot
            let credentialRevision: Data?
            switch credential {
            case .missing:
                credentialRevision = nil
                snapshot = await deckRefreshCoordinator.refresh(deck: deck, credentialRevision: credentialRevision) {
                    await Self.refreshWithDeadline(deck: deck, previous: previous, token: nil)
                }
            case .readable(let token, let revision):
                credentialRevision = revision
                snapshot = await deckRefreshCoordinator.refresh(deck: deck, credentialRevision: credentialRevision) {
                    await Self.refreshWithDeadline(deck: deck, previous: previous, token: token)
                }
            case .unreadable(let revision):
                snapshot = Self.failure(deck: deck, previous: previous, state: "error", attempted: widgetAttemptTimestamp(), rate: nil)
                credentialRevision = revision
            case .unavailable:
                guard let current = store.configuration(deck.deckId), sameSelection(current, deck) else {
                    let current = store.configuration(deck.deckId)
                    completion(timeline(deck: current, snapshot: current.flatMap { store.snapshot($0.deckId) }))
                    return
                }
                let snapshot = Self.failure(deck: current, previous: store.snapshot(current.deckId), state: "error", attempted: widgetAttemptTimestamp(), rate: nil)
                completion(timeline(deck: current, snapshot: snapshot))
                return
            }
            guard let current = store.configuration(deck.deckId), sameSelection(current, deck) else {
                let current = store.configuration(deck.deckId)
                completion(timeline(deck: current, snapshot: current.flatMap { store.snapshot($0.deckId) }))
                return
            }
            let stored = store.save(snapshot, whileEnabled: current, credentialRevision: credentialRevision)
            completion(timeline(deck: current, snapshot: store.snapshot(current.deckId)))
        }
    }

    private func timeline(deck: DeckConfiguration?, snapshot: DeckSnapshot?) -> Timeline<DeckEntry> {
        let now = Date()
        var entries = [DeckEntry(date: now, configuration: deck, snapshot: snapshot)]
        if let value = snapshot?.lastSuccessfulAt, let lastSuccess = parseWidgetTimestamp(value) {
            let staleDate = lastSuccess.addingTimeInterval(staleAfter)
            if staleDate > now { entries.append(DeckEntry(date: staleDate, configuration: deck, snapshot: snapshot)) }
        }
        return Timeline(entries: entries, policy: .after(now.addingTimeInterval(30 * 60)))
    }

    private static func refreshWithDeadline(deck: DeckConfiguration, previous: DeckSnapshot?, token: String?) async -> DeckSnapshot {
        let attempted = widgetAttemptTimestamp()
        return await withTaskGroup(of: DeckSnapshot.self) { group in
            group.addTask { await refresh(deck: deck, previous: previous, token: token, attempted: attempted) }
            group.addTask {
                try? await Task.sleep(nanoseconds: refreshDeadlineNanoseconds)
                return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: nil)
            }
            let snapshot = await group.next() ?? failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: nil)
            group.cancelAll()
            return snapshot
        }
    }

    private static func refresh(deck: DeckConfiguration, previous: DeckSnapshot?, token: String?, attempted: String) async -> DeckSnapshot {
        guard let token else { return failure(deck: deck, previous: previous, state: "missing-token", attempted: attempted, rate: nil) }
        if let validation = await validateRepositories(deck: deck, token: token) {
            return failure(deck: deck, previous: previous, state: validation.state, attempted: attempted, rate: validation.rate)
        }
        var components = URLComponents(string: "https://api.github.com/search/issues")!
        components.queryItems = [URLQueryItem(name: "q", value: deck.query), URLQueryItem(name: "per_page", value: "100"), URLQueryItem(name: "page", value: "1")]
        var request = URLRequest(url: components.url!, timeoutInterval: 20)
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("2026-03-10", forHTTPHeaderField: "X-GitHub-Api-Version")
        do {
            let (data, response) = try await githubSession.data(for: request)
            guard let http = response as? HTTPURLResponse else { return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: nil) }
            let rate = responseRate(http)
            let rateLimited = responseIsRateLimited(http, data: data, rate: rate)
            if rateLimited { return failure(deck: deck, previous: previous, state: "rate-limit", attempted: attempted, rate: rate) }
            if http.statusCode == 401 { return failure(deck: deck, previous: previous, state: "missing-token", attempted: attempted, rate: rate) }
            if http.statusCode == 403 || http.statusCode == 404 { return failure(deck: deck, previous: previous, state: "permission", attempted: attempted, rate: rate) }
            guard (200...299).contains(http.statusCode), let root = try JSONSerialization.jsonObject(with: data) as? [String: Any], let items = root["items"] as? [[String: Any]], let total = root["total_count"] as? Int else {
                return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: rate)
            }
            if root["incomplete_results"] as? Bool == true { return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: rate) }
            var open = 0, draft = 0, merged = 0, closed = 0
            var results: [DeckPullRequest] = []
            for item in items.prefix(100) {
                guard let nodeId = item["node_id"] as? String, let number = item["number"] as? Int,
                      let title = item["title"] as? String, let repositoryURL = item["repository_url"] as? String,
                      let isDraft = item["draft"] as? Bool, let itemState = item["state"] as? String,
                      itemState == "open" || itemState == "closed",
                      let pull = item["pull_request"] as? [String: Any], let mergedAt = pull["merged_at"],
                      mergedAt is String || mergedAt is NSNull else {
                    return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: rate)
                }
                let isMerged = mergedAt is String
                let state = isMerged ? "merged" : itemState
                if isDraft { draft += 1 } else if isMerged { merged += 1 } else if state == "closed" { closed += 1 } else { open += 1 }
                results.append(DeckPullRequest(nodeId: nodeId, number: number, title: title, repository: repositoryURL.components(separatedBy: "/repos/").last ?? repositoryURL, state: state, draft: isDraft))
            }
            return DeckSnapshot(version: 1, deckId: deck.deckId, query: deck.query, counts: DeckCounts(total: total, open: open, draft: draft, merged: merged, closed: closed, bounded: total > 100), results: results, state: "fresh", lastSuccessfulAt: attempted, lastAttemptedAt: attempted, rate: rate)
        } catch { return failure(deck: deck, previous: previous, state: "error", attempted: attempted, rate: nil) }
    }

    private static func validateRepositories(deck: DeckConfiguration, token: String) async -> RepositoryValidationFailure? {
        await withTaskGroup(of: RepositoryValidationFailure?.self) { group in
            let initialCount = min(repositoryValidationConcurrency, deck.repositories.count)
            for repository in deck.repositories.prefix(initialCount) {
                group.addTask { await validateRepository(repository, profileKind: deck.profileKind, token: token) }
            }
            var nextIndex = initialCount
            while let result = await group.next() {
                if let result {
                    group.cancelAll()
                    return result
                }
                if nextIndex < deck.repositories.count {
                    let repository = deck.repositories[nextIndex]
                    nextIndex += 1
                    group.addTask { await validateRepository(repository, profileKind: deck.profileKind, token: token) }
                }
            }
            return nil
        }
    }

    private static func validateRepository(_ repository: DeckRepository, profileKind: String, token: String) async -> RepositoryValidationFailure? {
        do {
            let path = "/repos/\(repository.owner)/\(repository.name)"
            let metadata = try await github(path: path, token: token)
            if let state = responseFailure(metadata.response, data: metadata.data, rate: metadata.rate) { return RepositoryValidationFailure(state: state, rate: metadata.rate) }
            if profileKind == "classic" {
                let scopes = Set((metadata.response.value(forHTTPHeaderField: "X-OAuth-Scopes") ?? "").split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) })
                if !scopes.contains("repo") { return RepositoryValidationFailure(state: "permission", rate: metadata.rate) }
            }
            guard let root = try JSONSerialization.jsonObject(with: metadata.data) as? [String: Any] else { return RepositoryValidationFailure(state: "error", rate: metadata.rate) }
            let neverPushed = root["pushed_at"] is NSNull
            for suffix in ["/pulls?state=open&per_page=1", "/issues?state=open&per_page=1"] {
                let access = try await github(path: path + suffix, token: token)
                if let state = responseFailure(access.response, data: access.data, rate: access.rate) { return RepositoryValidationFailure(state: state, rate: access.rate) }
            }
            let contents = try await github(path: path + "/contents", token: token)
            if contents.response.statusCode != 404 || !neverPushed {
                if let state = responseFailure(contents.response, data: contents.data, rate: contents.rate) { return RepositoryValidationFailure(state: state, rate: contents.rate) }
            }
            if profileKind == "fine-grained" {
                let probe = try await github(path: path + "/issues", token: token, method: "POST", body: Data("{}".utf8))
                if probe.response.statusCode != 422 {
                    if let state = responseFailure(probe.response, data: probe.data, rate: probe.rate) { return RepositoryValidationFailure(state: state, rate: probe.rate) }
                    return RepositoryValidationFailure(state: "error", rate: probe.rate)
                }
            }
            return nil
        } catch { return RepositoryValidationFailure(state: "error", rate: nil) }
    }

    private static func github(path: String, token: String, method: String = "GET", body: Data? = nil) async throws -> (data: Data, response: HTTPURLResponse, rate: DeckRate) {
        try Task.checkCancellation()
        var request = URLRequest(url: URL(string: "https://api.github.com" + path)!, timeoutInterval: 20)
        request.httpMethod = method
        request.httpBody = body
        request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        request.setValue("application/vnd.github+json", forHTTPHeaderField: "Accept")
        request.setValue("2026-03-10", forHTTPHeaderField: "X-GitHub-Api-Version")
        if body != nil { request.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        let (data, response) = try await githubSession.data(for: request)
        guard let http = response as? HTTPURLResponse else { throw URLError(.badServerResponse) }
        return (data, http, responseRate(http))
    }

    private static func responseIsRateLimited(_ response: HTTPURLResponse, data: Data, rate: DeckRate) -> Bool {
        if response.statusCode == 429 { return true }
        guard response.statusCode == 403 else { return false }
        if rate.remaining == 0 || rate.retryAfterSeconds != nil { return true }
        guard let root = try? JSONSerialization.jsonObject(with: data) as? [String: Any], let message = root["message"] as? String else { return false }
        return message.lowercased().contains("rate limit")
    }

    private static func responseFailure(_ response: HTTPURLResponse, data: Data, rate: DeckRate) -> String? {
        if (200...299).contains(response.statusCode) { return nil }
        if responseIsRateLimited(response, data: data, rate: rate) { return "rate-limit" }
        if response.statusCode == 401 { return "missing-token" }
        if response.statusCode == 403 || response.statusCode == 404 { return "permission" }
        return "error"
    }

    private static func failure(deck: DeckConfiguration, previous: DeckSnapshot?, state: String, attempted: String, rate: DeckRate?) -> DeckSnapshot {
        var retained: DeckSnapshot
        if let previous, previous.query == deck.query {
            retained = previous
        } else {
            retained = DeckSnapshot(version: 1, deckId: deck.deckId, query: deck.query, counts: DeckCounts(total: 0, open: 0, draft: 0, merged: 0, closed: 0, bounded: false), results: [], state: state, lastSuccessfulAt: nil, lastAttemptedAt: attempted, rate: rate)
        }
        retained.state = state; retained.lastAttemptedAt = attempted; retained.rate = rate
        return retained
    }
    private static func responseRate(_ response: HTTPURLResponse) -> DeckRate {
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
        if snapshot.state == "fresh", let value = snapshot.lastSuccessfulAt, let date = parseWidgetTimestamp(value), entry.date.timeIntervalSince(date) >= staleAfter { return "stale" }
        return snapshot.state
    }
    private var stale: Bool { guard let value = entry.snapshot?.lastSuccessfulAt, let date = parseWidgetTimestamp(value) else { return false }; return entry.date.timeIntervalSince(date) >= staleAfter }
    private func text(_ en: String, _ ko: String) -> String { korean ? ko : en }
    private var status: String {
        let primary: String = switch state { case "stale": text("Stale", "오래됨"); case "missing-token": text("Setup required", "설정 필요"); case "rate-limit": text("Rate limited", "요청 제한됨"); case "permission": text("Access denied", "접근 거부됨"); case "error": text("Refresh failed", "새로 고침 실패"); default: text("Current", "최신") }
        return stale && state != "stale" ? "\(primary) · \(text("Stale", "오래됨"))" : primary
    }
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(entry.configuration?.name ?? text("Open DevHUD to set up", "DevHUD에서 설정하세요")).font(.headline).lineLimit(1)
            if let counts = entry.snapshot?.counts { Text(text("Total \(counts.total) · Open \(counts.open) · Draft \(counts.draft) · Merged \(counts.merged) · Closed \(counts.closed)\(counts.bounded ? " · state counts: first 100" : "")", "전체 \(counts.total) · 열림 \(counts.open) · 초안 \(counts.draft) · 병합 \(counts.merged) · 닫힘 \(counts.closed)\(counts.bounded ? " · 상태 개수: 처음 100개" : "")")).font(.caption).lineLimit(2) }
            ForEach(Array((entry.snapshot?.results ?? []).prefix(3))) { item in Text("\(item.repository)#\(item.number) · \(item.title)").font(.caption).lineLimit(1) }
            Spacer(minLength: 0)
            Text("\(status) · \(text("Last success", "마지막 성공")) \(entry.snapshot?.lastSuccessfulAt ?? text("Never", "없음"))").font(.caption2).foregroundStyle(Color.white.opacity(0.72)).lineLimit(1)
        }
        .padding().foregroundStyle(.white).modifier(WidgetBackground())
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
            .configurationDisplayName(LocalizedStringKey("widget_display_name"))
            .description(LocalizedStringKey("widget_description"))
            .supportedFamilies([.systemMedium, .systemLarge])
    }
}
