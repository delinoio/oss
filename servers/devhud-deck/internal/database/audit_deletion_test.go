package database

import (
	"testing"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/google/uuid"
)

func TestRetainDeviceStateRemovesDeletedViews(t *testing.T) {
	t.Parallel()
	deletedViewID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	retainedViewID := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	deleted := map[uuid.UUID]struct{}{deletedViewID: {}}
	shortcuts, changed := retainShortcuts([]*deckv1.ViewShortcut{
		{ViewId: uuidProto(deletedViewID)},
		{ViewId: uuidProto(retainedViewID)},
	}, deleted)
	if !changed || len(shortcuts) != 1 ||
		uuidValueFromProto(shortcuts[0].ViewId) != retainedViewID {
		t.Fatalf("retained shortcuts = %#v changed=%t", shortcuts, changed)
	}
	widgets, changed := retainWidgets([]*deckv1.WidgetState{
		{ViewId: uuidProto(deletedViewID)},
		{ViewId: uuidProto(retainedViewID)},
	}, deleted)
	if !changed || len(widgets) != 1 ||
		uuidValueFromProto(widgets[0].ViewId) != retainedViewID {
		t.Fatalf("retained widgets = %#v changed=%t", widgets, changed)
	}
}

func TestRecalculateShortcutStatesAfterDeletion(t *testing.T) {
	t.Parallel()
	binding := &deckv1.ShortcutBinding{
		Modifiers: []deckv1.ShortcutModifier{
			deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META,
		},
		Key: deckv1.ShortcutKey_SHORTCUT_KEY_A,
	}
	shortcuts := []*deckv1.ViewShortcut{
		{Binding: binding, State: deckv1.ShortcutState_SHORTCUT_STATE_CONFLICTED},
	}
	recalculateShortcutStates(shortcuts)
	if shortcuts[0].State != deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE {
		t.Fatalf("remaining shortcut state = %s", shortcuts[0].State)
	}
}
