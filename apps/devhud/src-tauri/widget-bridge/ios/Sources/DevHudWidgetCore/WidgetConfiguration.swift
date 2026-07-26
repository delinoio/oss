import Foundation
import CoreFoundation

public enum DevHudWidgetContract {
    public static let appGroupIdentifier = "group.dev.deli.devhud"
    public static let extensionIdentifier = "dev.deli.devhud.widget"
    public static let storageKey = "devhud.widget-configuration.v1"
    public static let schemaVersion = 1
}

public enum WidgetSlot: String, CaseIterable, Codable, Sendable {
    case primary
    case secondary
    case tertiary
}

public struct StableToolID: Codable, Equatable, Hashable, Sendable {
    public let rawValue: String

    public init(_ rawValue: String) throws {
        let expression = try NSRegularExpression(pattern: "^[a-z]+(?:-[a-z0-9]+)*$")
        let range = NSRange(rawValue.startIndex..<rawValue.endIndex, in: rawValue)
        guard expression.firstMatch(in: rawValue, range: range)?.range == range else {
            throw WidgetConfigurationError.incompatible
        }
        self.rawValue = rawValue
    }

    public init(from decoder: Decoder) throws {
        try self.init(decoder.singleValueContainer().decode(String.self))
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(rawValue)
    }
}

public struct WidgetSlotReference: Codable, Equatable, Sendable {
    public let slot: WidgetSlot
    public let toolId: StableToolID

    public init(slot: WidgetSlot, toolId: StableToolID) {
        self.slot = slot
        self.toolId = toolId
    }
}

public struct WidgetConfiguration: Codable, Equatable, Sendable {
    public let slots: [WidgetSlotReference]

    public init(slots: [WidgetSlotReference]) throws {
        guard Set(slots.map(\.slot)).count == slots.count else {
            throw WidgetConfigurationError.incompatible
        }
        self.slots = slots
    }
}

public struct WidgetConfigurationRecord: Codable, Equatable, Sendable {
    public let version: Int
    public let configuration: WidgetConfiguration

    public init(
        version: Int = DevHudWidgetContract.schemaVersion,
        configuration: WidgetConfiguration
    ) throws {
        guard version == DevHudWidgetContract.schemaVersion else {
            throw version > DevHudWidgetContract.schemaVersion
                ? WidgetConfigurationError.futureVersion
                : WidgetConfigurationError.incompatible
        }
        self.version = version
        self.configuration = configuration
    }

    public static var empty: WidgetConfigurationRecord {
        try! WidgetConfigurationRecord(configuration: WidgetConfiguration(slots: []))
    }
}

public enum WidgetConfigurationError: String, Error, Equatable, Sendable {
    case corrupt
    case futureVersion = "future-version"
    case incompatible
    case refreshFailed = "refresh-failed"
    case sharedStoreUnavailable = "shared-store-unavailable"
    case writeFailed = "write-failed"
}

public enum WidgetConfigurationCodec {
    public static func decode(_ data: Data) throws -> WidgetConfigurationRecord {
        let json: Any
        do {
            json = try JSONSerialization.jsonObject(with: data)
        } catch {
            throw WidgetConfigurationError.corrupt
        }
        guard let root = json as? [String: Any] else {
            throw WidgetConfigurationError.corrupt
        }
        guard Set(root.keys) == Set(["version", "configuration"]) else {
            throw WidgetConfigurationError.incompatible
        }
        guard
            let version = root["version"] as? NSNumber,
            CFGetTypeID(version) != CFBooleanGetTypeID(),
            version.doubleValue.rounded() == version.doubleValue
        else {
            throw WidgetConfigurationError.incompatible
        }
        if version.intValue > DevHudWidgetContract.schemaVersion {
            throw WidgetConfigurationError.futureVersion
        }
        guard version.intValue == DevHudWidgetContract.schemaVersion else {
            throw WidgetConfigurationError.incompatible
        }
        try validateConfigurationObject(root["configuration"])
        do {
            let record = try JSONDecoder().decode(WidgetConfigurationRecord.self, from: data)
            return try WidgetConfigurationRecord(
                version: record.version,
                configuration: WidgetConfiguration(slots: record.configuration.slots)
            )
        } catch let error as WidgetConfigurationError {
            throw error
        } catch {
            throw WidgetConfigurationError.incompatible
        }
    }

    public static func encode(_ record: WidgetConfigurationRecord) throws -> Data {
        _ = try WidgetConfigurationRecord(
            version: record.version,
            configuration: WidgetConfiguration(slots: record.configuration.slots)
        )
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.sortedKeys]
        return try encoder.encode(record)
    }

    private static func validateConfigurationObject(_ value: Any?) throws {
        guard
            let configuration = value as? [String: Any],
            Set(configuration.keys) == Set(["slots"]),
            let slots = configuration["slots"] as? [[String: Any]]
        else {
            throw WidgetConfigurationError.incompatible
        }
        var seenSlots = Set<String>()
        for reference in slots {
            guard
                Set(reference.keys) == Set(["slot", "toolId"]),
                let slot = reference["slot"] as? String,
                WidgetSlot(rawValue: slot) != nil,
                seenSlots.insert(slot).inserted,
                let toolId = reference["toolId"] as? String
            else {
                throw WidgetConfigurationError.incompatible
            }
            _ = try StableToolID(toolId)
        }
    }
}
