// SPDX-License-Identifier: Apache-2.0
import Darwin
import Foundation

let root = FileManager.default.temporaryDirectory.appendingPathComponent("delidev-widget-fixture-\(UUID().uuidString)", isDirectory: true)
try FileManager.default.createDirectory(at: root, withIntermediateDirectories: false, attributes: [.posixPermissions: 0o700])
defer { try? FileManager.default.removeItem(at: root) }
let store = SnapshotStore(directory: root.appendingPathComponent("state"))
let now = widgetTimestamp("2026-09-30T10:00:00Z")!
let first = "01999ae2-9000-7000-8000-000000000001"
let second = "01999ae2-9000-7000-8000-000000000002"
func publication(_ id: String, _ name: String, _ raw: String) throws -> Publication {
    let summary = try JSONDecoder().decode(Summary.self, from: Data(raw.utf8))
    return Publication(action: .publish, id: id, name: name, summary: summary)
}
func expectFailure(_ body: () throws -> Void) {
    do { try body(); fatalError("Expected fixture rejection") } catch {}
}
let summary = #"{"overview":{"observed_at":"2026-09-30T10:00:00Z","stale":false,"active_sessions":"9007199254740993","pending_interactions":"0","registered_workers":"2","connected_workers":"1"},"usage":{"known_tokens":"90071992547409930000","incomplete":true,"estimates":[{"currency":"USD","known_amount":"0.000000001"},{"currency":"KRW","known_amount":"9007199254740993.000"},{"currency":"EUR","known_amount":null}]},"accounts":{"more":true,"entries":[{"alias":"private@example.test","more":false,"windows":[{"state":"observed","remaining_basis_points":0,"observed_at":"2026-09-30T09:59:00Z","reset_at":null},{"state":"unknown","remaining_basis_points":null,"observed_at":null,"reset_at":null}]}]}}"#
let extraPrivateFields = summary.replacingOccurrences(of: "\"overview\":", with: "\"token\":\"SECRET_SENTINEL\",\"prompt\":\"CONTENT_SENTINEL\",\"endpoint\":\"https://private.example.test\",\"overview\":")
try store.apply(publication(first, "first@example.test", extraPrivateFields), now: now)
try store.apply(publication(second, "Second server", summary.replacingOccurrences(of: "90071992547409930000", with: "0")), now: now)
var states = try store.read().servers
assert(states.count == 2 && states[0].id == first && states[1].id == second)
assert(states[0].name == "Alias hidden" && states[0].summary!.accounts!.entries[0].alias == "Alias hidden")
assert(tokenLabel(states[0].summary!.usage) == "90071992547409930000 known tokens · incomplete")
assert(tokenLabel(states[1].summary!.usage) == "0 known tokens · incomplete")
assert(tokenLabel(nil) == "Today's tokens unavailable")
assert(estimateLabels(states[0].summary!.usage) == ["USD 0.000000001 · estimate", "KRW 9007199254740993.000 · estimate", "EUR unavailable · estimate"])
let quota = states[0].summary!.accounts!.entries[0].windows[0]
assert(quota.label(at: now, snapshotStale: false) == "0.00% remaining · observed")
assert(quota.label(at: now.addingTimeInterval(301), snapshotStale: false) == "0.00% remaining · stale")
assert(!states[0].isStale(at: now) && states[0].isStale(at: now.addingTimeInterval(45)))
assert(states[0].isStale(at: now.addingTimeInterval(-1)))
let unavailableQuota = states[0].summary!.accounts!.entries[0].windows[1]
assert(unavailableQuota.label(at: now, snapshotStale: false) == "Quota unknown")
let resetQuota = Quota(state: .observed, remaining_basis_points: 10000, observed_at: "2026-09-30T09:59:00Z", reset_at: "2026-09-30T10:00:00Z")
assert(resetQuota.label(at: now, snapshotStale: false) == "100.00% remaining · stale")
let failed = #"{"overview":null,"usage":null,"accounts":null}"#
try store.apply(publication(first, "First", failed), now: now.addingTimeInterval(10))
states = try store.read().servers
assert(states[0].state == .stale && states[0].last_successful_at == "2026-09-30T10:00:00Z")
assert(states[0].summary!.usage!.known_tokens == "90071992547409930000")
assert(!states[1].isStale(at: now.addingTimeInterval(10)))
try store.apply(Publication(action: .stop, id: nil, name: nil, summary: nil), now: now.addingTimeInterval(20))
states = try store.read().servers
assert(states.allSatisfy { $0.state == .stale && $0.last_successful_at == "2026-09-30T10:00:00Z" })
let bytes = try Data(contentsOf: store.directory.appendingPathComponent("snapshots-v1.json"))
let serialized = String(decoding: bytes, as: UTF8.self)
assert(!serialized.contains("example.test"))
assert(!serialized.contains("SECRET_SENTINEL") && !serialized.contains("CONTENT_SENTINEL"))
for secret in ["Bearer SECRET_SENTINEL", "sk-SECRET_SENTINEL", "github_pat_SECRET_SENTINEL"] { assert(safeAlias(secret) == "Alias hidden") }
expectFailure { try store.apply(publication(first, "First", summary.replacingOccurrences(of: "90071992547409930000", with: "01")), now: now) }
expectFailure { try store.apply(publication(first, "First", summary.replacingOccurrences(of: "\"incomplete\":true", with: "\"incomplete\":false")), now: now) }
expectFailure { try store.apply(publication(first, "First", summary.replacingOccurrences(of: "2026-09-30T10:00:00Z", with: "invalid")), now: now) }
expectFailure { try store.apply(publication(first, "First", summary.replacingOccurrences(of: "\"EUR\"", with: "\"USD\"")), now: now) }
let unchanged = try Data(contentsOf: store.directory.appendingPathComponent("snapshots-v1.json"))
assert(unchanged == bytes)
try store.apply(Publication(action: .remove, id: first, name: nil, summary: nil), now: now)
let retained = try store.read().servers.map(\.id)
assert(retained == [second])
// Neither a forged ID nor a symlink can select/read another server or file.
expectFailure { try store.apply(Publication(action: .remove, id: "../../foreign", name: nil, summary: nil), now: now) }
let file = store.directory.appendingPathComponent("snapshots-v1.json")
try FileManager.default.removeItem(at: file)
try FileManager.default.createSymbolicLink(at: file, withDestinationURL: root.appendingPathComponent("sentinel"))
expectFailure { _ = try store.read() }
expectFailure { try store.apply(publication(second, "Second", summary), now: now) }
try FileManager.default.removeItem(at: file)
try Data("{broken".utf8).write(to: file)
chmod(file.path, 0o600)
expectFailure { _ = try store.read() }
try FileManager.default.removeItem(at: file)
try store.apply(publication(second, "Second", summary), now: now)
var info = stat()
assert(stat(file.path, &info) == 0 && info.st_mode & 0o777 == 0o600)
let linked = root.appendingPathComponent("linked")
assert(link(file.path, linked.path) == 0)
expectFailure { _ = try store.read() }
expectFailure { try store.apply(publication(second, "Second", summary), now: now) }
try FileManager.default.removeItem(at: linked)
chmod(file.path, 0o644)
expectFailure { _ = try store.read() }
chmod(file.path, 0o600)
let oversized = Data(repeating: 65, count: 2 * 1024 * 1024 + 1)
try oversized.write(to: file)
expectFailure { _ = try store.read() }
print("Widget fixture checks passed: exact values, currencies, isolation, stale/closure, masking, corruption and private storage")
