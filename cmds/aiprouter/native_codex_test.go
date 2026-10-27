// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func codexFixture(t *testing.T, config, cache string) (Resolver, Disk, State) {
	t.Helper()
	r := Resolver{Home: realTemp(t), OS: "linux", Env: map[string]string{}}
	path, err := r.Resolve("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"config.toml": config, "models_cache.json": cache} {
		if data != "" {
			if err = os.WriteFile(filepath.Join(filepath.Dir(path), name), []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	disk := testDisk(t)
	state, err := disk.Load()
	if err != nil {
		t.Fatal(err)
	}
	return r, disk, state
}

func TestCodexNativeCatalogCacheAndRestore(t *testing.T) {
	config := "# keep native identity\nmodel_provider = 'openai'\nmodel = 'native-model'\n[features]\nuser_feature = true\n"
	cache := "{\"models\":[{\"slug\":\"native-model\",\"display_name\":\"Native\",\"custom\":17}],\"fetched_at\":\"2026-01-01T00:00:00Z\",\"client_version\":\"1.0\",\"opaque\":\"keep\"}\n"
	r, d, s := codexFixture(t, config, cache)
	models := []ClientModel{{ID: "vendor/model", Name: "Remote", Context: 64000, Output: 4096, Vision: true, Tools: true, Efforts: []string{"low", "high"}}}
	changes, err := PlanCodexNative(r, s, testProfile(), "REMOTE_SECRET", models)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 || !changes[0].RemoteKey {
		t.Fatal("missing journaled files or key consent")
	}
	if got, _ := os.ReadFile(changes[0].Path); string(got) != config {
		t.Fatal("planner wrote settings")
	}
	var preview bytes.Buffer
	Preview(&preview, changes)
	if strings.Contains(preview.String(), "REMOTE_SECRET") {
		t.Fatal("preview leaked values")
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(false)); err == nil {
		t.Fatal("missing consent")
	}
	if exists(changes[1].Path) {
		t.Fatal("declined plan created catalog")
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(true)); err != nil {
		t.Fatal(err)
	}
	doc, err := parseDocument(changes[0].After, TOML)
	if err != nil {
		t.Fatal(err)
	}
	if doc["model_provider"] != "openai" || doc["model"] != "native-model" || mapAt(doc, "model_providers", "openai")["requires_openai_auth"] != false {
		t.Fatal("native routing identity lost")
	}
	rows, err := codexCatalogRows(changes[1].After)
	if err != nil || len(rows) != 2 {
		t.Fatalf("catalog: %v", err)
	}
	if rows[0].(map[string]any)["custom"] != float64(17) {
		t.Fatal("native metadata lost")
	}
	remote := rows[1].(map[string]any)
	if remote["context_window"] != float64(64000) || remote["max_output_tokens"] != float64(4096) {
		t.Fatal("model limits lost")
	}
	levels := remote["supported_reasoning_levels"].([]any)
	if len(levels) != 2 || levels[1].(map[string]any)["effort"] != "high" {
		t.Fatal("reasoning ladder synthesized")
	}
	var cached map[string]any
	if err = json.Unmarshal(changes[2].After, &cached); err != nil {
		t.Fatal(err)
	}
	if cached["opaque"] != "keep" || cached["fetched_at"] != "2000-01-01T00:00:00Z" {
		t.Fatal("cache ownership or invalidation lost")
	}
	// Refresh must replace only our contribution and retain immutable originals.
	models[0].Name = "Updated"
	refreshed, err := PlanCodexNative(r, s, testProfile(), "NEXT_SECRET", models)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", refreshed, answer(true)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(s.Records["codex"].Originals[changes[0].Path].Before, []byte(config)) {
		t.Fatal("original backup changed")
	}
	if err = d.Restore(&s, "codex"); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{changes[0].Path: config, changes[2].Path: cache} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("restore %s: %v", filepath.Base(path), err)
		}
	}
	if exists(changes[1].Path) {
		t.Fatal("new owned catalog not removed")
	}
}

func TestCodexNativeSelectedCatalogPreservedAndFenced(t *testing.T) {
	r, d, s := codexFixture(t, "model_catalog_json = 'native.json'\n", "")
	config, _ := r.Resolve("codex")
	originalPath := filepath.Join(filepath.Dir(config), "native.json")
	original := []byte(`[{"slug":"native","base_instructions":"keep exact instructions","visibility":"hide"}]`)
	if err := os.WriteFile(originalPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	changes, err := PlanCodexNative(r, s, testProfile(), "KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(originalPath, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(true)); !errors.Is(err, ErrConflict) {
		t.Fatalf("source catalog race accepted: %v", err)
	}
	if exists(changes[1].Path) {
		t.Fatal("race applied partial plan")
	}
	if err = os.WriteFile(originalPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(true)); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(originalPath); !bytes.Equal(got, original) {
		t.Fatal("original catalog changed")
	}
	if err = d.Restore(&s, "codex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(config); string(got) != "model_catalog_json = 'native.json'\n" {
		t.Fatal("selection not restored")
	}
}

func TestCodexNativeRefusesOwnershipAndInvalidMetadata(t *testing.T) {
	for name, config := range map[string]string{
		"provider":        "model_provider = 'foreign'\n",
		"table":           "[model_providers.openai]\nbase_url = 'https://foreign.test'\n",
		"profile":         "profile = 'work'\n[profiles.work]\nmodel_provider = 'other'\n",
		"missing catalog": "model_catalog_json = 'missing.json'\n",
		"bad catalog":     "model_catalog_json = 5\n",
		"self catalog":    "model_catalog_json = 'aiprouter-catalog.json'\n",
	} {
		t.Run(name, func(t *testing.T) {
			r, _, s := codexFixture(t, config, "")
			if _, err := PlanCodexNative(r, s, testProfile(), "KEY", nil); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	for name, models := range map[string][]ClientModel{
		"duplicate": {{ID: "same"}, {ID: "same"}}, "blank": {{ID: " "}}, "effort": {{ID: "m", Efforts: []string{"unrecognized"}}},
	} {
		t.Run(name, func(t *testing.T) {
			r, _, s := codexFixture(t, "", "")
			if _, err := PlanCodexNative(r, s, testProfile(), "KEY", models); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		})
	}
	r, _, s := codexFixture(t, "", "")
	config, _ := r.Resolve("codex")
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), codexCatalogName), []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanCodexNative(r, s, testProfile(), "KEY", nil); err == nil {
		t.Fatal("unowned generated catalog adopted")
	}
}

func TestCodexNativeConflictAndHomeSwitch(t *testing.T) {
	r, d, s := codexFixture(t, "# native\n", "")
	changes, err := PlanCodexNative(r, s, testProfile(), "KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(true)); err != nil {
		t.Fatal(err)
	}
	switched := r
	switched.Env = map[string]string{"CODEX_HOME": filepath.Join(r.Home, "other")}
	if _, err = PlanCodexNative(switched, s, testProfile(), "KEY", nil); err == nil {
		t.Fatal("active home changed")
	}
	changed := bytes.Replace(changes[0].After, []byte("KEY"), []byte("USER_EDIT"), 1)
	if err = os.WriteFile(changes[0].Path, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = PlanCodexNative(r, s, testProfile(), "KEY", nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("owned edit overwritten: %v", err)
	}
	if err = d.Restore(&s, "codex"); !errors.Is(err, ErrConflict) {
		t.Fatalf("owned edit restored: %v", err)
	}
	if got, _ := os.ReadFile(changes[0].Path); !bytes.Equal(got, changed) {
		t.Fatal("conflicting user edit lost")
	}
}

func TestCodexNativeExactServiceCapabilities(t *testing.T) {
	exact := object{"slug": "provider/exact", "display_name": "Exact model", "visibility": "list", "supported_reasoning_levels": []any{object{"effort": "max", "description": "Exact tier"}}, "default_reasoning_level": "max", "context_window": 123456, "auto_compact_token_limit": 111111, "supports_websockets": true, "tool_mode": "code_mode_only", "comp_hash": "authoritative", "base_instructions": "Exact neutral instructions", "service_tiers": []any{object{"name": "priority"}}}
	model := ClientModel{ID: "provider/exact", CodexCatalog: exact, Context: 1, Efforts: []string{"low"}}
	r, _, state := codexFixture(t, "", "")
	changes, planErr := PlanCodexNative(r, state, testProfile(), "KEY", []ClientModel{model})
	if planErr != nil {
		t.Fatal(planErr)
	}
	config, parseErr := parseDocument(changes[0].After, TOML)
	if parseErr != nil || mapAt(config, "model_providers", "openai")["supports_websockets"] != true {
		t.Fatal("authoritative WebSocket capability not enabled")
	}
	row, err := codexModelRow(model)
	if err != nil || !semanticEqual(row, exact) {
		t.Fatalf("exact capabilities changed: %v", err)
	}
	row["display_name"] = "changed"
	if exact["display_name"] != "Exact model" {
		t.Fatal("row aliases source metadata")
	}
	model.ID = "different"
	if _, err = codexModelRow(model); err == nil {
		t.Fatal("mismatched service slug accepted")
	}
}

func TestCodexNativeRestoreIndependentSettingsAndDerivedCache(t *testing.T) {
	r, _, s := codexFixture(t, "# native\n[features]\nuser_feature = true\n", `{"models":[{"slug":"native"}],"fetched_at":"original","opaque":"old"}`)
	changes, err := PlanCodexNative(r, s, testProfile(), "KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	config := changes[0]
	changed := bytes.Replace(config.After, []byte("user_feature = true"), []byte("user_feature = false"), 1)
	restored, remove, err := restoreCodexNative(Backup{Path: config.Path, Before: config.Before, After: config.After, Existed: true}, changed)
	if err != nil || remove || !bytes.Equal(restored, bytes.Replace(config.Before, []byte("true"), []byte("false"), 1)) {
		t.Fatalf("unrelated TOML edit lost: %v", err)
	}
	cache := changes[2]
	changed, err = Patch(cache.After, JSON, []Fragment{frag([]string{"fetched_at"}, "new-native-time"), frag([]string{"client_version"}, "new-native-version"), frag([]string{"opaque"}, "user edit")}, true)
	if err != nil {
		t.Fatal(err)
	}
	backup := Backup{Path: cache.Path, Before: cache.Before, After: cache.After, Existed: true}
	restored, remove, err = restoreCodexNative(backup, changed)
	if err != nil || remove {
		t.Fatalf("derived cache refused: %v", err)
	}
	doc, err := parseDocument(restored, JSON)
	if err != nil || doc["opaque"] != "user edit" || doc["fetched_at"] != "original" {
		t.Fatal("unrelated cache data lost")
	}
	changed, err = Patch(changed, JSON, []Fragment{frag([]string{"models"}, []any{object{"slug": "foreign"}})}, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = restoreCodexNative(backup, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed models ignored")
	}
}

func TestCodexNativeSymlinkAndPreviewDestinationRaces(t *testing.T) {
	r, d, s := codexFixture(t, "", "")
	changes, err := PlanCodexNative(r, s, testProfile(), "KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(changes[1].Path, []byte(`{"foreign":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(true)); !errors.Is(err, ErrConflict) {
		t.Fatal("catalog race accepted")
	}
	if exists(changes[0].Path) {
		t.Fatal("race partially applied")
	}
	if err = os.Remove(changes[1].Path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(realTemp(t), "outside.json")
	if err = os.WriteFile(target, []byte(`{"models":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, changes[2].Path); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err = PlanCodexNative(r, s, testProfile(), "KEY", nil); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestCodexNativeRefreshPreservesConcurrentUnrelatedEdits(t *testing.T) {
	r, d, s := codexFixture(t, "[features]\nuser_feature = true\n", `{"models":[{"slug":"native"}],"opaque":"before"}`)
	changes, err := PlanCodexNative(r, s, testProfile(), "KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", changes, answer(true)); err != nil {
		t.Fatal(err)
	}
	config := bytes.Replace(changes[0].After, []byte("user_feature = true"), []byte("user_feature = false"), 1)
	if err = os.WriteFile(changes[0].Path, config, 0600); err != nil {
		t.Fatal(err)
	}
	cache, err := Patch(changes[2].After, JSON, []Fragment{frag([]string{"fetched_at"}, "native-update"), frag([]string{"opaque"}, "foreign edit")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(changes[2].Path, cache, 0600); err != nil {
		t.Fatal(err)
	}
	next, err := PlanCodexNative(r, s, testProfile(), "NEXT_KEY", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Apply(&s, "codex", "test", next, answer(true)); err != nil {
		t.Fatal(err)
	}
	if err = d.Restore(&s, "codex"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(changes[0].Path)
	if err != nil || !bytes.Contains(data, []byte("user_feature = false")) || bytes.Contains(data, []byte("KEY")) {
		t.Fatal("foreign TOML edit lost or key retained")
	}
	data, err = os.ReadFile(changes[2].Path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseDocument(data, JSON)
	if err != nil || doc["opaque"] != "foreign edit" {
		t.Fatal("foreign cache edit lost")
	}
}

func TestCodexNativeLargeCatalogLimits(t *testing.T) {
	instructions := strings.Repeat("x", MaxConfigBytes+1)
	t.Run("service row", func(t *testing.T) {
		r, _, s := codexFixture(t, "", "")
		model := ClientModel{ID: "large-model", CodexCatalog: object{"slug": "large-model", "base_instructions": instructions}}
		changes, err := PlanCodexNative(r, s, testProfile(), "KEY", []ClientModel{model})
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range changes[1:] {
			if len(change.After) <= MaxConfigBytes || len(change.After) > configSizeLimit(change.Path) {
				t.Fatal("large catalog did not use native limit")
			}
		}
		if len(changes[0].After) > MaxConfigBytes {
			t.Fatal("settings exceeded ordinary bound")
		}
	})
	t.Run("arbitrary selected path", func(t *testing.T) {
		r, _, s := codexFixture(t, "model_catalog_json = 'custom-native.data'\n", "")
		config, _ := r.Resolve("codex")
		path := filepath.Join(filepath.Dir(config), "custom-native.data")
		data, err := json.Marshal(object{"models": []any{object{"slug": "native", "base_instructions": instructions}}})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err = readConfig(path); err == nil {
			t.Fatal("ordinary reader accepted oversized arbitrary settings")
		}
		changes, err := PlanCodexNative(r, s, testProfile(), "KEY", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = changes[0].Check(); err != nil {
			t.Fatalf("large selected catalog could not be revalidated: %v", err)
		}
		if err = os.WriteFile(path, []byte(`{"models":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		if err = changes[0].Check(); !errors.Is(err, ErrConflict) {
			t.Fatal("selected catalog race accepted")
		}
	})
	t.Run("ordinary settings", func(t *testing.T) {
		r, _, s := codexFixture(t, "", "")
		if _, err := PlanCodexNative(r, s, testProfile(), instructions, nil); err == nil {
			t.Fatal("oversized ordinary settings accepted")
		}
	})
}
