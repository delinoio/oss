// Package shortcut owns canonical Deck shortcut binding comparison.
package shortcut

import (
	"errors"
	"fmt"
	"sort"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
)

func CanonicalBinding(binding *deckv1.ShortcutBinding) (string, error) {
	if binding == nil || binding.Key < deckv1.ShortcutKey_SHORTCUT_KEY_A ||
		binding.Key > deckv1.ShortcutKey_SHORTCUT_KEY_ENTER ||
		len(binding.Modifiers) == 0 || len(binding.Modifiers) > 4 {
		return "", errors.New("deck shortcut: invalid binding")
	}
	modifiers := append([]deckv1.ShortcutModifier(nil), binding.Modifiers...)
	sort.Slice(modifiers, func(left, right int) bool {
		return modifiers[left] < modifiers[right]
	})
	for index, modifier := range modifiers {
		if modifier < deckv1.ShortcutModifier_SHORTCUT_MODIFIER_CONTROL ||
			modifier > deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META ||
			(index > 0 && modifiers[index-1] == modifier) {
			return "", errors.New("deck shortcut: invalid binding")
		}
	}
	return fmt.Sprintf("%v:%d", modifiers, binding.Key), nil
}
