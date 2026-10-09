// SPDX-License-Identifier: Apache-2.0
// Offscreen SwiftUI fixture; it does not install or accept a WidgetKit extension.
import AppKit
import Foundation
import SwiftUI
import WidgetKit

@main struct LayoutFixture {
    @MainActor static func main() throws {
        guard CommandLine.arguments.count == 2 else { throw SnapshotFailure.invalid }
        let destination = URL(fileURLWithPath: CommandLine.arguments[1], isDirectory: true)
        let now = Date(), observed = ISO8601DateFormatter().string(from: now)
        let reset = ISO8601DateFormatter().string(from: now.addingTimeInterval(6 * 86400 + 3 * 3600 + 120))
        let stale = ISO8601DateFormatter().string(from: now.addingTimeInterval(-3600))
        let summary = Summary(
            overview: Overview(observed_at: observed, stale: false, active_sessions: "12", pending_interactions: "1", registered_workers: "3", connected_workers: "2"),
            usage: Usage(known_tokens: "90071992547409930000", incomplete: true, estimates: [Estimate(currency: "USD", known_amount: "0.000000001"), Estimate(currency: "KRW", known_amount: "9007199254740993.000")]),
            accounts: Accounts(entries: [Account(alias: "Original user name", windows: [Quota(state: .observed, remaining_basis_points: 1200, observed_at: observed, reset_at: reset)], more: true)], more: false))
        let id = "01999ae2-9000-7000-8000-000000000001"
        let fresh = ServerSnapshot(id: id, name: "Original server", state: .observed, last_successful_at: observed, last_attempted_at: observed, summary: summary)
        let old = ServerSnapshot(id: id, name: "Original server", state: .stale, last_successful_at: stale, last_attempted_at: observed, summary: summary)
        let offline = ServerSnapshot(id: id, name: "Original server", state: .unavailable, last_successful_at: nil, last_attempted_at: observed, summary: nil)
        let sizes: [(String, WidgetFamily, CGFloat, CGFloat)] = [("small", .systemSmall, 170, 170), ("medium", .systemMedium, 364, 170), ("large", .systemLarge, 364, 382)]
        let cases: [(String, ServerSnapshot?, Bool, Bool)] = [("observed", fresh, false, false), ("unselected", nil, false, false), ("offline", offline, false, false), ("stale", old, false, false), ("storage-failed", nil, true, false), ("language-failed", fresh, false, true)]
        var rendered = 0
        for language in [WidgetLanguage.english, .korean] {
            for (size, family, width, height) in sizes {
                for (state, server, storageFailed, languageFailed) in cases {
                    let entry = StatusEntry(date: now, server: server, storageFailed: storageFailed, language: language, languageFailed: languageFailed)
                    let view = StatusContent(entry: entry, family: family).padding(12).frame(width: width, height: height).background(Color.white).foregroundStyle(Color.black).environment(\.locale, Locale(identifier: language == .korean ? "ko-KR" : "en-US"))
                    let renderer = ImageRenderer(content: view); renderer.scale = 2
                    guard let image = renderer.cgImage, image.width == Int(width * 2), image.height == Int(height * 2), let png = NSBitmapImageRep(cgImage: image).representation(using: .png, properties: [:]) else { throw SnapshotFailure.invalid }
                    try png.write(to: destination.appendingPathComponent("\(language.rawValue)-\(size)-\(state).png"))
                    rendered += 1
                }
            }
        }
        print("widget_layout_fixture images=\(rendered) languages=2 sizes=3 states=6 native_acceptance=not-performed")
    }
}
