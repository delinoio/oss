package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

func TestDeviceWriteRejectsDuplicateShortcutIDs(t *testing.T) {
	t.Parallel()
	shortcutID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	viewID := uuid.MustParse("01900000-0000-7000-8000-000000000002")
	configuration := func() *deckv1.ViewShortcutConfiguration {
		return &deckv1.ViewShortcutConfiguration{
			ShortcutId: &deckv1.UuidV7{Value: shortcutID.String()},
			ViewId:     &deckv1.UuidV7{Value: viewID.String()},
			Binding: &deckv1.ShortcutBinding{
				Modifiers: []deckv1.ShortcutModifier{
					deckv1.ShortcutModifier_SHORTCUT_MODIFIER_META,
				},
				Key: deckv1.ShortcutKey_SHORTCUT_KEY_A,
			},
		}
	}
	_, err := (&Device{}).deviceWrite(
		context.Background(),
		contracts.Viewer{},
		deckv1.DevicePlatform_DEVICE_PLATFORM_MACOS,
		"Laptop",
		&deckv1.PushRegistration{
			Provider:        deckv1.PushProvider_PUSH_PROVIDER_APPLE,
			OpaquePushToken: "opaque",
		},
		false,
		[]*deckv1.ViewShortcutConfiguration{configuration(), configuration()},
		nil,
	)
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("duplicate shortcut code = %v, err = %v", code, err)
	}
}

func TestDeviceWriteAllowsRegistrationWithoutInactivePushDelivery(t *testing.T) {
	t.Parallel()
	write, err := (&Device{}).deviceWrite(
		context.Background(),
		contracts.Viewer{},
		deckv1.DevicePlatform_DEVICE_PLATFORM_LINUX,
		"DevHud desktop",
		nil,
		false,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("pushless device write = %v", err)
	}
	if write.Push != nil {
		t.Fatalf("pushless device write persisted push = %#v", write.Push)
	}
}

func TestDeviceWriteRejectsMoreThanTwentyWidgets(t *testing.T) {
	t.Parallel()
	widgets := make([]*deckv1.WidgetConfiguration, 21)
	_, err := (&Device{}).deviceWrite(
		context.Background(), contracts.Viewer{},
		deckv1.DevicePlatform_DEVICE_PLATFORM_ANDROID,
		"DevHud mobile", nil, false, nil, widgets)
	if code := connect.CodeOf(err); code != connect.CodeInvalidArgument {
		t.Fatalf("widget limit code = %v, err = %v", code, err)
	}
}

func TestPreserveWidgetSnapshotsKeepsOnlyUnchangedConfigurations(t *testing.T) {
	t.Parallel()
	widgetID := "01900000-0000-7000-8000-000000000001"
	viewID := "01900000-0000-7000-8000-000000000002"
	snapshot := &deckv1.WidgetSnapshot{
		MatchingCount: 7,
		Freshness:     deckv1.FreshnessState_FRESHNESS_STATE_FRESH,
	}
	current := []*deckv1.WidgetState{{
		WidgetId: &deckv1.UuidV7{Value: widgetID},
		ViewId:   &deckv1.UuidV7{Value: viewID},
		Family:   deckv1.WidgetFamily_WIDGET_FAMILY_ANDROID_COMPACT,
		Privacy:  deckv1.WidgetPrivacy_WIDGET_PRIVACY_COUNTS_ONLY,
		Snapshot: snapshot,
	}}
	unchanged := proto.Clone(current[0]).(*deckv1.WidgetState)
	unchanged.Snapshot = &deckv1.WidgetSnapshot{
		Freshness: deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED,
	}
	changed := proto.Clone(unchanged).(*deckv1.WidgetState)
	changed.Privacy = deckv1.WidgetPrivacy_WIDGET_PRIVACY_REPOSITORY_AND_TITLES

	preserveWidgetSnapshots([]*deckv1.WidgetState{unchanged, changed}, current)

	if !proto.Equal(unchanged.Snapshot, snapshot) {
		t.Fatalf("unchanged snapshot = %#v, want %#v", unchanged.Snapshot, snapshot)
	}
	if changed.GetSnapshot().GetFreshness() !=
		deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED {
		t.Fatalf("changed snapshot was preserved = %#v", changed.Snapshot)
	}
	if unchanged.Snapshot == snapshot {
		t.Fatal("preserved snapshot aliases current server state")
	}
}
