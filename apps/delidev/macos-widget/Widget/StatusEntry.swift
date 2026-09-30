// SPDX-License-Identifier: Apache-2.0
import Foundation
import WidgetKit

struct StatusEntry: TimelineEntry {
    let date: Date
    let server: ServerSnapshot?
    let storageFailed: Bool
}
