package postgres

import (
	"encoding/json"
	"testing"
)

func TestMigrateSettingsCanonicalJSONRemovesDeviceLocalAndEndpointFields(t *testing.T) {
	input := []byte(`{"agents":[{"repositoryPrompts":[{"body":"private prompt"}]}],"schemaVersion":6,"shortcuts":{"desktop":{"secret":"binding"}},"uploads":{"provider":"r2","r2":{"accountId":"0123456789abcdef0123456789abcdef","endpoint":"https://attacker.invalid","profileRef":"profile"}}}`)
	migrated, transformed, err := migrateSettingsCanonicalJSON(input, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !transformed {
		t.Fatal("expected the legacy snapshot to be transformed")
	}
	var root map[string]any
	if err := json.Unmarshal(migrated, &root); err != nil {
		t.Fatal(err)
	}
	if _, exists := root["shortcuts"]; exists {
		t.Fatal("shortcut bindings must not survive the server migration")
	}
	agent := root["agents"].([]any)[0].(map[string]any)
	if _, exists := agent["repositoryPrompts"]; exists {
		t.Fatal("repository prompts must not survive the server migration")
	}
	r2 := root["uploads"].(map[string]any)["r2"].(map[string]any)
	if _, exists := r2["endpoint"]; exists {
		t.Fatal("arbitrary R2 endpoints must not survive the server migration")
	}
	if r2["accountId"] != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("unexpected account ID: %v", r2["accountId"])
	}
}

func TestMigrateSettingsCanonicalJSONDerivesOrDisablesLegacyR2(t *testing.T) {
	for name, test := range map[string]struct {
		endpoint     string
		wantProvider string
		wantAccount  string
	}{
		"cloudflare": {endpoint: "https://fedcba9876543210fedcba9876543210.r2.cloudflarestorage.com/", wantProvider: "r2", wantAccount: "fedcba9876543210fedcba9876543210"},
		"custom":     {endpoint: "https://storage.internal.example/", wantProvider: "official"},
	} {
		t.Run(name, func(t *testing.T) {
			input := []byte(`{"agents":[],"schemaVersion":4,"shortcuts":{},"uploads":{"provider":"r2","r2":{"endpoint":"` + test.endpoint + `","profileRef":"legacy"}}}`)
			migrated, _, err := migrateSettingsCanonicalJSON(input, 4)
			if err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal(migrated, &root); err != nil {
				t.Fatal(err)
			}
			uploads := root["uploads"].(map[string]any)
			if uploads["provider"] != test.wantProvider {
				t.Fatalf("provider = %v, want %q", uploads["provider"], test.wantProvider)
			}
			if test.wantAccount == "" {
				if uploads["r2"] != nil {
					t.Fatalf("unsafe legacy R2 profile was retained: %v", uploads["r2"])
				}
				return
			}
			r2 := uploads["r2"].(map[string]any)
			if r2["accountId"] != test.wantAccount {
				t.Fatalf("account ID = %v, want %q", r2["accountId"], test.wantAccount)
			}
		})
	}
}
