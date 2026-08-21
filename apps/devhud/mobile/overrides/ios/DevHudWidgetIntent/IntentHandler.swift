import Intents
import Foundation

final class IntentHandler: INExtension, SelectDeckIntentHandling {
    private let stateStore = WidgetStateStore(appGroup: "group.io.delino.devhud")

    override func handler(for intent: INIntent) -> Any { self }

    @objc func provideDeckOptionsCollection(for intent: SelectDeckIntent, with completion: @escaping (INObjectCollection<DeckSelection>?, Error?) -> Void) {
        guard case .success(let states) = stateStore.allDeckStates() else { completion(INObjectCollection(items: []), nil); return }
        let items = states.compactMap { state -> DeckSelection? in
            guard !state.transactionPending, let data = state.configuration,
                  let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let deckId = object["deckId"] as? String, deckId == state.deckId,
                  let name = object["name"] as? String else { return nil }
            let selection = DeckSelection(identifier: deckId, display: name)
            selection.name = name
            return selection
        }.sorted { $0.displayString.localizedStandardCompare($1.displayString) == .orderedAscending }
        completion(INObjectCollection(items: items), nil)
    }

    func defaultDeck(for intent: SelectDeckIntent) -> DeckSelection? { nil }
}
