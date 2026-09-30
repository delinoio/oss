// SPDX-License-Identifier: Apache-2.0
import Foundation
import SwiftUI
import WidgetKit

struct StatusView: View {
    let entry: StatusEntry
    @Environment(\.widgetFamily) private var family
    var body: some View { StatusContent(entry: entry, family: family) }
}
struct StatusContent: View {
    let entry: StatusEntry
    let family: WidgetFamily
    private var now: Date { max(entry.date, Date()) }
    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            HStack { Text("DeliDev").font(.headline); Spacer(); Text(entry.server.map { $0.last_successful_at == nil ? "Unavailable" : $0.isStale(at: now) ? "Stale" : "Observed" } ?? "Unavailable").font(.caption2) }
            if let server = entry.server {
                Text(server.name).font(.subheadline).lineLimit(1).privacySensitive()
                HStack(alignment: .top, spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        if let overview = server.summary?.overview {
                            Text("\(overview.active_sessions) active · \(overview.pending_interactions) waiting").font(.caption).lineLimit(2).privacySensitive()
                            Text("Workers \(overview.connected_workers)/\(overview.registered_workers)").font(.caption).privacySensitive()
                        } else { Text("Status unavailable").font(.caption) }
                        if let tokens = server.summary?.usage?.known_tokens {
                            Text(tokens).font(.caption).monospacedDigit().lineLimit(1).privacySensitive()
                            Text("Known tokens · incomplete").font(.caption2)
                        } else { Text("Today's tokens unavailable").font(.caption).lineLimit(2) }

                    }.frame(maxWidth: .infinity, alignment: .leading)
                    if family != .systemSmall {
                        VStack(alignment: .leading, spacing: 2) {
                            if let accounts = server.summary?.accounts {
                                ForEach(Array(accounts.entries.prefix(family == .systemLarge ? 2 : 1).enumerated()), id: \.offset) { _, account in
                                    Text(account.alias).font(.caption).lineLimit(1).privacySensitive()
                                    if let quota = account.windows.first {
                                        Text(quota.label(at: now, snapshotStale: server.isStale(at: now))).font(.caption2).lineLimit(2).privacySensitive()
                                        if family == .systemLarge {
                                            if let observed = quota.observed_at.flatMap(widgetTimestamp) { Text("Observed \(observed.formatted())").font(.caption2) }
                                            if let reset = quota.reset_at.flatMap(widgetTimestamp) { Text("Reset \(reset.formatted())").font(.caption2) }
                                        }
                                    } else { Text("Quota unavailable").font(.caption2) }
                                    if family == .systemLarge && (account.more || account.windows.count > 1) { Text("More windows in DeliDev").font(.caption2) }
                                }
                                if accounts.entries.isEmpty { Text("Quota unavailable").font(.caption2) }
                                if accounts.more || accounts.entries.count > (family == .systemLarge ? 2 : 1) || (family == .systemMedium && accounts.entries.contains { $0.more || $0.windows.count > 1 }) { Text("More quotas in DeliDev").font(.caption2) }
                            } else { Text("Quota unavailable").font(.caption2) }
                            if !(server.summary?.usage?.estimates.isEmpty ?? true) { Text("Token-price estimates").font(.caption2) }
                            ForEach(Array((server.summary?.usage?.estimates ?? []).prefix(2).enumerated()), id: \.offset) { _, estimate in Text("\(estimate.currency) \(estimate.known_amount ?? "unavailable")").font(.caption2).lineLimit(1).privacySensitive() }
                            if (server.summary?.usage?.estimates.count ?? 0) > 2 { Text("More currencies in DeliDev").font(.caption2) }
                            Text("Actual cost unavailable").font(.caption2)
                        }.frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                Spacer(minLength: 0)
                if let success = server.last_successful_at.flatMap(widgetTimestamp) {
                    Text("Last success \(success.formatted(date: .abbreviated, time: .shortened))").font(.caption2).lineLimit(2)
                } else { Text("No successful refresh").font(.caption2) }
            } else {
                Text(entry.storageFailed ? "Snapshot unavailable" : "Select a saved server").font(.subheadline)
                Text("Open its DeliDev window to refresh. Updates follow macOS scheduling.").font(.caption)
            }
        }.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}
