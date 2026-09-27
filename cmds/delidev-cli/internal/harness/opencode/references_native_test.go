package opencode

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeOpenCodeWorkspaceReferences(t *testing.T) {
	for _, agent := range []PrimaryAgent{BuildAgent, PlanAgent} {
		t.Run(string(agent), func(t *testing.T) {
			var references []WorkspaceReference
			var primary string
			configure := func(c *apiSessionConfig) {
				prepareNativeCheckpointGit(t, c, checkpointCommittedProject)
				c.Settings.Agent = agent
				primary = c.Workspace
				for range 2 {
					path, err := filepath.EvalSymlinks(t.TempDir())
					if err != nil {
						t.Fatal(err)
					}
					references = append(references, WorkspaceReference{domain.NewID(), path})
				}
				c.References = references
			}
			check := func(body map[string]json.RawMessage) {
				var messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				}
				if json.Unmarshal(body["messages"], &messages) != nil {
					t.Error("missing native environment")
					return
				}
				var system strings.Builder
				for _, message := range messages {
					if message.Role != "system" {
						continue
					}
					var content string
					if json.Unmarshal(message.Content, &content) != nil {
						t.Error("changed native environment shape")
						return
					}
					system.WriteString(content)
				}
				text := system.String()
				if !strings.Contains(text, "Working directory: "+primary) {
					t.Error("primary cwd was replaced")
				}
				position := -1
				for i, reference := range references {
					name, path := "<name>"+referenceName(i, reference)+"</name>", "<path>"+reference.Path+"</path>"
					at := strings.Index(text, name)
					if at <= position || strings.Count(text, name) != 1 || strings.Count(text, path) != 1 {
						t.Error("native reference was omitted, repeated or reordered")
					}
					position = at
				}
			}
			nativeOwnedAPISessionWithRequestCheck(t, true, false, nativeServerRelay, configure, check)
		})
	}
}
