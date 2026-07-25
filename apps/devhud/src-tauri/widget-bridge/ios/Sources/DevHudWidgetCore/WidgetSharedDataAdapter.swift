import Foundation
import WidgetKit

public final class WidgetSharedDataAdapter: @unchecked Sendable {
    private let defaults: UserDefaults
    private let lock = NSLock()

    public init(defaults: UserDefaults) {
        self.defaults = defaults
    }

    public static func live() throws -> WidgetSharedDataAdapter {
        guard
            var container = FileManager.default.containerURL(
                forSecurityApplicationGroupIdentifier: DevHudWidgetContract.appGroupIdentifier
            )
        else {
            throw WidgetConfigurationError.sharedStoreUnavailable
        }
        var resourceValues = URLResourceValues()
        resourceValues.isExcludedFromBackup = true
        try container.setResourceValues(resourceValues)
        guard
            let defaults = UserDefaults(
                suiteName: DevHudWidgetContract.appGroupIdentifier
            )
        else {
            throw WidgetConfigurationError.sharedStoreUnavailable
        }
        return WidgetSharedDataAdapter(defaults: defaults)
    }

    public func readRecord() throws -> WidgetConfigurationRecord {
        try synchronized {
            guard let value = defaults.object(forKey: DevHudWidgetContract.storageKey) else {
                return .empty
            }
            guard let raw = value as? String, let data = raw.data(using: .utf8) else {
                throw WidgetConfigurationError.corrupt
            }
            return try WidgetConfigurationCodec.decode(data)
        }
    }

    public func readRawRecord() throws -> String? {
        try synchronized {
            guard let value = defaults.object(forKey: DevHudWidgetContract.storageKey) else {
                return nil
            }
            guard let raw = value as? String, let data = raw.data(using: .utf8) else {
                throw WidgetConfigurationError.corrupt
            }
            _ = try WidgetConfigurationCodec.decode(data)
            return raw
        }
    }

    public func writeRawRecord(_ raw: String) throws {
        guard let data = raw.data(using: .utf8) else {
            throw WidgetConfigurationError.incompatible
        }
        _ = try WidgetConfigurationCodec.decode(data)
        try synchronized {
            defaults.set(raw, forKey: DevHudWidgetContract.storageKey)
            guard defaults.string(forKey: DevHudWidgetContract.storageKey) == raw else {
                throw WidgetConfigurationError.writeFailed
            }
        }
    }

    public func reset() {
        synchronized {
            defaults.removeObject(forKey: DevHudWidgetContract.storageKey)
        }
    }

    private func synchronized<Value>(
        _ operation: () throws -> Value
    ) rethrows -> Value {
        lock.lock()
        defer { lock.unlock() }
        return try operation()
    }
}

public protocol WidgetRefreshing {
    @discardableResult
    func refresh() throws -> UInt32
}

public struct WidgetKitRefresher: WidgetRefreshing {
    public init() {}

    @discardableResult
    public func refresh() throws -> UInt32 {
        WidgetCenter.shared.reloadTimelines(ofKind: DevHudWidgetContract.extensionIdentifier)
        return 0
    }
}

public final class WidgetConfigurationService {
    private let adapter: WidgetSharedDataAdapter
    private let refresher: any WidgetRefreshing

    public init(
        adapter: WidgetSharedDataAdapter,
        refresher: any WidgetRefreshing = WidgetKitRefresher()
    ) {
        self.adapter = adapter
        self.refresher = refresher
    }

    public func readRawRecord() throws -> String? {
        try adapter.readRawRecord()
    }

    @discardableResult
    public func writeRawRecord(_ raw: String) throws -> UInt32 {
        try adapter.writeRawRecord(raw)
        return try refresher.refresh()
    }

    @discardableResult
    public func reset() throws -> UInt32 {
        adapter.reset()
        return try refresher.refresh()
    }
}
