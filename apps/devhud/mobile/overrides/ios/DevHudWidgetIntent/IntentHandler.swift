import Intents
import Foundation

final class IntentHandler: INExtension, SelectDeckIntentHandling {
    override func handler(for intent: INIntent) -> Any { self }

    @objc func provideDeckOptionsCollection(for intent: SelectDeckIntent, with completion: @escaping (INObjectCollection<DeckSelection>?, Error?) -> Void) {
        guard let defaults = UserDefaults(suiteName: "group.io.delino.devhud") else { completion(INObjectCollection(items: []), nil); return }
        let items = defaults.dictionaryRepresentation().compactMap { key, value -> DeckSelection? in
            guard key.hasPrefix("widget.configuration."), let data = value as? Data,
                  let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let deckId = object["deckId"] as? String, let name = object["name"] as? String else { return nil }
            let selection = DeckSelection(identifier: deckId, display: name)
            selection.name = name
            return selection
        }.sorted { $0.displayString.localizedStandardCompare($1.displayString) == .orderedAscending }
        completion(INObjectCollection(items: items), nil)
    }

    func defaultDeck(for intent: SelectDeckIntent) -> DeckSelection? { nil }
}
