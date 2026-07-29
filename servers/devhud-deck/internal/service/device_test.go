package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/google/uuid"
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
