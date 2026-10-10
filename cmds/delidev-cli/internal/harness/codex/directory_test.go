// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestDirectoryResumePreservesOriginalRootsAndHistory(t *testing.T) {
	_, _, source, page := continuationFixture(t, "ok")
	root := source.Effective.Cwd
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	// This fixture supplies the full explicit-root profile used by the Worker.
	source.Effective.WorkspaceRoots = []string{root}
	source.Effective.Sandbox.WritableRoots = []string{root}
	before, _ := json.Marshal(source)
	c, capture := openThreadFixture(t, "thread-continuation-ok")
	settings := ThreadSettings{Model: source.Effective.Model, Provider: source.Effective.Provider, Effort: *source.Effective.Effort, Cwd: nested, WorkspaceRoots: []string{root}, Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite, ApprovalPolicy: string(source.Effective.ApprovalPolicy), ServiceTier: *source.Effective.ServiceTier}}
	claims := 0
	request := domain.NewID()
	bound, err := c.ResumeDirectory(context.Background(), request, source, settings, func(intent DirectoryIntent) error {
		claims++
		if intent.RequestID != request || intent.Source.ThreadID != source.ThreadID || intent.Destination != nested || !slices.Equal(intent.WorkspaceRoots, []string{root}) {
			t.Fatal("original directory intent changed")
		}
		return nil
	})
	if err != nil || bound.Thread == nil || bound.Thread.ID != source.ThreadID || bound.Effective == nil || bound.Effective.Cwd != nested || !slices.Equal(bound.Effective.WorkspaceRoots, []string{root}) {
		t.Fatal("cold retained-thread settings", err)
	}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), domain.SessionInput{Prompt: "blocked", Mode: domain.ExecuteMode}); err == nil {
		t.Fatal("directory acknowledgement authorized input before original history")
	}
	if _, err := c.VerifyDirectoryContinuation(context.Background(), domain.NewID(), source, *bound.Effective, ContinueAfterSuccess); err != nil {
		t.Fatal("unchanged original terminal history", err)
	}
	after, _ := json.Marshal(source)
	if string(before) != string(after) || claims != 1 {
		t.Fatal("historical generation rewritten or intent duplicated")
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var call struct {
			Method string
			Params map[string]json.RawMessage
		}
		if json.Unmarshal(line, &call) != nil {
			t.Fatal("invalid wire capture")
		}
		if call.Method == "thread/resume" {
			var cwd string
			var roots []string
			_ = json.Unmarshal(call.Params["cwd"], &cwd)
			_ = json.Unmarshal(call.Params["runtimeWorkspaceRoots"], &roots)
			if cwd != nested || !slices.Equal(roots, []string{root}) {
				t.Fatal("native Resume replaced prepared roots")
			}
		}
	}
}

func TestDirectoryResumeRejectsAuthorityDriftBeforeClaim(t *testing.T) {
	for _, changed := range []string{"model", "provider", "effort", "tier", "approval", "permission", "roots", "missing-original-roots"} {
		t.Run(changed, func(t *testing.T) {
			_, _, source, _ := continuationFixture(t, "ok")
			root := source.Effective.Cwd
			nested := filepath.Join(root, "nested")
			if err := os.Mkdir(nested, 0700); err != nil {
				t.Fatal(err)
			}
			source.Effective.WorkspaceRoots = []string{root}
			settings := ThreadSettings{Model: source.Effective.Model, Provider: source.Effective.Provider, Effort: *source.Effective.Effort, Cwd: nested, WorkspaceRoots: []string{root}, Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite, ApprovalPolicy: string(source.Effective.ApprovalPolicy), ServiceTier: *source.Effective.ServiceTier}}
			switch changed {
			case "model":
				settings.Model = "other"
			case "provider":
				settings.Provider = "other"
			case "effort":
				settings.Effort = "other"
			case "tier":
				settings.Options.ServiceTier = "other"
			case "approval":
				settings.Options.ApprovalPolicy = "never"
			case "permission":
				settings.Options.Permission = domain.PermissionFullAccess
			case "roots":
				settings.WorkspaceRoots = []string{nested}
			case "missing-original-roots":
				source.Effective.WorkspaceRoots = nil
			}
			c, capture := openThreadFixture(t, "thread-continuation-ok")
			claims := 0
			if _, err := c.ResumeDirectory(context.Background(), domain.NewID(), source, settings, func(DirectoryIntent) error { claims++; return nil }); err == nil || claims != 0 {
				t.Fatal("authority drift reached claim", err)
			}
			raw, err := os.ReadFile(capture)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if bytes.Contains(raw, []byte("thread/resume")) {
				t.Fatal("authority drift reached native mutation")
			}
		})
	}
}
