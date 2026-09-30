// SPDX-License-Identifier: Apache-2.0
import Foundation
import Intents
import SwiftUI
import WidgetKit

struct StatusProvider: IntentTimelineProvider {
    func placeholder(in context: Context) -> StatusEntry { StatusEntry(date: Date(), server: nil, storageFailed: false) }
    func getSnapshot(for configuration: SelectServerIntent, in context: Context, completion: @escaping (StatusEntry) -> Void) {
        completion(entry(configuration, at: Date()))
    }
    private func entry(_ configuration: SelectServerIntent, at date: Date) -> StatusEntry {
        guard let id = configuration.server?.identifier, canonicalServer(id) else { return StatusEntry(date: date, server: nil, storageFailed: false) }
        do { return StatusEntry(date: date, server: try SnapshotStore.shared().read().servers.first { $0.id == id }, storageFailed: false) }
        catch { return StatusEntry(date: date, server: nil, storageFailed: true) }
    }
    func getTimeline(for configuration: SelectServerIntent, in context: Context, completion: @escaping (Timeline<StatusEntry>) -> Void) {
        let now = Date()
        let current = entry(configuration, at: now)
        var entries = [current]
        if let success = current.server?.last_successful_at.flatMap(widgetTimestamp) {
            let expires = success.addingTimeInterval(widgetStaleAfter)
            if expires > now { entries.append(StatusEntry(date: expires, server: current.server, storageFailed: current.storageFailed)) }
        }
        // OS scheduling is best effort; this reads the same frozen observation.
        // A timeline entry never advances the successful-refresh timestamp.
        completion(Timeline(entries: entries, policy: .after(now.addingTimeInterval(300))))
    }
}
@main
struct DeliDevStatusWidget: Widget {
    var body: some WidgetConfiguration {
        IntentConfiguration(kind: widgetKind, intent: SelectServerIntent.self, provider: StatusProvider()) { entry in
            if #available(macOS 14, *) {
                StatusView(entry: entry).containerBackground(for: .widget) { Color.clear }
            } else { StatusView(entry: entry).padding() }
        }
        .configurationDisplayName("DeliDev status")
        .description("Read-only status from one explicitly selected saved server. Open DeliDev to refresh.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}
