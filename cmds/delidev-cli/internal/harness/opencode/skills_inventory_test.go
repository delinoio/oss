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
