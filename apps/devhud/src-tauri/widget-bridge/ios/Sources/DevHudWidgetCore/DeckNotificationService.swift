import Foundation
import UserNotifications

public final class DeckNotificationService: Sendable {
    public init() {}

    public func authorizationEnabled() async -> Bool {
        let status = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
        switch status {
        case .authorized, .provisional, .ephemeral:
            return true
        default:
            return false
        }
    }

    public func requestAuthorization() async throws -> Bool {
        try await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .badge, .sound])
    }

    /**
     The request uses the default interruption level and never a critical or
     time-sensitive entitlement, so Focus and Do Not Disturb remain authoritative.
     Push/userInfo is restricted to one opaque event identifier.
     */
    public func publishOpaqueEvent(
        _ userInfo: [AnyHashable: Any],
        detailedText: String? = nil,
        localDetailEnabled: Bool = false
    ) async throws -> Bool {
        guard let eventId = DeckNotificationPolicy.payloadEventId(userInfo) else { return false }
        let content = UNMutableNotificationContent()
        content.title = "Deck"
        content.body = DeckNotificationPolicy.text(
            detailedText: detailedText,
            localDetailEnabled: localDetailEnabled
        )
        content.sound = .default
        content.interruptionLevel = .active
        content.userInfo = ["eventId": eventId]
        if let url = DeckWidgetAction.resolveEvent(eventId: eventId).url {
            content.userInfo["url"] = url.absoluteString
        }
        try await UNUserNotificationCenter.current().add(
            UNNotificationRequest(identifier: eventId, content: content, trigger: nil)
        )
        return true
    }
}
