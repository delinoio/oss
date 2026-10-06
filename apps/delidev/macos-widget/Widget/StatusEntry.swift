// SPDX-License-Identifier: Apache-2.0
import Foundation
import WidgetKit

struct StatusEntry: TimelineEntry {
    let date: Date
    let server: ServerSnapshot?
    let storageFailed: Bool
    let language: WidgetLanguage
    let languageFailed: Bool
    init(date: Date, server: ServerSnapshot?, storageFailed: Bool, language: WidgetLanguage = resolveWidgetLanguage(.system), languageFailed: Bool = false) {
        self.date = date; self.server = server; self.storageFailed = storageFailed
        self.language = language; self.languageFailed = languageFailed
    }
}
