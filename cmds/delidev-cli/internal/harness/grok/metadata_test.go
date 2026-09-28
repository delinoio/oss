package grok

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
)

//go:embed testdata/passive.json
var passiveFixture []byte

func TestOriginalPassiveMetadataRejectsNestedAliasesAndForeignScope(t *testing.T) {
	var fixtures []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if json.Unmarshal(passiveFixture, &fixtures) != nil || len(fixtures) == 0 {
		t.Fatal("invalid passive fixtures")
	}
	for _, fixture := range fixtures {
		validate := func(raw []byte) error {
			if fixture.Method == "_x.ai/sessions/changed" {
				_, err := parseActivity(raw, turnFixtureSession, "/private/workspace")
				return err
			}
			_, err := parsePassiveObservation(raw, fixture.Method, turnFixtureSession, turnFixturePrompt)
			return err
		}
		if err := validate(fixture.Params); err != nil {
			t.Fatal(fixture.Method, err)
		}
		if validate([]byte(strings.ReplaceAll(string(fixture.Params), string(turnFixtureSession), "019f6de0-a760-7000-8000-000000000072"))) == nil {
			t.Fatal("foreign passive session accepted")
		}
		root := fixtureObject(fixture.Params)
		var inspect func(any)
		inspect = func(value any) {
			switch value := value.(type) {
			case map[string]any:
				for key, child := range value {
					alias := strings.ToUpper(key)
					value[alias] = child
					raw, _ := json.Marshal(root)
					delete(value, alias)
					if validate(raw) == nil {
						t.Fatalf("passive case alias accepted: %s %s", fixture.Method, key)
					}
					inspect(child)
				}
			case []any:
				for _, child := range value {
					inspect(child)
				}
			}
		}
		inspect(root)
	}
}

func TestOriginalActivityCannotChangeWorkspaceModelOrPermission(t *testing.T) {
	var fixtures []struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(passiveFixture, &fixtures)
	for _, fixture := range fixtures {
		if fixture.Method != "_x.ai/sessions/changed" {
			continue
		}
		for _, field := range []string{"cwd", "modelId", "yolo", "isWorktree", "resident", "activity", "origin", "title", "removed"} {
			value := fixtureObject(fixture.Params)
			entry := value["upserted"].([]any)[0].(map[string]any)
			switch field {
			case "cwd", "modelId", "activity":
				entry[field] = "foreign"
			case "yolo", "isWorktree":
				entry[field] = true
			case "resident":
				entry[field] = false
			case "origin":
				entry[field] = map[string]any{"kind": "remote"}
			case "title":
				entry[field] = 42
			case "removed":
				value[field] = []any{turnFixtureSession}
			}
			raw, _ := json.Marshal(value)
			if _, err := parseActivity(raw, turnFixtureSession, "/private/workspace"); err == nil {
				t.Fatal("changed native activity accepted", field)
			}
		}
	}
}
