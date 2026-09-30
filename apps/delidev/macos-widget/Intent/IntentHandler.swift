// SPDX-License-Identifier: Apache-2.0
import Foundation
import Intents

final class IntentHandler: INExtension, SelectServerIntentHandling {
    override func handler(for intent: INIntent) -> Any { self }
    func provideServerOptionsCollection(for intent: SelectServerIntent, with completion: @escaping (INObjectCollection<ServerSelection>?, Error?) -> Void) {
        let servers = (try? SnapshotStore.shared().read().servers) ?? []
        completion(INObjectCollection(items: servers.map { ServerSelection(identifier: $0.id, display: $0.name) }), nil)
    }
    // An unset or removed server never falls back to another connection.
    func defaultServer(for intent: SelectServerIntent) -> ServerSelection? { nil }
}
