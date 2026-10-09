// SPDX-License-Identifier: Apache-2.0
import Foundation
import WidgetKit

@_cdecl("delidev_widget_set_language")
public func delidevWidgetSetLanguage(_ pointer: UnsafePointer<UInt8>?, _ size: Int) -> Int32 {
    guard let pointer, size > 0, size <= 4096 else { return 1 }
    do {
        let bytes = Data(bytes: pointer, count: size)
        guard let object = try JSONSerialization.jsonObject(with: bytes) as? [String: Any],
              Set(object.keys) == Set(["version", "language"]) else { return 1 }
        let document = try JSONDecoder().decode(WidgetLanguageDocument.self, from: bytes)
        try SnapshotStore.shared().setLanguage(document)
        WidgetCenter.shared.reloadTimelines(ofKind: widgetKind)
        return 0
    } catch { return 1 }
}

// Only a closed, bounded metadata envelope crosses this in-process ABI. Never
// return an OS error description, group path or account data to native logs.
@_cdecl("delidev_widget_publish")
public func delidevWidgetPublish(_ pointer: UnsafePointer<UInt8>?, _ size: Int) -> Int32 {
    guard let pointer, size > 0, size <= 128 * 1024 else { return 1 }
    do {
        let publication = try JSONDecoder().decode(Publication.self, from: Data(bytes: pointer, count: size))
        try SnapshotStore.shared().apply(publication)
        WidgetCenter.shared.reloadTimelines(ofKind: widgetKind)
        return 0
    } catch { return 1 }
}
