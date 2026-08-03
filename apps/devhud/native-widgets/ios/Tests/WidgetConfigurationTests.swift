import DevHudWidgetCore
import XCTest

final class WidgetConfigurationTests: XCTestCase {
    private var suiteName: String!
    private var defaults: UserDefaults!

    override func setUpWithError() throws {
        suiteName = "devhud.widget.tests.\(UUID().uuidString)"
        defaults = try XCTUnwrap(UserDefaults(suiteName: suiteName))
    }

    override func tearDownWithError() throws {
        defaults.removePersistentDomain(forName: suiteName)
        defaults = nil
        suiteName = nil
    }

    func testFixtureRoundTripsThroughTypedSharedAdapter() throws {
        let raw = try fixture()
        let adapter = WidgetSharedDataAdapter(
            defaults: defaults,
            encryptor: IdentityWidgetRecordEncryptor()
        )
        try adapter.writeRawRecord(raw)

        XCTAssertEqual(try adapter.readRawRecord(), raw)
        let widget = try XCTUnwrap(adapter.readRecord().configuration.widgets.first)
        XCTAssertEqual(widget.family, .appleMedium)
        XCTAssertEqual(widget.snapshot.matchingCount, 2)
        XCTAssertTrue(widget.snapshot.offline)
    }

    func testCountsOnlyRejectsRepositoryAndTitleDetails() throws {
        let raw = try fixture().replacingOccurrences(
            of: #""privacy":"repository-and-titles""#,
            with: #""privacy":"counts-only""#
        )
        XCTAssertThrowsError(try WidgetConfigurationCodec.decode(Data(raw.utf8))) {
            XCTAssertEqual($0 as? WidgetConfigurationError, .incompatible)
        }
    }

    func testFamiliesFreshnessAndOneSelectedViewAreStrict() throws {
        let record = try WidgetConfigurationCodec.decode(Data(try fixture().utf8))
        XCTAssertEqual(record.configuration.widgets.count, 1)
        XCTAssertEqual(record.configuration.widgets[0].viewId, "018f0000-0000-7000-8000-000000000003")
        XCTAssertEqual(record.configuration.widgets[0].snapshot.freshness, .stale)
    }

    func testActionsOpenOrRefreshAndNeverEncodeMutation() throws {
        let view = "018f0000-0000-7000-8000-000000000003"
        let actions = [
            DeckWidgetAction.openView(viewId: view),
            .openPullRequest(viewId: view, owner: "acme", repository: "widgets", number: 42),
            .refresh(viewId: view),
        ]
        let urls = try actions.map { try XCTUnwrap($0.url?.absoluteString) }
        XCTAssertTrue(urls.allSatisfy { $0.contains("https://deli.dev/devhud/deck/open?") })
        XCTAssertFalse(urls.joined().contains("merge"))
        XCTAssertFalse(urls.joined().contains("close"))
        XCTAssertFalse(urls.joined().contains("mutation"))
    }

    func testNotificationPayloadIsOpaqueAndTextDefaultsExactly() {
        XCTAssertEqual(
            DeckNotificationPolicy.payloadEventId(["eventId": "opaque_event_123456"]),
            "opaque_event_123456"
        )
        XCTAssertNil(DeckNotificationPolicy.payloadEventId([
            "eventId": "opaque_event_123456", "title": "private title",
        ]))
        XCTAssertEqual(
            DeckNotificationPolicy.text(detailedText: "private", localDetailEnabled: false),
            "Deck view updated"
        )
    }

    func testResetIsIsolatedAndIdempotent() throws {
        let adapter = WidgetSharedDataAdapter(
            defaults: defaults,
            encryptor: IdentityWidgetRecordEncryptor()
        )
        defaults.set("preserved", forKey: "devhud.settings.v1")
        try adapter.writeRawRecord(try fixture())
        try adapter.reset()
        try adapter.reset()
        XCTAssertNil(try adapter.readRawRecord())
        XCTAssertEqual(defaults.string(forKey: "devhud.settings.v1"), "preserved")
    }

    func testCorruptAndFutureRecordsAreRejectedWithoutOverwrite() throws {
        let adapter = WidgetSharedDataAdapter(
            defaults: defaults,
            encryptor: IdentityWidgetRecordEncryptor()
        )
        let valid = try fixture()
        try adapter.writeRawRecord(valid)
        XCTAssertThrowsError(try adapter.writeRawRecord("{not-json}"))
        XCTAssertThrowsError(try adapter.writeRawRecord(valid.replacingOccurrences(of: #""version":1"#, with: #""version":2"#))) {
            XCTAssertEqual($0 as? WidgetConfigurationError, .futureVersion)
        }
        XCTAssertEqual(try adapter.readRawRecord(), valid)
    }

    func testLiveCiphertextDoesNotContainSnapshotDetails() throws {
        let directory = FileManager.default.temporaryDirectory
            .appendingPathComponent("devhud-widget-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: directory) }
        let adapter = WidgetSharedDataAdapter(
            defaults: defaults,
            encryptor: ProtectedWidgetRecordEncryptor(container: directory)
        )
        try adapter.writeRawRecord(try fixture())
        let stored = try XCTUnwrap(defaults.string(forKey: DevHudWidgetContract.storageKey))
        XCTAssertFalse(stored.contains("Keep snapshot minimal"))
        XCTAssertFalse(stored.contains("acme"))
        XCTAssertEqual(try adapter.readRawRecord(), try fixture())
        try adapter.reset()
    }

    func testRefreshFailuresPropagateAfterValidStateIsStored() throws {
        struct FailingRefresher: WidgetRefreshing {
            func refresh() throws -> UInt32 { throw WidgetConfigurationError.refreshFailed }
        }
        let adapter = WidgetSharedDataAdapter(
            defaults: defaults,
            encryptor: IdentityWidgetRecordEncryptor()
        )
        let service = WidgetConfigurationService(adapter: adapter, refresher: FailingRefresher())
        let raw = try fixture()
        XCTAssertThrowsError(try service.writeRawRecord(raw)) {
            XCTAssertEqual($0 as? WidgetConfigurationError, .refreshFailed)
        }
        XCTAssertEqual(try adapter.readRawRecord(), raw)
    }

    private func fixture() throws -> String {
        let url = try XCTUnwrap(Bundle(for: Self.self).url(
            forResource: "widget-configuration.v1", withExtension: "json"
        ))
        return try String(contentsOf: url, encoding: .utf8)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
}
