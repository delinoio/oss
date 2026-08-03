import CryptoKit
import Foundation
import WidgetKit

public protocol WidgetRecordEncrypting: Sendable {
    func encrypt(_ plaintext: Data, accountId: String) throws -> String
    func decrypt(_ envelope: String) throws -> Data
    func reset(_ envelope: String?) throws
}

private struct EncryptedWidgetEnvelope: Codable {
    let version: Int
    let accountBinding: String
    let ciphertext: String
}

/** AES-GCM plus iOS Data Protection; the key is scoped to one opaque account binding. */
public final class ProtectedWidgetRecordEncryptor: WidgetRecordEncrypting, @unchecked Sendable {
    private let container: URL
    private let lock = NSLock()

    public init(container: URL) { self.container = container }

    public func encrypt(_ plaintext: Data, accountId: String) throws -> String {
        try synchronized {
            let binding = Self.binding(accountId)
            let key = try loadOrCreateKey(binding)
            let sealed = try AES.GCM.seal(plaintext, using: key, authenticating: Data(binding.utf8))
            guard let combined = sealed.combined else { throw WidgetConfigurationError.encryptionFailed }
            let envelope = EncryptedWidgetEnvelope(
                version: 1,
                accountBinding: binding,
                ciphertext: combined.base64EncodedString()
            )
            return String(data: try JSONEncoder().encode(envelope), encoding: .utf8)!
        }
    }

    public func decrypt(_ envelope: String) throws -> Data {
        try synchronized {
            guard let envelopeData = envelope.data(using: .utf8),
                  let value = try? JSONDecoder().decode(EncryptedWidgetEnvelope.self, from: envelopeData),
                  value.version == 1,
                  let ciphertext = Data(base64Encoded: value.ciphertext)
            else { throw WidgetConfigurationError.corrupt }
            let key = try loadKey(value.accountBinding)
            let box = try AES.GCM.SealedBox(combined: ciphertext)
            do {
                return try AES.GCM.open(box, using: key, authenticating: Data(value.accountBinding.utf8))
            } catch { throw WidgetConfigurationError.corrupt }
        }
    }

    public func reset(_ envelope: String?) throws {
        try synchronized {
            if let envelope, let data = envelope.data(using: .utf8),
               let value = try? JSONDecoder().decode(EncryptedWidgetEnvelope.self, from: data) {
                try? FileManager.default.removeItem(at: keyURL(value.accountBinding))
            }
        }
    }

    private func loadOrCreateKey(_ binding: String) throws -> SymmetricKey {
        if let key = try? loadKey(binding) { return key }
        let data = SymmetricKey(size: .bits256).withUnsafeBytes { Data($0) }
        let url = keyURL(binding)
        do {
            try data.write(to: url, options: [.atomic, .completeFileProtection])
            return SymmetricKey(data: data)
        } catch { throw WidgetConfigurationError.encryptionFailed }
    }

    private func loadKey(_ binding: String) throws -> SymmetricKey {
        do {
            let data = try Data(contentsOf: keyURL(binding))
            guard data.count == 32 else { throw WidgetConfigurationError.corrupt }
            return SymmetricKey(data: data)
        } catch let error as WidgetConfigurationError { throw error }
        catch { throw WidgetConfigurationError.corrupt }
    }

    private func keyURL(_ binding: String) -> URL {
        container.appendingPathComponent("deck-widget-\(binding).key", isDirectory: false)
    }

    private static func binding(_ accountId: String) -> String {
        SHA256.hash(data: Data(accountId.utf8)).map { String(format: "%02x", $0) }.joined()
    }

    private func synchronized<Value>(_ operation: () throws -> Value) rethrows -> Value {
        lock.lock(); defer { lock.unlock() }
        return try operation()
    }
}

/** Test-only injectable cipher; live adapters never use it. */
public struct IdentityWidgetRecordEncryptor: WidgetRecordEncrypting {
    public init() {}
    public func encrypt(_ plaintext: Data, accountId: String) throws -> String {
        String(decoding: plaintext, as: UTF8.self)
    }
    public func decrypt(_ envelope: String) throws -> Data { Data(envelope.utf8) }
    public func reset(_ envelope: String?) throws {}
}

public final class WidgetSharedDataAdapter: @unchecked Sendable {
    private let defaults: UserDefaults
    private let encryptor: any WidgetRecordEncrypting
    private let lock = NSLock()

    public init(defaults: UserDefaults, encryptor: any WidgetRecordEncrypting) {
        self.defaults = defaults
        self.encryptor = encryptor
    }

    public static func live() throws -> WidgetSharedDataAdapter {
        guard var container = FileManager.default.containerURL(
            forSecurityApplicationGroupIdentifier: DevHudWidgetContract.appGroupIdentifier
        ), let defaults = UserDefaults(suiteName: DevHudWidgetContract.appGroupIdentifier)
        else { throw WidgetConfigurationError.sharedStoreUnavailable }
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try container.setResourceValues(values)
        return WidgetSharedDataAdapter(
            defaults: defaults,
            encryptor: ProtectedWidgetRecordEncryptor(container: container)
        )
    }

    public func readRecord() throws -> WidgetConfigurationRecord {
        guard let raw = try readRawRecord() else { return .empty }
        return try WidgetConfigurationCodec.decode(Data(raw.utf8))
    }

    public func readRawRecord() throws -> String? {
        try synchronized {
            guard let value = defaults.object(forKey: DevHudWidgetContract.storageKey) else { return nil }
            guard let envelope = value as? String else { throw WidgetConfigurationError.corrupt }
            if let legacy = try WidgetConfigurationCodec.decodeLegacy(Data(envelope.utf8)) {
                let plaintext = try WidgetConfigurationCodec.encode(legacy)
                let migratedEnvelope = try encryptor.encrypt(
                    plaintext,
                    accountId: legacy.configuration.accountId
                )
                defaults.set(migratedEnvelope, forKey: DevHudWidgetContract.storageKey)
                guard defaults.string(forKey: DevHudWidgetContract.storageKey) == migratedEnvelope else {
                    throw WidgetConfigurationError.writeFailed
                }
                return String(decoding: plaintext, as: UTF8.self)
            }
            let plaintext = try encryptor.decrypt(envelope)
            _ = try WidgetConfigurationCodec.decode(plaintext)
            return String(decoding: plaintext, as: UTF8.self)
        }
    }

    public func writeRawRecord(_ raw: String) throws {
        let data = Data(raw.utf8)
        let record = try WidgetConfigurationCodec.decode(data)
        let previousEnvelope = try synchronized { () throws -> String? in
            guard let value = defaults.object(forKey: DevHudWidgetContract.storageKey) else { return nil }
            guard let envelope = value as? String else { throw WidgetConfigurationError.corrupt }
            return envelope
        }
        let previousAccountId = try previousEnvelope.map {
            try WidgetConfigurationCodec.decode(encryptor.decrypt($0)).configuration.accountId
        }
        let envelope = try encryptor.encrypt(data, accountId: record.configuration.accountId)
        do {
            try synchronized {
                defaults.set(envelope, forKey: DevHudWidgetContract.storageKey)
                guard defaults.string(forKey: DevHudWidgetContract.storageKey) == envelope else {
                    throw WidgetConfigurationError.writeFailed
                }
            }
        } catch {
            if previousAccountId != record.configuration.accountId { try? encryptor.reset(envelope) }
            throw error
        }
        if let previousEnvelope,
           previousAccountId != record.configuration.accountId {
            try encryptor.reset(previousEnvelope)
        }
    }

    public func reset() throws {
        try synchronized {
            let envelope = defaults.string(forKey: DevHudWidgetContract.storageKey)
            try encryptor.reset(envelope)
            defaults.removeObject(forKey: DevHudWidgetContract.storageKey)
            guard defaults.object(forKey: DevHudWidgetContract.storageKey) == nil else {
                throw WidgetConfigurationError.writeFailed
            }
        }
    }

    private func synchronized<Value>(_ operation: () throws -> Value) rethrows -> Value {
        lock.lock(); defer { lock.unlock() }
        return try operation()
    }
}

public protocol WidgetRefreshing {
    @discardableResult func refresh() throws -> UInt32
}

public struct WidgetKitRefresher: WidgetRefreshing {
    public init() {}
    @discardableResult public func refresh() throws -> UInt32 {
        WidgetCenter.shared.reloadTimelines(ofKind: DevHudWidgetContract.extensionIdentifier)
        return 0
    }
}

public final class WidgetConfigurationService {
    private let adapter: WidgetSharedDataAdapter
    private let refresher: any WidgetRefreshing
    public init(adapter: WidgetSharedDataAdapter, refresher: any WidgetRefreshing = WidgetKitRefresher()) {
        self.adapter = adapter; self.refresher = refresher
    }
    public func readRawRecord() throws -> String? { try adapter.readRawRecord() }
    @discardableResult public func writeRawRecord(_ raw: String) throws -> UInt32 {
        try adapter.writeRawRecord(raw); return try refresher.refresh()
    }
    @discardableResult public func reset() throws -> UInt32 {
        try adapter.reset(); return try refresher.refresh()
    }
}
