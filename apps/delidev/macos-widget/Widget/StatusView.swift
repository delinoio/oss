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
    private func copy(_ key: WidgetMessage, _ values: [String: String] = [:]) -> String { widgetCopy(key, entry.language, values) }
    private func date(_ value: Date) -> String { widgetDate(value, entry.language) }
    var body: some View {
        VStack(alignment: .leading, spacing: 3) {
            if entry.languageFailed { Text(copy(.widgetLanguageUnavailable)).font(.caption2) }
            HStack { Text("DeliDev").font(.headline); Spacer(); Text(entry.server.map { $0.last_successful_at == nil ? copy(.widgetUnavailable) : $0.isStale(at: now) ? copy(.widgetStale) : copy(.widgetObserved) } ?? copy(.widgetUnavailable)).font(.caption2) }
            if let server = entry.server {
                Text(server.name_hidden == true ? copy(.widgetAliasHidden) : server.name).font(.subheadline).lineLimit(1).privacySensitive()
                HStack(alignment: .top, spacing: 12) {
                    VStack(alignment: .leading, spacing: 2) {
                        if let overview = server.summary?.overview {
                            Text(copy(.widgetStatus, ["active": widgetNumber(overview.active_sessions), "pending": widgetNumber(overview.pending_interactions)])).font(.caption).lineLimit(2).privacySensitive()
                            Text(copy(.widgetWorkers, ["connected": widgetNumber(overview.connected_workers), "registered": widgetNumber(overview.registered_workers)])).font(.caption).privacySensitive()
                        } else { Text(copy(.widgetStatusUnavailable)).font(.caption) }
                        if let tokens = server.summary?.usage?.known_tokens {
                            Text(widgetNumber(tokens)).font(.caption).monospacedDigit().lineLimit(1).privacySensitive()
                            Text("Known tokens · incomplete").font(.caption2)
                        } else { Text(copy(.widgetTokensUnavailable)).font(.caption).lineLimit(2) }

                    }.frame(maxWidth: .infinity, alignment: .leading)
                    if family != .systemSmall {
                        VStack(alignment: .leading, spacing: 2) {
                            if let accounts = server.summary?.accounts {
                                ForEach(Array(accounts.entries.prefix(family == .systemLarge ? 2 : 1).enumerated()), id: \.offset) { _, account in
                                    Text(account.alias_hidden == true ? copy(.widgetAliasHidden) : account.alias).font(.caption).lineLimit(1).privacySensitive()
                                    if let quota = account.windows.first {
                                        Text(quota.label(at: now, snapshotStale: server.isStale(at: now), language: entry.language)).font(.caption2).lineLimit(2).privacySensitive()
                                        if family == .systemLarge {
                                            if let observed = quota.observed_at.flatMap(widgetTimestamp) { Text(copy(.observedAt, ["at": date(observed)])).font(.caption2) }
                                            if let reset = quota.reset_at.flatMap(widgetTimestamp) { Text(copy(.resetAt, ["at": date(reset)])).font(.caption2) }
                                        }
                                    } else { Text(copy(.quotaUnavailable)).font(.caption2) }
                                    if family == .systemLarge && (account.more || account.windows.count > 1) { Text(copy(.widgetMoreWindows)).font(.caption2) }
                                }
                                if accounts.entries.isEmpty { Text(copy(.quotaUnavailable)).font(.caption2) }
                                if accounts.more || accounts.entries.count > (family == .systemLarge ? 2 : 1) || (family == .systemMedium && accounts.entries.contains { $0.more || $0.windows.count > 1 }) { Text(copy(.widgetMoreQuotas)).font(.caption2) }
                            } else { Text(copy(.quotaUnavailable)).font(.caption2) }
                            if !(server.summary?.usage?.estimates.isEmpty ?? true) { Text(copy(.widgetEstimates)).font(.caption2) }
                            ForEach(Array((server.summary?.usage?.estimates ?? []).prefix(2).enumerated()), id: \.offset) { _, estimate in Text(copy(.widgetEstimate, ["currency": estimate.currency, "amount": estimate.known_amount.map(widgetNumber) ?? copy(.widgetUnavailable)])).font(.caption2).lineLimit(1).privacySensitive() }
                            if (server.summary?.usage?.estimates.count ?? 0) > 2 { Text(copy(.widgetMoreCurrencies)).font(.caption2) }
                            Text(copy(.widgetActualCost)).font(.caption2)
                        }.frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                Spacer(minLength: 0)
                if let success = server.last_successful_at.flatMap(widgetTimestamp) {
                    Text(copy(.widgetLastSuccess, ["at": date(success)])).font(.caption2).lineLimit(2)
                } else { Text(copy(.widgetNoSuccess)).font(.caption2) }
            } else {
                Text(entry.storageFailed ? copy(.widgetSnapshotUnavailable) : copy(.widgetSelect)).font(.subheadline)
                Text(copy(.widgetRefreshHelp)).font(.caption)
            }
        }.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}
