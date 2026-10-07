package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

func settingsFixture() map[string]any {
	return map[string]any{"effective": map[string]any{}, "sources": []any{}, "applied": map[string]any{"model": "fixed-model", "effort": "high", "advisor": nil, "ultracode": false}}
}

func TestAppliedSettingsPreservesNativeDefaultsAndRejectsSilentChanges(t *testing.T) {
	for _, name := range []string{"selected", "default", "no-effort", "model-change", "effort-change", "effort-null", "effort-missing", "effort-unknown", "effort-number", "model-null", "model-missing", "model-overlong", "advisor-missing", "advisor-enabled", "ultracode-enabled", "ultracode-missing", "ultracode-null", "raw-settings", "settings-null", "settings-missing", "source-present", "sources-null", "sources-missing", "applied-missing", "applied-null", "case-alias", "unknown-field", "duplicate-field"} {
		t.Run(name, func(t *testing.T) {
			value := settingsFixture()
			applied := value["applied"].(map[string]any)
			requested := HighEffort
			switch name {
			case "default":
				requested = ""
			case "no-effort":
				requested = ""
				applied["effort"] = nil
			case "model-change":
				applied["model"] = "foreign"
			case "effort-change":
				applied["effort"] = "medium"
			case "effort-null":
				applied["effort"] = nil
			case "effort-missing":
				delete(applied, "effort")
			case "effort-unknown":
				applied["effort"] = "unverified"
			case "effort-number":
				applied["effort"] = 10000
			case "model-null":
				applied["model"] = nil
			case "model-missing":
				delete(applied, "model")
			case "model-overlong":
				applied["model"] = strings.Repeat("x", 257)
			case "advisor-missing":
				delete(applied, "advisor")
			case "advisor-enabled":
				applied["advisor"] = map[string]any{"model": "foreign"}
			case "ultracode-enabled":
				applied["ultracode"] = true
			case "ultracode-missing":
				delete(applied, "ultracode")
			case "ultracode-null":
				applied["ultracode"] = nil
			case "raw-settings":
				value["effective"] = map[string]any{"env": map[string]any{"unrequested": "private"}}
			case "settings-null":
				value["effective"] = nil
			case "settings-missing":
				delete(value, "effective")
			case "source-present":
				value["sources"] = []any{map[string]any{"source": "managed", "settings": map[string]any{}}}
			case "sources-null":
				value["sources"] = nil
			case "sources-missing":
				delete(value, "sources")
			case "applied-missing":
				delete(value, "applied")
			case "applied-null":
				value["applied"] = nil
			case "case-alias":
				applied["Model"] = "foreign"
			case "unknown-field":
				applied["fallback"] = "foreign"
			}
			raw, _ := json.Marshal(value)
			if name == "duplicate-field" {
				raw = []byte(`{"effective":{},"sources":[],"applied":{"model":"fixed-model","effort":"high","effort":"low","advisor":null,"ultracode":false}}`)
			}
			observed, err := decodeAppliedSettings(raw, "fixed-model", requested)
			valid := name == "selected" || name == "default" || name == "no-effort"
			if valid {
				if err != nil || observed.Model != "fixed-model" || (name == "no-effort") != (observed.Effort == nil) || (observed.Effort != nil && *observed.Effort != HighEffort) {
					t.Fatal("native applied observation changed", err)
				}
			} else if err == nil {
				t.Fatal("unverified or changed applied settings accepted")
			}
		})
	}
}

func TestRawStreamHasNoInventedInitialAppliedSettings(t *testing.T) {
	if _, ok := (&Stream{}).InitialAppliedSettings(); ok {
		t.Fatal("raw transport invented verified settings")
	}
}

func TestAppliedEffortHasNoAdvertisedSupportGate(t *testing.T) {
	value := settingsFixture()
	effort := NativeEffort("Future-Effort")
	value["applied"].(map[string]any)["effort"] = string(effort)
	raw, _ := json.Marshal(value)
	for _, requested := range []NativeEffort{effort, ""} {
		observed, err := decodeAppliedSettings(raw, "fixed-model", requested)
		if err != nil || observed.Effort == nil || *observed.Effort != effort {
			t.Fatal("native support was decided by an effort list", err)
		}
	}
}
