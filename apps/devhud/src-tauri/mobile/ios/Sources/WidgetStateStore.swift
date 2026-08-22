import Foundation

private let widgetStateVersion = 1
private let widgetStateDirectory = "Library/Application Support/io.delino.devhud/widget-state-v2"
private let legacyWidgetConfigurationPrefix = "widget.configuration."
private let legacyWidgetSnapshotPrefix = "widget.snapshot."
private let legacyWidgetTransactionPrefix = "widget.transaction."
private let legacyWidgetCredentialReplacementKey = "widget.credential-replacement.v1"
private let legacyWidgetForegroundReloadDeadlineKey = "widget.foreground-reload-deadline.v1"
private let legacyWidgetForegroundReloadDeadlinePrefix = "widget.foreground-reload-deadline.v1."

enum WidgetStateRead<Value> {
    case success(Value)
    case failure
}

struct StoredWidgetDeckState: Codable, Equatable {
    let version: Int
    let deckId: String
    var configuration: Data?
    var snapshot: Data?
    var transactionPending: Bool
    var foregroundReloadDeadline: Date?

    init(deckId: String, configuration: Data? = nil, snapshot: Data? = nil,
         transactionPending: Bool = false, foregroundReloadDeadline: Date? = nil) {
        version = widgetStateVersion
        self.deckId = deckId
        self.configuration = configuration
        self.snapshot = snapshot
        self.transactionPending = transactionPending
        self.foregroundReloadDeadline = foregroundReloadDeadline
    }
}

struct StoredWidgetStateMetadata: Codable, Equatable {
    let version: Int
    var credentialReplacement: Data?
    var legacyMigrationCompleted: Bool

    init(credentialReplacement: Data? = nil, legacyMigrationCompleted: Bool = false) {
        version = widgetStateVersion
        self.credentialReplacement = credentialReplacement
        self.legacyMigrationCompleted = legacyMigrationCompleted
    }
}

final class WidgetStateStore {
    private enum StoreError: Error { case unavailable, invalidRecord }

    private let appGroup: String
    private let fileManager: FileManager
    private let preparationLock = NSLock()
    private var prepared = false

    init(appGroup: String, fileManager: FileManager = .default) {
        self.appGroup = appGroup
        self.fileManager = fileManager
    }

    func allDeckStates() -> WidgetStateRead<[StoredWidgetDeckState]> {
        withPreparedRead { root in
            let decks = root.appendingPathComponent("decks", isDirectory: true)
            return try fileManager.contentsOfDirectory(at: decks, includingPropertiesForKeys: nil)
                .filter { $0.pathExtension == "json" }
                .map { url in
                    guard let state = try readDeckState(at: url, expectedDeckId: nil), deckURL(root, deckId: state.deckId) == url else { throw StoreError.invalidRecord }
                    return state
                }
        }
    }

    func deckState(_ deckId: String) -> WidgetStateRead<StoredWidgetDeckState?> {
        withPreparedRead { root in try readDeckState(at: deckURL(root, deckId: deckId), expectedDeckId: deckId) }
    }

    func updateDeckState(_ deckId: String, _ update: (inout StoredWidgetDeckState?) -> Bool) -> Bool {
        withPreparedWrite { root in
            let url = deckURL(root, deckId: deckId)
            var state = try readDeckState(at: url, expectedDeckId: deckId)
            guard update(&state) else { return false }
            if let state {
                guard state.version == widgetStateVersion, state.deckId == deckId else { throw StoreError.invalidRecord }
                try write(state, to: url)
            } else if fileManager.fileExists(atPath: url.path) {
                try fileManager.removeItem(at: url)
            }
            return true
        }
    }

    func metadata() -> WidgetStateRead<StoredWidgetStateMetadata> {
        withPreparedRead { root in try readMetadata(root) }
    }

    func updateMetadata(_ update: (inout StoredWidgetStateMetadata) -> Bool) -> Bool {
        withPreparedWrite { root in
            var metadata = try readMetadata(root)
            guard update(&metadata), metadata.version == widgetStateVersion else { return false }
            try write(metadata, to: metadataURL(root))
            return true
        }
    }

    func clear() -> Bool {
        withPreparedWrite { root in
            let decks = root.appendingPathComponent("decks", isDirectory: true)
            for url in try fileManager.contentsOfDirectory(at: decks, includingPropertiesForKeys: nil) {
                try fileManager.removeItem(at: url)
            }
            try write(StoredWidgetStateMetadata(legacyMigrationCompleted: true), to: metadataURL(root))
            guard let defaults = UserDefaults(suiteName: appGroup) else { throw StoreError.unavailable }
            removeLegacyDefaults(defaults)
            guard defaults.synchronize() else { throw StoreError.unavailable }
            return true
        }
    }

    private func withPreparedRead<Value>(_ access: (URL) throws -> Value) -> WidgetStateRead<Value> {
        guard prepare(), let root = rootURL() else { return .failure }
        do { return .success(try coordinatedRead(root, access)) }
        catch { return .failure }
    }

    private func withPreparedWrite(_ access: (URL) throws -> Bool) -> Bool {
        guard prepare(), let root = rootURL() else { return false }
        do { return try coordinatedWrite(root, access) }
        catch { return false }
    }

    private func prepare() -> Bool {
        preparationLock.lock()
        defer { preparationLock.unlock() }
        if prepared { return true }
        guard let root = rootURL() else { return false }
        do {
            let decks = root.appendingPathComponent("decks", isDirectory: true)
            try fileManager.createDirectory(at: decks, withIntermediateDirectories: true)
            try excludeFromBackup(root)
            try excludeFromBackup(decks)
            try coordinatedWrite(root) { coordinatedRoot in
                try migrateLegacyDefaults(coordinatedRoot)
                return true
            }
            prepared = true
            return true
        } catch {
            return false
        }
    }

    private func migrateLegacyDefaults(_ root: URL) throws {
        guard let defaults = UserDefaults(suiteName: appGroup) else { throw StoreError.unavailable }
        var metadata = try readMetadata(root)
        if !metadata.legacyMigrationCompleted {
            let entries = defaults.dictionaryRepresentation()
            let deckIds = Set(entries.keys.compactMap { key -> String? in
                for prefix in [legacyWidgetConfigurationPrefix, legacyWidgetSnapshotPrefix, legacyWidgetTransactionPrefix, legacyWidgetForegroundReloadDeadlinePrefix] where key.hasPrefix(prefix) {
                    return String(key.dropFirst(prefix.count))
                }
                return nil
            })
            for deckId in deckIds {
                let state = StoredWidgetDeckState(
                    deckId: deckId,
                    configuration: defaults.data(forKey: legacyWidgetConfigurationPrefix + deckId),
                    snapshot: defaults.data(forKey: legacyWidgetSnapshotPrefix + deckId),
                    transactionPending: defaults.bool(forKey: legacyWidgetTransactionPrefix + deckId),
                    foregroundReloadDeadline: defaults.object(forKey: legacyWidgetForegroundReloadDeadlinePrefix + deckId) as? Date
                )
                try write(state, to: deckURL(root, deckId: deckId))
            }
            if let replacement = defaults.data(forKey: legacyWidgetCredentialReplacementKey) {
                metadata.credentialReplacement = replacement
            }
            // Persist every migrated value before deleting its legacy copy.
            // A crash after the defaults sync can then resume from this file.
            try write(metadata, to: metadataURL(root))
            // The former unscoped deadline is intentionally discarded. It
            // cannot be associated with one Deck safely during migration.
            defaults.removeObject(forKey: legacyWidgetForegroundReloadDeadlineKey)
            removeLegacyDefaults(defaults)
            guard defaults.synchronize() else { throw StoreError.unavailable }
            metadata.legacyMigrationCompleted = true
            try write(metadata, to: metadataURL(root))
        } else {
            // A second process may still have cached legacy preferences. Keep
            // the file migration authoritative and erase those cached copies.
            removeLegacyDefaults(defaults)
            guard defaults.synchronize() else { throw StoreError.unavailable }
        }
    }

    private func removeLegacyDefaults(_ defaults: UserDefaults) {
        for key in defaults.dictionaryRepresentation().keys where
            key.hasPrefix(legacyWidgetConfigurationPrefix) ||
            key.hasPrefix(legacyWidgetSnapshotPrefix) ||
            key.hasPrefix(legacyWidgetTransactionPrefix) ||
            key.hasPrefix(legacyWidgetForegroundReloadDeadlinePrefix) {
            defaults.removeObject(forKey: key)
        }
        defaults.removeObject(forKey: legacyWidgetCredentialReplacementKey)
        defaults.removeObject(forKey: legacyWidgetForegroundReloadDeadlineKey)
    }

    private func rootURL() -> URL? {
        fileManager.containerURL(forSecurityApplicationGroupIdentifier: appGroup)?.appendingPathComponent(widgetStateDirectory, isDirectory: true)
    }

    private func deckURL(_ root: URL, deckId: String) -> URL {
        let encoded = Data(deckId.utf8).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
        return root.appendingPathComponent("decks", isDirectory: true)
            .appendingPathComponent(encoded.isEmpty ? "_" : encoded)
            .appendingPathExtension("json")
    }

    private func metadataURL(_ root: URL) -> URL { root.appendingPathComponent("metadata.json") }

    private func readDeckState(at url: URL, expectedDeckId: String?) throws -> StoredWidgetDeckState? {
        guard fileManager.fileExists(atPath: url.path) else { return nil }
        let state = try JSONDecoder().decode(StoredWidgetDeckState.self, from: Data(contentsOf: url))
        guard state.version == widgetStateVersion, expectedDeckId == nil || state.deckId == expectedDeckId else { throw StoreError.invalidRecord }
        return state
    }

    private func readMetadata(_ root: URL) throws -> StoredWidgetStateMetadata {
        let url = metadataURL(root)
        guard fileManager.fileExists(atPath: url.path) else { return StoredWidgetStateMetadata() }
        let metadata = try JSONDecoder().decode(StoredWidgetStateMetadata.self, from: Data(contentsOf: url))
        guard metadata.version == widgetStateVersion else { throw StoreError.invalidRecord }
        return metadata
    }

    private func write<Value: Encodable>(_ value: Value, to url: URL) throws {
        try JSONEncoder().encode(value).write(to: url, options: .atomic)
        // Atomic replacement can reset this resource property, so enforce it
        // after every successful save as required by the backup contract.
        try excludeFromBackup(url)
        let excluded = try url.resourceValues(forKeys: [.isExcludedFromBackupKey]).isExcludedFromBackup
        guard excluded == true else { throw StoreError.unavailable }
    }

    private func excludeFromBackup(_ url: URL) throws {
        var mutable = url
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try mutable.setResourceValues(values)
    }

    private func coordinatedRead<Value>(_ root: URL, _ access: (URL) throws -> Value) throws -> Value {
        var coordinationError: NSError?
        var result: Result<Value, Error>?
        NSFileCoordinator(filePresenter: nil).coordinate(readingItemAt: root, options: [], error: &coordinationError) { coordinatedRoot in
            result = Result { try access(coordinatedRoot) }
        }
        if let coordinationError { throw coordinationError }
        guard let result else { throw StoreError.unavailable }
        return try result.get()
    }

    private func coordinatedWrite<Value>(_ root: URL, _ access: (URL) throws -> Value) throws -> Value {
        var coordinationError: NSError?
        var result: Result<Value, Error>?
        NSFileCoordinator(filePresenter: nil).coordinate(writingItemAt: root, options: [], error: &coordinationError) { coordinatedRoot in
            result = Result { try access(coordinatedRoot) }
        }
        if let coordinationError { throw coordinationError }
        guard let result else { throw StoreError.unavailable }
        return try result.get()
    }
}
