package postgres

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/delinoio/oss/servers/devhud-api/internal/rpc"
)

const migrationProfileID = "018f47a2-7b3c-7def-8abc-1234567890ab"

func TestMigrateSettingsCanonicalJSONTransformsEveryLegacySchema(t *testing.T) {
	for schemaVersion := uint32(1); schemaVersion <= 6; schemaVersion++ {
		t.Run(fmt.Sprintf("v%d", schemaVersion), func(t *testing.T) {
			input := legacySettingsFixture(schemaVersion)
			migrated, transformed, err := migrateSettingsCanonicalJSON(mustJSON(t, input), schemaVersion)
			if err != nil {
				t.Fatal(err)
			}
			if !transformed {
				t.Fatal("expected the legacy snapshot to be transformed")
			}
			if err := rpc.ValidateDevHudSettingsSnapshot(migrated, 7); err != nil {
				t.Fatalf("migrated snapshot is invalid: %v\n%s", err, migrated)
			}
			var root map[string]any
			if err := json.Unmarshal(migrated, &root); err != nil {
				t.Fatal(err)
			}
			if root["schemaVersion"] != float64(7) {
				t.Fatalf("schemaVersion = %v", root["schemaVersion"])
			}
			if _, exists := root["shortcuts"]; exists {
				t.Fatal("shortcut bindings must not survive the server migration")
			}
			agent := root["agents"].([]any)[0].(map[string]any)
			if _, exists := agent["repositoryPrompts"]; exists {
				t.Fatal("repository prompts must not survive the server migration")
			}
			if schemaVersion < 6 && agent["profileRef"] != nil {
				t.Fatalf("dangling pre-v6 agent profile was retained: %v", agent["profileRef"])
			}
		})
	}
}

func TestMigrateSettingsCanonicalJSONAppliesVersionSpecificTransforms(t *testing.T) {
	v1 := legacySettingsFixture(1)
	v1["decks"] = []any{legacyDeck(false)}
	v1GitHub := v1["github"].(map[string]any)
	v1GitHub["repositories"] = []any{map[string]any{"owner": "delinoio", "name": "oss"}}
	v1GitHub["issueTracker"] = map[string]any{"owner": "delinoio", "repository": "oss", "labels": []any{"bug"}}
	v1Root := migratedRoot(t, v1, 1)
	if len(v1Root["decks"].([]any)) != 0 {
		t.Fatal("v1 decks without credential profiles must be dropped")
	}
	v1MigratedGitHub := v1Root["github"].(map[string]any)
	if v1MigratedGitHub["repositories"].([]any)[0].(map[string]any)["profileRef"] != nil || v1MigratedGitHub["issueTracker"].(map[string]any)["profileRef"] != nil {
		t.Fatal("v1 GitHub references were not made explicitly local/unconfigured")
	}

	v2 := legacySettingsFixture(2)
	v2["decks"] = []any{legacyDeck(true)}
	v2Root := migratedRoot(t, v2, 2)
	deck := v2Root["decks"].([]any)[0].(map[string]any)
	if deck["name"] != "Pull requests" || deck["query"] != "author:octocat is:pr repo:delinoio/oss" {
		t.Fatalf("unexpected v2 deck migration: %v", deck)
	}
	if _, exists := deck["title"]; exists {
		t.Fatal("legacy deck title survived migration")
	}
	builder := deck["builder"].(map[string]any)
	if builder["repository"] != "delinoio/oss" || builder["author"] != "octocat" {
		t.Fatalf("unexpected deck builder projection: %v", builder)
	}

	v3 := legacySettingsFixture(3)
	v3["urlMappings"] = []any{structuredMapping()}
	v3Root := migratedRoot(t, v3, 3)
	if len(v3Root["urlMappings"].([]any)) != 1 {
		t.Fatal("structured mappings in the colliding v3 schema must be retained")
	}
}

func TestMigrateSettingsCanonicalJSONTrimsECMAScriptDeckWhitespace(t *testing.T) {
	for _, schemaVersion := range []uint32{2, 3} {
		t.Run(fmt.Sprintf("v%d", schemaVersion), func(t *testing.T) {
			input := legacySettingsFixture(schemaVersion)
			deck := legacyDeck(true)
			deck["title"] = "\ufeff Pull requests \ufeff"
			input["decks"] = []any{deck}

			root := migratedRoot(t, input, schemaVersion)
			migrated := root["decks"].([]any)[0].(map[string]any)
			if migrated["name"] != "Pull requests" {
				t.Fatalf("migrated Deck name = %q", migrated["name"])
			}
		})
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
			input := legacySettingsFixture(4)
			input["uploads"] = map[string]any{"provider": "r2", "r2": map[string]any{
				"profileRef": "legacy", "bucket": "screenshots", "endpoint": test.endpoint, "region": "auto", "publicBaseUrl": nil,
			}}
			root := migratedRoot(t, input, 4)
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
			if r2["accountId"] != test.wantAccount || r2["name"] != "R2" || r2["prefix"] != "" {
				t.Fatalf("unexpected R2 migration: %v", r2)
			}
			if _, exists := r2["endpoint"]; exists {
				t.Fatal("arbitrary R2 endpoint survived migration")
			}
		})
	}
}

func migratedRoot(t *testing.T, input map[string]any, version uint32) map[string]any {
	t.Helper()
	migrated, _, err := migrateSettingsCanonicalJSON(mustJSON(t, input), version)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(migrated, &root); err != nil {
		t.Fatal(err)
	}
	return root
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func legacySettingsFixture(version uint32) map[string]any {
	github := map[string]any{"repositories": []any{}, "issueTracker": nil}
	if version >= 2 {
		github["profiles"] = []any{map[string]any{"id": migrationProfileID, "name": "Work", "kind": "fine-grained"}}
		github["pendingPatRemovals"] = []any{}
	}
	shortcuts := map[string]any{"desktop": map[string]any{}, "ios": map[string]any{}, "android": map[string]any{}}
	if version >= 4 {
		shortcuts["desktop"] = structuredShortcuts()
	}
	agentProfileRef := any("dangling")
	if version >= 6 {
		agentProfileRef = migrationProfileID
	}
	return map[string]any{
		"schemaVersion": json.Number(fmt.Sprint(version)),
		"appearance":    map[string]any{"theme": "system", "language": "system"},
		"decks":         []any{},
		"github":        github,
		"urlMappings":   []any{},
		"shortcuts":     shortcuts,
		"agents": []any{map[string]any{
			"id": "codex", "enabled": true, "kind": "codex", "mode": "draft", "profileRef": agentProfileRef, "repositoryPrompts": promptsForVersion(version),
		}},
		"uploads": map[string]any{"provider": "official", "r2": nil},
	}
}

func promptsForVersion(version uint32) any {
	if version < 6 {
		return true
	}
	return []any{map[string]any{"repository": map[string]any{"owner": "delinoio", "name": "oss"}, "body": "local-only"}}
}

func structuredShortcuts() map[string]any {
	result := make(map[string]any)
	for index, action := range []string{"shell.command-palette", "realqa.capture.display", "realqa.capture.active-window", "realqa.capture.all-displays", "realqa.capture.selection", "realqa.capture.toolbar"} {
		key := "key-k"
		modifiers := []any{"right-primary"}
		if index > 0 {
			key = fmt.Sprintf("digit-%d", index)
			modifiers = []any{}
		}
		result[action] = map[string]any{"enabled": true, "modifiers": modifiers, "key": key}
	}
	return result
}

func legacyDeck(profile bool) map[string]any {
	deck := map[string]any{
		"id": "018f47a2-7b3c-7def-8abc-1234567890ac", "title": " Pull requests ", "query": "author:octocat", "repository": "delinoio/oss",
		"display": map[string]any{"groupBy": "none", "showDrafts": true}, "refreshMinutes": json.Number("5"), "notifications": []any{"review", "review"},
	}
	if profile {
		deck["profileRef"] = migrationProfileID
	}
	return deck
}

func structuredMapping() map[string]any {
	return map[string]any{
		"id": "018f47a2-7b3c-7def-8abc-1234567890ad", "pattern": "https://github.com/delinoio/oss/**",
		"repository": map[string]any{"owner": "delinoio", "name": "oss"}, "credentialProfileRef": migrationProfileID,
		"priority": json.Number("1"), "chromeOrigin": nil, "updatedAt": "2026-08-17T00:00:00.000Z",
	}
}
