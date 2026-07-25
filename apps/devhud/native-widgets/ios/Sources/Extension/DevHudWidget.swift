import DevHudWidgetCore
import SwiftUI
import WidgetKit

private struct DevHudWidgetEntry: TimelineEntry {
    let date: Date
    let configuredSlotCount: Int
}

private struct DevHudWidgetProvider: TimelineProvider {
    func placeholder(in context: Context) -> DevHudWidgetEntry {
        DevHudWidgetEntry(date: .now, configuredSlotCount: 0)
    }

    func getSnapshot(
        in context: Context,
        completion: @escaping (DevHudWidgetEntry) -> Void
    ) {
        completion(entry())
    }

    func getTimeline(
        in context: Context,
        completion: @escaping (Timeline<DevHudWidgetEntry>) -> Void
    ) {
        completion(Timeline(entries: [entry()], policy: .never))
    }

    private func entry() -> DevHudWidgetEntry {
        let slotCount = (
            try? WidgetSharedDataAdapter.live().readRecord().configuration.slots.count
        ) ?? 0
        return DevHudWidgetEntry(date: .now, configuredSlotCount: slotCount)
    }
}

private struct DevHudWidgetView: View {
    let entry: DevHudWidgetEntry

    var body: some View {
        Text("No widget configured")
        .containerBackground(.background, for: .widget)
    }
}

@main
struct DevHudWidget: Widget {
    var body: some SwiftUI.WidgetConfiguration {
        StaticConfiguration(
            kind: DevHudWidgetContract.extensionIdentifier,
            provider: DevHudWidgetProvider()
        ) { entry in
            DevHudWidgetView(entry: entry)
        }
        .configurationDisplayName("DevHud Widget Foundation")
        .description("Build-only widget source. It is not embedded in DevHud 0.1.0.")
        .supportedFamilies([.systemSmall])
    }
}
