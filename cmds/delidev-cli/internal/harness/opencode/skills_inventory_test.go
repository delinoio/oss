// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
)

type nativeSkillInventoryTransport struct {
	t        *testing.T
	selected []skills.OpenCodeSelection
	change   string
	reads    []string
}

func (f *nativeSkillInventoryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f.t.Helper()
	if request.Method != http.MethodGet || request.Body != nil && request.Body != http.NoBody {
		f.t.Fatal("inventory inspection attempted a native mutation")
	}
	f.reads = append(f.reads, request.URL.Path)
	var values []map[string]any
	switch request.URL.Path {
	case "/skill":
		values = append(values, map[string]any{"name": "customize-opencode", "location": "<built-in>", "content": "Native builtin"})
		for _, selected := range f.selected {
			values = append(values, map[string]any{"name": selected.Name, "location": selected.Path, "description": selected.Description, "content": selected.Content})
		}
		switch f.change {
		case "unselected":
			values = append(values, map[string]any{"name": "third", "location": "/foreign/SKILL.md", "description": "Foreign", "content": "Foreign"})
		case "duplicate-name":
			values[2]["name"] = values[1]["name"]
		case "foreign-location":
			values[1]["location"] = "/foreign/SKILL.md"
		case "changed-content":
			values[1]["content"] = "changed"
		}
	case "/command":
		for _, name := range []string{"init", "review", "customize-opencode"} {
			values = append(values, map[string]any{"name": name, "template": "Native builtin", "hints": []string{}})
		}
		for _, selected := range f.selected {
			values = append(values, map[string]any{"name": selected.Name, "source": "skill", "description": selected.Description, "hints": []string{}, "template": selected.Content + "\n\nBase directory for this skill: " + filepath.Dir(selected.Path) + "\nRelative paths in this skill (e.g., scripts/, references/) are relative to this base directory."})
		}
		switch f.change {
		case "changed-command":
			values[3]["template"] = "changed"
		case "foreign-command-source":
			values[3]["source"] = "command"
		case "foreign-command-agent":
			values[3]["agent"] = "foreign"
		}
	default:
		f.t.Fatal("unexpected native inventory route", request.URL.Path)
	}
	raw, _ := json.Marshal(values)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func TestNativeSkillInventoryRequiresExactSelectedPackagesAndCommands(t *testing.T) {
	observer, _ := nativeSkillToolFixture(t)
	first := observer.skillPackages[0]
	second := first
	second.Name = "second"
	second.Path = filepath.Join(filepath.Dir(filepath.Dir(first.Path)), second.Name, "SKILL.md")
	for _, change := range []string{"original", "unselected", "duplicate-name", "foreign-location", "changed-content", "changed-command", "foreign-command-source", "foreign-command-agent"} {
		t.Run(change, func(t *testing.T) {
			transport := &nativeSkillInventoryTransport{t: t, selected: []skills.OpenCodeSelection{first, second}, change: change}
			session := &sessionAPI{apiProfile: &nativeAPIProfile{Skills: transport.selected}, client: &http.Client{Transport: transport}, password: "fixture-secret", origin: "http://127.0.0.1:1", cwd: t.TempDir(), alive: func() error { return nil }}
			if err := session.verifyNativeSkills(context.Background()); (err == nil) != (change == "original") {
				t.Fatalf("native inventory %s: %v", change, err)
			}
			if change == "original" && strings.Join(transport.reads, ",") != "/skill,/command" {
				t.Fatal("complete native inventory was not read")
			}
		})
	}
}

func TestNativeSkillMutationRechecksPolicyAndLatchesDriftBeforeClaim(t *testing.T) {
	for _, change := range []string{"original", "configuration", "agent-policy", "native-registry"} {
		t.Run(change, func(t *testing.T) {
			observer, _ := nativeSkillToolFixture(t)
			profile := fixtureAPIProfile()
			profile.Skills = observer.skillPackages
			home := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(profile.Skills[0].Path))))
			profile.SkillHome = home
			if err := profile.inspectInstructions(); err != nil {
				t.Fatal("local package fixture", err)
			}
			agents := fixtureAgentInventory(home)
			primary, err := expectedPrimaryAgent(profile.Settings.Agent, home, home)
			if err != nil {
				t.Fatal(err)
			}
			rules := primary["permission"].([]PermissionRule)
			rules = append(append(append([]PermissionRule{}, rules[:5]...), PermissionRule{"external_directory", filepath.Join(filepath.Dir(profile.Skills[0].Path), "*"), PermissionAllow}), rules[5:]...)
			rules = append(append(append([]PermissionRule{}, rules[:len(rules)-1]...), PermissionRule{"skill", "*", PermissionDeny}, PermissionRule{"skill", profile.Skills[0].Name, PermissionAllow}), rules[len(rules)-1:]...)
			primary["permission"] = rules
			agents[0] = primary
			transport := &nativeSkillInventoryTransport{t: t, selected: profile.Skills, change: "original"}
			if change == "native-registry" {
				transport.change = "foreign-location"
			}
			client := &http.Client{Transport: childHistoryTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatal("preflight consumed native send authority")
				}
				var value any
				switch r.URL.Path {
				case "/config":
					value = fixtureEffectiveConfig(profile)
					if change == "configuration" {
						value = map[string]any{}
					}
				case "/agent":
					value = agents
					if change == "agent-policy" {
						value = []any{}
					}
				default:
					return transport.RoundTrip(r)
				}
				raw, _ := json.Marshal(value)
				if change == "original" {
					if r.URL.Path == "/config" {
						if err := profile.validateConfig(raw); err != nil {
							t.Fatal("config fixture", err)
						}
					}
					if r.URL.Path == "/agent" {
						if err := validatePrimaryAgentSkills(raw, profile.Settings.Agent, home, home, profile); err != nil {
							t.Fatal("agent fixture", err)
						}
					}
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
			})}
			session := &sessionAPI{apiProfile: profile, runtimeHome: home, runtimeRoot: home, client: client, password: "fixture-secret", origin: "http://127.0.0.1:1", cwd: home, alive: func() error { return nil }}
			err = session.verifyInstructions(context.Background(), "submit-input")
			if (err == nil) != (change == "original") {
				t.Fatal("native mutation policy verification", change, err)
			}
			if change != "original" && (session.problem == nil || session.input != nil || session.commandHTTP != nil) {
				t.Fatal("native drift failed to latch before original claim")
			}
		})
	}
}

func TestNativeSkillSchemaRequiresIndependentOriginalOperations(t *testing.T) {
	for _, change := range []string{"original", "missing-skill", "foreign-command", "unsupported-media"} {
		var document map[string]any
		if err := json.Unmarshal(schemaFixture, &document); err != nil {
			t.Fatal(err)
		}
		paths := document["paths"].(map[string]any)
		for _, operation := range nativeSkillOperations() {
			paths[operation.path] = map[string]any{operation.method: map[string]any{"operationId": operation.id, "responses": map[string]any{operation.status: map[string]any{"content": map[string]any{operation.content: map[string]any{"schema": map[string]any{"type": "object"}}}}}}}
		}
		switch change {
		case "missing-skill":
			delete(paths, "/skill")
		case "foreign-command":
			paths["/session/{sessionID}/command"].(map[string]any)["post"].(map[string]any)["operationId"] = "session.prompt"
		case "unsupported-media":
			paths["/skill"].(map[string]any)["get"].(map[string]any)["responses"].(map[string]any)["200"].(map[string]any)["content"] = map[string]any{}
		}
		raw, _ := json.Marshal(document)
		if err := validateSchemaOperations(raw, nativeSkillOperations()); (err == nil) != (change == "original") {
			t.Fatal("native skill operation identity", change, err)
		}
	}
}
