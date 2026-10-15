// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func modelFixture(id json.RawMessage, method string, raw json.RawMessage, mode string, write func(json.RawMessage, any)) bool {
	if method == "config/read" {
		var name string
		for _, arg := range os.Args {
			if value, ok := strings.CutPrefix(arg, "model_provider="); ok {
				name, _ = strconv.Unquote(value)
			}
		}
		write(id, map[string]any{"config": map[string]any{"model_provider": name, "cli_auth_credentials_store": "ephemeral", "model_providers": map[string]any{name: map[string]any{"name": "DeliDev model observation", "base_url": "http://127.0.0.1:1", "wire_api": "responses", "requires_openai_auth": false, "supports_websockets": false}}}, "origins": map[string]any{}, "layers": nil})
		return true
	}
	if method != "model/list" {
		return false
	}
	var params struct {
		Cursor *string `json:"cursor"`
		Limit  uint32  `json:"limit"`
		Hidden bool    `json:"includeHidden"`
	}
	if json.Unmarshal(raw, &params) != nil || params.Limit != 200 {
		os.Exit(22)
	}
	entry := map[string]any{"id": "picker-one", "model": "executable-one", "displayName": "First", "description": "Advisory fixture", "hidden": params.Hidden, "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "medium", "description": "Moderate"}}, "defaultReasoningEffort": "medium", "inputModalities": []string{"text"}, "serviceTiers": []any{}, "defaultServiceTier": nil}
	var next *string
	if params.Cursor == nil {
		value := "page-two"
		next = &value
	} else {
		entry["id"] = "picker-two"
		entry["model"] = "executable-two"
	}
	switch mode {
	case "models-access-programs":
		entry["availableAccessPrograms"] = nil
	case "models-access-programs-object":
		entry["availableAccessPrograms"] = map[string]any{"cyber": []string{"standard", "daybreakBlue", "daybreakRed"}}
	case "models-access-programs-empty":
		entry["availableAccessPrograms"] = map[string]any{"cyber": []string{}}
	case "models-access-programs-type", "models-access-programs-unknown", "models-access-programs-missing", "models-access-programs-null", "models-access-programs-value", "models-access-programs-item", "models-access-programs-array-type":
		// Reject malformed metadata on page two without publishing page one.
		if params.Cursor != nil {
			switch mode {
			case "models-access-programs-type":
				entry["availableAccessPrograms"] = "invalid"
			case "models-access-programs-unknown":
				entry["availableAccessPrograms"] = map[string]any{"cyber": []string{}, "unknown": true}
			case "models-access-programs-missing":
				entry["availableAccessPrograms"] = map[string]any{}
			case "models-access-programs-null":
				entry["availableAccessPrograms"] = map[string]any{"cyber": nil}
			case "models-access-programs-value":
				entry["availableAccessPrograms"] = map[string]any{"cyber": []string{"unknown"}}
			case "models-access-programs-item":
				entry["availableAccessPrograms"] = map[string]any{"cyber": []any{nil}}
			case "models-access-programs-array-type":
				entry["availableAccessPrograms"] = map[string]any{"cyber": "standard"}
			}
		}
	case "models-cursor":
		value := "page-two"
		next = &value
	case "models-duplicate":
		entry["id"] = "picker-one"
	case "models-secret":
		entry["description"] = "Bearer sk-private-fixture"
	case "models-hidden":
		entry["hidden"] = true
	case "models-oversized":
		entry["description"] = strings.Repeat("a", 3000)
	case "models-unknown":
		entry["newAuthority"] = true
	}
	write(id, map[string]any{"data": []any{entry}, "nextCursor": next})
	return true
}

func TestNativeModelPagesAreAtomicBoundedAndAdvisory(t *testing.T) {
	for _, mode := range []string{"models-valid", "models-access-programs", "models-access-programs-object", "models-access-programs-empty", "models-access-programs-type", "models-access-programs-unknown", "models-access-programs-missing", "models-access-programs-null", "models-access-programs-value", "models-access-programs-item", "models-access-programs-array-type", "models-cursor", "models-duplicate", "models-secret", "models-hidden", "models-oversized", "models-unknown"} {
		t.Run(mode, func(t *testing.T) {
			config := fixtureConfig(t, mode)
			config.ModelObservation = true
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			client, err := Open(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			models, err := client.ModelList(ctx, false)
			if closeErr := client.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if mode == "models-valid" || mode == "models-access-programs" || mode == "models-access-programs-object" || mode == "models-access-programs-empty" {
				if err != nil || len(models) != 2 || models[0].ID == models[0].Model || models[1].Model != "executable-two" {
					t.Fatalf("lost complete native identity: count=%d error=%v", len(models), err)
				}
			} else if err == nil || models != nil {
				t.Fatal("published a partial or unsafe observation")
			}
		})
	}
}

func TestNativeModelHiddenSelection(t *testing.T) {
	config := fixtureConfig(t, "models-valid")
	config.ModelObservation = true
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ModelList(ctx, true)
	if closeErr := client.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || len(models) != 2 || !models[0].Hidden {
		t.Fatal("hidden selection was not retained")
	}
}

func TestManualNativeModelObservation(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_MODEL_EXECUTABLE")
	if binary == "" {
		t.Skip("opt-in installed Codex 0.151.0 with a fresh credential-free runtime")
	}
	config := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
	config.API = nil
	config.Mode = ProbeProtocol
	config.ModelObservation = true
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := Open(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	models, err := client.ModelList(ctx, false)
	if closeErr := client.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil || len(models) == 0 {
		t.Fatalf("native listing failed: count=%d error=%v", len(models), err)
	}
	for _, model := range models {
		if model.Validate() != nil {
			t.Fatal("invalid native public metadata")
		}
	}
	t.Logf("credential-free native observation: %d entries; no thread or inference requested", len(models))
}
