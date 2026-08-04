import DevHudWidgetCore
import Foundation
import SwiftRs
import Tauri

private final class WriteConfigurationArgs: Decodable {
    let record: String
}

private struct ReadConfigurationResponse: Encodable {
    let record: String?
}

private struct WidgetRefreshResponse: Encodable {
    let refreshedWidgetCount: UInt32
}

private struct NotificationAuthorizationResponse: Encodable {
    let authorized: Bool
}

private struct PrepareResetResponse: Encodable {
    let prepared: Bool
}

final class DevHudWidgetPlugin: Plugin {
    private lazy var service: WidgetConfigurationService? = {
        guard let adapter = try? WidgetSharedDataAdapter.live() else {
            return nil
        }
        return WidgetConfigurationService(adapter: adapter)
    }()

    @objc func readConfiguration(_ invoke: Invoke) {
        withService(invoke) { service in
            invoke.resolve(
                ReadConfigurationResponse(record: try service.readRawRecord())
            )
        }
    }

    @objc func writeConfiguration(_ invoke: Invoke) {
        withService(invoke) { service in
            let arguments = try invoke.parseArgs(WriteConfigurationArgs.self)
            let count = try service.writeRawRecord(arguments.record)
            invoke.resolve(WidgetRefreshResponse(refreshedWidgetCount: count))
        }
    }

    @objc func notificationAuthorizationStatus(_ invoke: Invoke) {
        Task {
            let authorized = await DeckNotificationService().authorizationEnabled()
            invoke.resolve(NotificationAuthorizationResponse(authorized: authorized))
        }
    }

    @objc func prepareReset(_ invoke: Invoke) {
        withService(invoke) { _ in
            // Resolving the App Group adapter is the only prerequisite. The
            // retained value is untouched until resetConfiguration.
            invoke.resolve(PrepareResetResponse(prepared: true))
        }
    }

    @objc func resetConfiguration(_ invoke: Invoke) {
        withService(invoke) { service in
            let count = try service.reset()
            invoke.resolve(WidgetRefreshResponse(refreshedWidgetCount: count))
        }
    }

    private func withService(
        _ invoke: Invoke,
        operation: (WidgetConfigurationService) throws -> Void
    ) {
        guard let service else {
            invoke.reject(
                "The DevHud widget operation failed.",
                code: WidgetConfigurationError.sharedStoreUnavailable.rawValue
            )
            return
        }
        do {
            try operation(service)
        } catch let error as WidgetConfigurationError {
            invoke.reject(
                "The DevHud widget operation failed.",
                code: error.rawValue
            )
        } catch {
            invoke.reject(
                "The DevHud widget operation failed.",
                code: WidgetConfigurationError.sharedStoreUnavailable.rawValue
            )
        }
    }
}

@_cdecl("init_plugin_devhud_widget")
func initPlugin() -> Plugin {
    DevHudWidgetPlugin()
}
