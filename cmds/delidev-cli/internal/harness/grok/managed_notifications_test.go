// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func fixtureManagedSettings() map[string]any {
	settings := map[string]any{}
	for _, field := range []string{"show_resolved_model", "sharing_enabled", "privacy_notice_rollout", "privacy_banner_reshow_days", "session_picker_grouped", "tips", "slash_command_tags", "announcements", "campaigns", "gate_message", "gate_url", "gate_label", "allow_access", "consent_gate", "subscription_tier_display", "auto_permission_mode_enabled", "prompt_suggestions_enabled", "permission_mode", "group_tool_verbs", "collapsed_edit_blocks", "subscription_watch_interval_secs", "dock_enabled", "terminal_theme_enabled"} {
		settings[field] = nil
	}
	return settings
}

func TestManagedPresentationCannotChangeAuthenticationOrGrantExecution(t *testing.T) {
	encode := func(value any) []byte { raw, _ := json.Marshal(value); return raw }
	settings := fixtureManagedSettings()
	event := nativewire.Event{Kind: nativewire.Notification, Method: "_x.ai/settings/update", Params: encode(settings)}
	api := &apiConnection{}
	if handled, err := api.consumeManagedPresentation(event); handled || err != nil {
		t.Fatal("API profile acquired managed notifications")
	}
	managed := &apiConnection{profile: apiProfile{authentication: managedAuthentication}}
	if handled, err := managed.consumeManagedPresentation(event); !handled || err != nil || managed.ready || managed.inputStarted {
		t.Fatal("presentation changed native execution authority")
	}
	for _, change := range []func(map[string]any){
		func(value map[string]any) { delete(value, "consent_gate") },
		func(value map[string]any) { value["gate_url"] = "https://fixture.example.invalid/private" },
		func(value map[string]any) { value["consent_gate"] = map[string]any{"id": "fixture-consent"} },
		func(value map[string]any) { value["allow_access"] = false },
		func(value map[string]any) { value["show_resolved_model"] = "true" },
		func(value map[string]any) { value["subscription_watch_interval_secs"] = -1 },
		func(value map[string]any) { value["token"] = "fixture-native-secret" },
		func(value map[string]any) { value["tips"] = map[string]any{"unexpected": true} },
	} {
		value := fixtureManagedSettings()
		change(value)
		if err := validateManagedSettings(encode(value)); err == nil {
			t.Fatal("unverified native gate, malformed settings or unknown field was accepted")
		}
	}
	event.Kind = nativewire.ServerRequest
	if handled, err := managed.consumeManagedPresentation(event); !handled || err == nil {
		t.Fatal("presentation acquired request authority")
	}
}

func TestManagedPresentationBoundsAndOriginalAnnouncementGeneration(t *testing.T) {
	encode := func(value any) []byte { raw, _ := json.Marshal(value); return raw }
	managed := &apiConnection{profile: apiProfile{authentication: managedAuthentication}}
	event := nativewire.Event{Kind: nativewire.Notification, Method: "_x.ai/announcements/update", Params: encode(map[string]any{"gen": 1, "announcements": []any{}})}
	if handled, err := managed.consumeManagedPresentation(event); !handled || err != nil || managed.announcementGeneration != 1 {
		t.Fatal("original presentation generation was not accepted")
	}
	if _, err := managed.consumeManagedPresentation(event); err == nil {
		t.Fatal("duplicate presentation generation was accepted")
	}
	event.Method, event.Params = "_x.ai/settings/update", encode(fixtureManagedSettings())
	managed.presentationCount = 64
	if _, err := managed.consumeManagedPresentation(event); err == nil {
		t.Fatal("unbounded native presentation traffic was accepted")
	}
	managed.presentationCount, managed.presentationBytes = 0, 256<<10
	if _, err := managed.consumeManagedPresentation(event); err == nil {
		t.Fatal("native presentation byte bound was exceeded")
	}
	if boundedPresentation([]byte(`[[[[["too-deep"]]]]]`), 0) {
		t.Fatal("unbounded discarded presentation descendants were accepted")
	}
}
