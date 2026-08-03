import AppIntents
import DevHudWidgetCore
import SwiftUI
import WidgetKit

struct DeckWidgetEntity: AppEntity, Identifiable {
    static let typeDisplayRepresentation = TypeDisplayRepresentation(name: "Deck view")
    static let defaultQuery = DeckWidgetEntityQuery()

    let id: String
    let viewId: String
    let privacy: DeckWidgetPrivacy
    let displayRepresentation: DisplayRepresentation

    init(widget: DeckWidgetInstance) {
        id = widget.widgetId
        viewId = widget.viewId
        privacy = widget.privacy
        displayRepresentation = DisplayRepresentation(
            title: "Deck view",
            subtitle: widget.privacy == .countsOnly ? "Counts only" : "Repository and PR titles"
        )
    }
}

struct DeckWidgetEntityQuery: EntityQuery {
    func entities(for identifiers: [String]) async throws -> [DeckWidgetEntity] {
        entities().filter { identifiers.contains($0.id) }
    }

    func suggestedEntities() async throws -> [DeckWidgetEntity] { entities() }

    private func entities() -> [DeckWidgetEntity] {
        let record = try? WidgetSharedDataAdapter.live().readRecord()
        return (record?.configuration.widgets ?? []).map(DeckWidgetEntity.init)
    }
}

struct ConfigureDeckWidgetIntent: WidgetConfigurationIntent {
    static let title: LocalizedStringResource = "Choose a Deck view"
    static let description = IntentDescription(
        "Each widget shows one selected view. New widget configurations show counts only."
    )

    @Parameter(title: "Deck view")
    var widget: DeckWidgetEntity?
}

private struct DevHudWidgetEntry: TimelineEntry {
    let date: Date
    let widget: DeckWidgetInstance?
}

private struct DevHudWidgetProvider: AppIntentTimelineProvider {
    func placeholder(in context: Context) -> DevHudWidgetEntry {
        DevHudWidgetEntry(date: .now, widget: nil)
    }

    func snapshot(for configuration: ConfigureDeckWidgetIntent, in context: Context) async -> DevHudWidgetEntry {
        entry(configuration)
    }

    func timeline(for configuration: ConfigureDeckWidgetIntent, in context: Context) async -> Timeline<DevHudWidgetEntry> {
        // WidgetKit decides execution time. The client requests reloads after
        // its normal coalesced/billed refresh path completes.
        Timeline(entries: [entry(configuration)], policy: .never)
    }

    private func entry(_ configuration: ConfigureDeckWidgetIntent) -> DevHudWidgetEntry {
        let widgets = (try? WidgetSharedDataAdapter.live().readRecord().configuration.widgets) ?? []
        let selected = widgets.first { $0.widgetId == configuration.widget?.id }
        return DevHudWidgetEntry(date: .now, widget: selected)
    }
}

private struct DevHudWidgetView: View {
    @Environment(\.widgetFamily) private var family
    let entry: DevHudWidgetEntry

    var body: some View {
        Group {
            if let widget = entry.widget {
                configured(widget)
            } else {
                ContentUnavailableView("Select a Deck view", systemImage: "rectangle.stack")
                    .accessibilityLabel("Select one Deck view for this widget")
            }
        }
        .containerBackground(.background, for: .widget)
    }

    @ViewBuilder
    private func configured(_ widget: DeckWidgetInstance) -> some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack {
                Text("Deck").font(.headline)
                Spacer()
                Text(status(widget.snapshot)).font(.caption).foregroundStyle(.secondary)
            }
            Link(destination: DeckWidgetAction.openView(viewId: widget.viewId).url!) {
                Text("\(widget.snapshot.matchingCount)")
                    .font(.system(size: family == .systemSmall ? 44 : 32, weight: .bold, design: .rounded))
                Text("matching pull requests")
                    .font(.caption)
            }
            .accessibilityLabel("\(widget.snapshot.matchingCount) matching pull requests. \(status(widget.snapshot)).")

            if family != .systemSmall && widget.privacy == .repositoryAndTitles {
                ForEach(Array(widget.snapshot.pullRequests.prefix(family == .systemLarge ? 5 : 2)), id: \.number) { pullRequest in
                    Link(destination: DeckWidgetAction.openPullRequest(
                        viewId: widget.viewId,
                        owner: pullRequest.repositoryOwner,
                        repository: pullRequest.repositoryName,
                        number: pullRequest.number
                    ).url!) {
                        VStack(alignment: .leading, spacing: 1) {
                            Text("\(pullRequest.repositoryOwner)/\(pullRequest.repositoryName) #\(pullRequest.number)")
                                .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
                            Text(pullRequest.title).font(.caption).lineLimit(1)
                        }
                    }
                    .accessibilityLabel("Open pull request \(pullRequest.number), \(pullRequest.title), in \(pullRequest.repositoryOwner) slash \(pullRequest.repositoryName)")
                }
            }

            Spacer(minLength: 0)
            Link("Request refresh", destination: DeckWidgetAction.refresh(viewId: widget.viewId).url!)
                .font(.caption2)
                .accessibilityHint("Opens DevHud and requests a best effort refresh through Deck")
        }
        .padding()
        .widgetURL(DeckWidgetAction.openView(viewId: widget.viewId).url)
    }

    private func status(_ snapshot: WidgetSnapshot) -> String {
        if snapshot.offline { return "Offline" }
        switch snapshot.freshness {
        case .fresh: return "Updated"
        case .stale: return "Stale"
        case .offline: return "Offline"
        case .disconnected: return "Disconnected"
        case .neverRefreshed: return "Not refreshed"
        }
    }
}

@main
struct DevHudWidget: Widget {
    var body: some SwiftUI.WidgetConfiguration {
        AppIntentConfiguration(
            kind: DevHudWidgetContract.extensionIdentifier,
            intent: ConfigureDeckWidgetIntent.self,
            provider: DevHudWidgetProvider()
        ) { entry in
            DevHudWidgetView(entry: entry)
        }
        .configurationDisplayName("Deck view")
        .description("One Deck view with privacy-controlled pull request details.")
        .supportedFamilies([.systemSmall, .systemMedium, .systemLarge])
    }
}
