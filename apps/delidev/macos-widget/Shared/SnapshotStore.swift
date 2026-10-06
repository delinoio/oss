// SPDX-License-Identifier: Apache-2.0
import Darwin
import Foundation

// The group is obtained from the OS entitlement API, never a renderer path.
// Tests inject an isolated owner-private directory and do not resolve real state.
struct SnapshotStore {
    let directory: URL
    static func shared() throws -> SnapshotStore {
        guard let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: widgetGroup) else { throw SnapshotFailure.storage }
        return SnapshotStore(directory: container.appendingPathComponent("StatusWidget", isDirectory: true))
    }
    private let limit = 2 * 1024 * 1024
    private let file = "snapshots-v1.json"

    private func checked(_ fd: Int32, directory: Bool = false) throws {
        var info = stat()
        guard fd >= 0, fstat(fd, &info) == 0, info.st_uid == getuid(),
              info.st_mode & 0o077 == 0,
              info.st_mode & S_IFMT == (directory ? S_IFDIR : S_IFREG),
              directory || info.st_nlink == 1 else { throw SnapshotFailure.storage }
    }
    private func opened(create: Bool) throws -> Int32 {
        if create && mkdir(directory.path, 0o700) != 0 && errno != EEXIST { throw SnapshotFailure.storage }
        let fd = open(directory.path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC)
        do { try checked(fd, directory: true); return fd } catch { if fd >= 0 { close(fd) }; throw error }
    }
    private func read(at dir: Int32) throws -> SnapshotFile {
        let fd = openat(dir, file, O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC)
        if fd < 0 && errno == ENOENT { return SnapshotFile(version: 1, servers: []) }
        defer { if fd >= 0 { close(fd) } }
        try checked(fd)
        var info = stat()
        guard fstat(fd, &info) == 0, info.st_size > 0, info.st_size <= limit else { throw SnapshotFailure.storage }
        var bytes = Data(count: Int(info.st_size))
        let count = bytes.withUnsafeMutableBytes { buffer -> Int in
            var position = 0
            while position < buffer.count {
                let result = Darwin.read(fd, buffer.baseAddress!.advanced(by: position), buffer.count - position)
                if result < 0 && errno == EINTR { continue }
                if result <= 0 { return -1 }
                position += result
            }
            return position
        }
        guard count == bytes.count else { throw SnapshotFailure.storage }
        var value = try JSONDecoder().decode(SnapshotFile.self, from: bytes)
        try value.validate()
        return value
    }
    func read() throws -> SnapshotFile {
        let dir = try opened(create: false)
        defer { close(dir) }
        return try read(at: dir)
    }

    private func readLanguage(at dir: Int32) throws -> WidgetLanguageDocument {
        let fd = openat(dir, "language-v1.json", O_RDONLY | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC)
        if fd < 0 && errno == ENOENT { return WidgetLanguageDocument(version: 1, language: .system) }
        defer { if fd >= 0 { close(fd) } }
        try checked(fd)
        var info = stat()
        guard fstat(fd, &info) == 0, info.st_size > 0, info.st_size <= 4096 else { throw SnapshotFailure.storage }
        var bytes = Data(count: Int(info.st_size))
        let count = bytes.withUnsafeMutableBytes { buffer -> Int in
            var position = 0
            while position < buffer.count {
                let size = Darwin.read(fd, buffer.baseAddress!.advanced(by: position), buffer.count - position)
                if size < 0 && errno == EINTR { continue }
                if size <= 0 { return -1 }
                position += size
            }
            return position
        }
        guard count == bytes.count,
              let object = try JSONSerialization.jsonObject(with: bytes) as? [String: Any],
              Set(object.keys) == Set(["version", "language"]) else { throw SnapshotFailure.invalid }
        let value = try JSONDecoder().decode(WidgetLanguageDocument.self, from: bytes)
        try value.validate()
        return value
    }

    func readLanguage() throws -> LanguagePreference {
        let dir = try opened(create: false)
        defer { close(dir) }
        return try readLanguage(at: dir).language
    }

    func setLanguage(_ value: WidgetLanguageDocument) throws {
        try value.validate()
        let dir = try opened(create: true)
        defer { close(dir) }
        let lock = openat(dir, "writer.lock", O_RDWR | O_CREAT | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC, 0o600)
        defer { if lock >= 0 { close(lock) } }
        try checked(lock)
        guard flock(lock, LOCK_EX | LOCK_NB) == 0 else { throw SnapshotFailure.storage }
        defer { flock(lock, LOCK_UN) }
        let current = try readLanguage(at: dir)
        if current.language == value.language { return }
        let bytes = try JSONEncoder().encode(value)
        let staging = ".language-\(UUID().uuidString)"
        let fd = openat(dir, staging, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw SnapshotFailure.storage }
        defer { close(fd); unlinkat(dir, staging, 0) }
        let written = bytes.withUnsafeBytes { buffer -> Bool in
            var position = 0
            while position < buffer.count {
                let size = Darwin.write(fd, buffer.baseAddress!.advanced(by: position), buffer.count - position)
                if size < 0 && errno == EINTR { continue }
                if size <= 0 { return false }
                position += size
            }
            return true
        }
        guard written, fsync(fd) == 0, renameat(dir, staging, dir, "language-v1.json") == 0, fsync(dir) == 0 else { throw SnapshotFailure.storage }
    }
    func apply(_ request: Publication, now: Date = Date()) throws {
        let dir = try opened(create: true)
        defer { close(dir) }
        let lock = openat(dir, "writer.lock", O_RDWR | O_CREAT | O_NOFOLLOW | O_NONBLOCK | O_CLOEXEC, 0o600)
        defer { if lock >= 0 { close(lock) } }
        try checked(lock)
        // One nonblocking writer claim. The host serializes its publications;
        // another app process cannot race a snapshot read/modify/replace.
        guard flock(lock, LOCK_EX | LOCK_NB) == 0 else { throw SnapshotFailure.storage }
        defer { flock(lock, LOCK_UN) }
        var value = try read(at: dir)
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        let attempted = formatter.string(from: now)
        switch request.action {
        case .stop:
            guard request.id == nil, request.name == nil, request.summary == nil else { throw SnapshotFailure.invalid }
            for index in value.servers.indices { value.servers[index].state = .stale }
        case .remove:
            guard let id = request.id, canonicalServer(id), request.name == nil, request.summary == nil else { throw SnapshotFailure.invalid }
            value.servers.removeAll { $0.id == id }
        case .publish:
            guard let id = request.id, canonicalServer(id), let name = request.name, !name.isEmpty, name.utf8.count <= 256,
                  var summary = request.summary else { throw SnapshotFailure.invalid }
            try summary.validateAndMask()
            let index = value.servers.firstIndex { $0.id == id }
            var server = index.map { value.servers[$0] } ?? ServerSnapshot(id: id, name: safeAlias(name), state: .unavailable, last_successful_at: nil, last_attempted_at: attempted, summary: nil)
            server.name = safeAlias(name)
            server.last_attempted_at = attempted
            if let overview = summary.overview, !overview.stale,
               let observation = widgetTimestamp(overview.observed_at), observation <= now,
               now.timeIntervalSince(observation) < widgetStaleAfter {
                server.state = .observed
                server.last_successful_at = overview.observed_at
                server.summary = summary
            } else {
                server.state = server.last_successful_at == nil ? .unavailable : .stale
            }
            if let index { value.servers[index] = server } else {
                guard value.servers.count < 32 else { throw SnapshotFailure.invalid }
                value.servers.append(server)
            }
        }
        try value.validate()
        let bytes = try JSONEncoder().encode(value)
        guard bytes.count <= limit else { throw SnapshotFailure.invalid }
        let staging = ".snapshot-\(UUID().uuidString)"
        let fd = openat(dir, staging, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW | O_CLOEXEC, 0o600)
        guard fd >= 0 else { throw SnapshotFailure.storage }
        defer { close(fd); unlinkat(dir, staging, 0) }
        let written = bytes.withUnsafeBytes { buffer -> Bool in
            var position = 0
            while position < buffer.count {
                let count = Darwin.write(fd, buffer.baseAddress!.advanced(by: position), buffer.count - position)
                if count < 0 && errno == EINTR { continue }
                if count <= 0 { return false }
                position += count
            }
            return true
        }
        guard written, fsync(fd) == 0, renameat(dir, staging, dir, file) == 0, fsync(dir) == 0 else { throw SnapshotFailure.storage }
    }
}
