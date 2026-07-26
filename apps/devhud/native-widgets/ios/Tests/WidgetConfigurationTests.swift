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
        let fixtureURL = try XCTUnwrap(
            Bundle(for: Self.self).url(
                forResource: "widget-configuration.v1",
                withExtension: "json"
            )
        )
        let raw = try String(contentsOf: fixtureURL, encoding: .utf8)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        let adapter = WidgetSharedDataAdapter(defaults: defaults)

        try adapter.writeRawRecord(raw)

        XCTAssertEqual(try adapter.readRawRecord(), raw)
        XCTAssertEqual(
            try adapter.readRecord().configuration.slots.first?.toolId.rawValue,
            "fixture-diagnostics"
        )
    }

    func testResetIsIsolatedToWidgetConfiguration() throws {
        let adapter = WidgetSharedDataAdapter(defaults: defaults)
        defaults.set("preserved", forKey: "devhud.settings.v1")
        try adapter.writeRawRecord(
            #"{"version":1,"configuration":{"slots":[]}}"#
        )

        try adapter.reset()
        try adapter.reset()

        XCTAssertNil(try adapter.readRawRecord())
        XCTAssertEqual(defaults.string(forKey: "devhud.settings.v1"), "preserved")
    }

    func testCorruptAndFutureRecordsAreRejectedWithoutOverwrite() throws {
        let adapter = WidgetSharedDataAdapter(defaults: defaults)
        try adapter.writeRawRecord(
            #"{"version":1,"configuration":{"slots":[]}}"#
        )

        XCTAssertThrowsError(try adapter.writeRawRecord("{not-json}")) {
            XCTAssertEqual($0 as? WidgetConfigurationError, .corrupt)
        }
        XCTAssertThrowsError(
            try adapter.writeRawRecord(
                #"{"version":2,"configuration":{"slots":[]}}"#
            )
        ) {
            XCTAssertEqual($0 as? WidgetConfigurationError, .futureVersion)
        }
        XCTAssertEqual(
            try adapter.readRawRecord(),
            #"{"version":1,"configuration":{"slots":[]}}"#
        )
    }

    func testRefreshFailuresPropagateAfterValidStateIsStored() throws {
        struct FailingRefresher: WidgetRefreshing {
            func refresh() throws -> UInt32 {
                throw WidgetConfigurationError.refreshFailed
            }
        }
        let adapter = WidgetSharedDataAdapter(defaults: defaults)
        let service = WidgetConfigurationService(
            adapter: adapter,
            refresher: FailingRefresher()
        )
        let raw = #"{"version":1,"configuration":{"slots":[]}}"#

        XCTAssertThrowsError(try service.writeRawRecord(raw)) {
            XCTAssertEqual($0 as? WidgetConfigurationError, .refreshFailed)
        }
        XCTAssertEqual(try adapter.readRawRecord(), raw)
    }
}
