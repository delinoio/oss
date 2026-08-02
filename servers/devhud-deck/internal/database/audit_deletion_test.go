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
	firstBinding := &deckv1.ShortcutBinding{
		Modifiers: []deckv1.ShortcutModifier{
			deckv1.ShortcutModifier_SHORTCUT_MODIFIER_CONTROL,
			deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META,
		},
		Key: deckv1.ShortcutKey_SHORTCUT_KEY_A,
	}
	shortcuts := []*deckv1.ViewShortcut{
		{Binding: firstBinding, State: deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE},
		{Binding: &deckv1.ShortcutBinding{
			Modifiers: []deckv1.ShortcutModifier{
				deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META,
				deckv1.ShortcutModifier_SHORTCUT_MODIFIER_CONTROL,
			},
			Key: deckv1.ShortcutKey_SHORTCUT_KEY_A,
		}, State: deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE},
	}
	if err := recalculateShortcutStates(shortcuts); err != nil {
		t.Fatal(err)
	}
	for _, shortcut := range shortcuts {
		if shortcut.State != deckv1.ShortcutState_SHORTCUT_STATE_CONFLICTED {
			t.Fatalf("canonical-equivalent shortcut state = %s", shortcut.State)
		}
	}
}

func TestOnlyActiveShortcutsAttachViews(t *testing.T) {
	t.Parallel()
	viewID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	otherViewID := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	for _, test := range []struct {
		name     string
		shortcut *deckv1.ViewShortcut
		want     bool
	}{
		{
			name: "active",
			shortcut: &deckv1.ViewShortcut{
				ViewId: uuidProto(viewID),
				State:  deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE,
			},
			want: true,
		},
		{
			name: "conflicted",
			shortcut: &deckv1.ViewShortcut{
				ViewId: uuidProto(viewID),
				State:  deckv1.ShortcutState_SHORTCUT_STATE_CONFLICTED,
			},
		},
		{
			name: "inactive",
			shortcut: &deckv1.ViewShortcut{
				ViewId: uuidProto(viewID),
				State:  deckv1.ShortcutState_SHORTCUT_STATE_INACTIVE,
			},
		},
		{
			name: "other view",
			shortcut: &deckv1.ViewShortcut{
				ViewId: uuidProto(otherViewID),
				State:  deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE,
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := activeShortcutTargetsView(
				test.shortcut, viewID); got != test.want {
				t.Fatalf("active shortcut attachment = %t, want %t",
					got, test.want)
			}
		})
	}
}
