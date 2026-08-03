import CoreFoundation
import Foundation

public enum DevHudWidgetContract {
    public static let appGroupIdentifier = "group.dev.deli.devhud"
    public static let extensionIdentifier = "dev.deli.devhud.widget"
    public static let storageKey = "devhud.widget-configuration.v1"
    public static let encryptionKeyService = "dev.deli.devhud.widget.snapshot-key.v1"
    public static let schemaVersion = 1
    public static let maximumWidgets = 20
    public static let maximumPullRequests = 10
    public static let genericNotificationText = "Deck view updated"
    public static let appLinkPath = "/devhud/deck/open"
}

public enum DeckWidgetFamily: String, CaseIterable, Codable, Sendable {
    case appleSmall = "apple-small"
    case appleMedium = "apple-medium"
    case appleLarge = "apple-large"
    case androidCompact = "android-compact"
    case androidWide = "android-wide"
    case androidList = "android-list"
}

public enum DeckWidgetPrivacy: String, CaseIterable, Codable, Sendable {
    case countsOnly = "counts-only"
    case repositoryAndTitles = "repository-and-titles"
}

public enum DeckWidgetFreshness: String, CaseIterable, Codable, Sendable {
    case fresh
    case stale
    case offline
    case disconnected
    case neverRefreshed = "never-refreshed"
}

public struct WidgetPullRequest: Codable, Equatable, Sendable {
    public let repositoryOwner: String
    public let repositoryName: String
    public let number: UInt64
    public let title: String
}

public struct WidgetSnapshot: Codable, Equatable, Sendable {
    public let matchingCount: UInt32
    public let pullRequests: [WidgetPullRequest]
    public let freshness: DeckWidgetFreshness
    public let offline: Bool
    public let generatedAt: String
}

public struct DeckWidgetInstance: Codable, Equatable, Sendable, Identifiable {
    public var id: String { widgetId }
    public let widgetId: String
    public let viewId: String
    public let family: DeckWidgetFamily
    public let privacy: DeckWidgetPrivacy
    public let snapshot: WidgetSnapshot
}

public struct WidgetConfiguration: Codable, Equatable, Sendable {
    public let accountId: String
    public let widgets: [DeckWidgetInstance]
}

public struct WidgetConfigurationRecord: Codable, Equatable, Sendable {
    public let version: Int
    public let configuration: WidgetConfiguration

    public static var empty: WidgetConfigurationRecord {
        WidgetConfigurationRecord(
            version: DevHudWidgetContract.schemaVersion,
            configuration: WidgetConfiguration(accountId: "", widgets: [])
        )
    }
}

public enum WidgetConfigurationError: String, Error, Equatable, Sendable {
    case corrupt
    case futureVersion = "future-version"
    case incompatible
    case refreshFailed = "refresh-failed"
    case sharedStoreUnavailable = "shared-store-unavailable"
    case writeFailed = "write-failed"
    case encryptionFailed = "encryption-failed"
}

public enum WidgetConfigurationCodec {
    public static func decode(_ data: Data) throws -> WidgetConfigurationRecord {
        let json: Any
        do { json = try JSONSerialization.jsonObject(with: data) }
        catch { throw WidgetConfigurationError.corrupt }
        guard let root = json as? [String: Any] else {
            throw WidgetConfigurationError.corrupt
        }
        try exactKeys(root, ["version", "configuration"])
        guard let version = integer(root["version"]) else {
            throw WidgetConfigurationError.incompatible
        }
        if version > DevHudWidgetContract.schemaVersion {
            throw WidgetConfigurationError.futureVersion
        }
        guard version == DevHudWidgetContract.schemaVersion,
              let configuration = root["configuration"] as? [String: Any]
        else { throw WidgetConfigurationError.incompatible }
        try validate(configuration)
        do { return try JSONDecoder().decode(WidgetConfigurationRecord.self, from: data) }
        catch { throw WidgetConfigurationError.incompatible }
    }

    public static func encode(_ record: WidgetConfigurationRecord) throws -> Data {
        let data = try JSONEncoder.sorted.encode(record)
        _ = try decode(data)
        return data
    }

    private static func validate(_ configuration: [String: Any]) throws {
        try exactKeys(configuration, ["accountId", "widgets"])
        guard let accountId = configuration["accountId"] as? String,
              let widgets = configuration["widgets"] as? [[String: Any]],
              widgets.count <= DevHudWidgetContract.maximumWidgets,
              widgets.isEmpty || UUID(uuidString: accountId) != nil
        else { throw WidgetConfigurationError.incompatible }
        var widgetIds = Set<String>()
        for widget in widgets {
            try exactKeys(widget, ["widgetId", "viewId", "family", "privacy", "snapshot"])
            guard let widgetId = widget["widgetId"] as? String,
                  UUID(uuidString: widgetId) != nil,
                  widgetIds.insert(widgetId).inserted,
                  let viewId = widget["viewId"] as? String,
                  UUID(uuidString: viewId) != nil,
                  let family = widget["family"] as? String,
                  DeckWidgetFamily(rawValue: family) != nil,
                  let privacyRaw = widget["privacy"] as? String,
                  let privacy = DeckWidgetPrivacy(rawValue: privacyRaw),
                  let snapshot = widget["snapshot"] as? [String: Any]
            else { throw WidgetConfigurationError.incompatible }
            try validate(snapshot, privacy: privacy)
        }
    }

    private static func validate(
        _ snapshot: [String: Any],
        privacy: DeckWidgetPrivacy
    ) throws {
        try exactKeys(snapshot, ["matchingCount", "pullRequests", "freshness", "offline", "generatedAt"])
        guard let count = integer(snapshot["matchingCount"]), count >= 0,
              let pullRequests = snapshot["pullRequests"] as? [[String: Any]],
              pullRequests.count <= DevHudWidgetContract.maximumPullRequests,
              privacy != .countsOnly || pullRequests.isEmpty,
              let freshness = snapshot["freshness"] as? String,
              DeckWidgetFreshness(rawValue: freshness) != nil,
              snapshot["offline"] is Bool,
              let generatedAt = snapshot["generatedAt"] as? String,
              ISO8601DateFormatter().date(from: generatedAt) != nil
        else { throw WidgetConfigurationError.incompatible }
        for pullRequest in pullRequests {
            try exactKeys(pullRequest, ["repositoryOwner", "repositoryName", "number", "title"])
            guard let owner = pullRequest["repositoryOwner"] as? String, !owner.isEmpty,
                  let repository = pullRequest["repositoryName"] as? String, !repository.isEmpty,
                  let number = integer(pullRequest["number"]), number > 0,
                  let title = pullRequest["title"] as? String, !title.isEmpty
            else { throw WidgetConfigurationError.incompatible }
        }
    }

    private static func integer(_ value: Any?) -> Int? {
        guard let number = value as? NSNumber,
              CFGetTypeID(number) != CFBooleanGetTypeID(),
              number.doubleValue.rounded() == number.doubleValue
        else { return nil }
        return number.intValue
    }

    private static func exactKeys(_ object: [String: Any], _ expected: Set<String>) throws {
        guard Set(object.keys) == expected else {
            throw WidgetConfigurationError.incompatible
        }
    }
}

private extension JSONEncoder {
    static var sorted: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return encoder
    }
}

public enum DeckWidgetAction: Equatable, Sendable {
    case openView(viewId: String)
    case openPullRequest(viewId: String, owner: String, repository: String, number: UInt64)
    case refresh(viewId: String)
    case resolveEvent(eventId: String)

    public var url: URL? {
        var components = URLComponents()
        components.scheme = "https"
        components.host = "deli.dev"
        components.path = DevHudWidgetContract.appLinkPath
        switch self {
        case .openView(let viewId):
            components.queryItems = [.init(name: "action", value: "open-view"), .init(name: "view", value: viewId)]
        case .openPullRequest(let viewId, let owner, let repository, let number):
            components.queryItems = [
                .init(name: "action", value: "open-pr"), .init(name: "view", value: viewId),
                .init(name: "owner", value: owner), .init(name: "repository", value: repository),
                .init(name: "number", value: String(number)),
            ]
        case .refresh(let viewId):
            components.queryItems = [.init(name: "action", value: "refresh"), .init(name: "view", value: viewId)]
        case .resolveEvent(let eventId):
            components.queryItems = [.init(name: "action", value: "resolve-event"), .init(name: "event", value: eventId)]
        }
        return components.url
    }
}

public enum DeckNotificationPolicy {
    public static func payloadEventId(_ userInfo: [AnyHashable: Any]) -> String? {
        guard Set(userInfo.keys.compactMap { $0 as? String }) == ["eventId"],
              let value = userInfo["eventId"] as? String,
              value.range(of: "^[A-Za-z0-9_-]{16,128}$", options: .regularExpression) != nil
        else { return nil }
        return value
    }

    public static func text(detailedText: String?, localDetailEnabled: Bool) -> String {
        guard localDetailEnabled, let detailedText, !detailedText.isEmpty else {
            return DevHudWidgetContract.genericNotificationText
        }
        return detailedText
    }
}
