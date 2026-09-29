// SPDX-License-Identifier: Apache-2.0
import Foundation
import WidgetKit

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
