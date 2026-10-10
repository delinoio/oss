// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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
	c, capture := openThreadFixture(t, "thread-directory-ok")
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
	if err := c.RequireDirectoryQuiescence(context.Background(), domain.NewID()); err != nil {
		t.Fatal("empty original bound background inventory", err)
	}
	reload, err := c.ReadDirectoryReloadEvidence(context.Background(), domain.NewID(), bound)
	if err != nil || len(reload.ConfigDigest) != 64 || reload.Instructions == nil {
		t.Fatal("private complete reload inventory", err)
	}
	fixtureSignal(t, c, "history", map[string]any{"page": page})
	if _, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), domain.SessionInput{Prompt: "blocked", Mode: domain.ExecuteMode}); err == nil {
		t.Fatal("directory acknowledgement authorized input before original history")
	}
	if _, err := c.VerifyDirectoryContinuation(context.Background(), domain.NewID(), source, *bound.Effective, ContinueAfterSuccess); err != nil {
		t.Fatal("unchanged original terminal history", err)
	}
	retained := source
	retained.Effective = *bound.Effective
	continued, _ := openThreadFixture(t, "thread-directory-ok")
	continuedBound, continuedErr := continued.ResumeDirectoryContinuation(context.Background(), domain.NewID(), retained, settings)
	if continuedErr != nil || continuedBound.Effective == nil || continuedBound.Effective.Cwd != nested {
		t.Fatal("retained directory did not persist", continuedErr)
	}
	fixtureSignal(t, continued, "history", map[string]any{"page": page})
	if _, err := continued.VerifyContinuation(context.Background(), domain.NewID(), retained, ContinueAfterSuccess); err != nil {
		t.Fatal("retained selected generation history", err)
	}
	continuedReload, err := continued.ReadDirectoryReloadEvidence(context.Background(), domain.NewID(), continuedBound)
	if err != nil || !reflect.DeepEqual(reload, continuedReload) {
		t.Fatal("destination reload proof drift", err)
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
	for _, changed := range []string{"model", "provider", "effort", "tier", "approval", "permission", "roots"} {
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

func TestDirectoryBackgroundInventoryRequiresCompleteEmptyProof(t *testing.T) {
	for _, fixture := range []struct {
		raw   string
		valid bool
	}{
		{`{"data":[],"nextCursor":null}`, true},
		{`{"data":[{"processId":"9"}],"nextCursor":null}`, false},
		{`{"data":[],"nextCursor":"more"}`, false},
		{`{"data":[],"nextCursor":""}`, false},
		{`{"data":[]}`, false}, {`{"data":null,"nextCursor":null}`, false},
		{`{"nextCursor":null}`, false}, {`null`, false},
	} {
		if (emptyDirectoryBackgroundInventory(json.RawMessage(fixture.raw)) == nil) != fixture.valid {
			t.Fatal("incomplete background proof accepted", fixture.raw)
		}
	}
}

func TestDirectoryInstructionEvidenceRejectsLinksAndChangedContent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := directoryInstructionEvidence([]string{path})
	if err != nil || len(first) != 1 {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := directoryInstructionEvidence([]string{path})
	if err != nil || first[0].Digest == second[0].Digest {
		t.Fatal("instruction change lost", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{nil, {link}, {path, path}, {root}, {filepath.Join(root, "missing")}} {
		if _, err := directoryInstructionEvidence(paths); err == nil {
			t.Fatal("unproven instruction source accepted", paths)
		}
	}
	if empty, err := directoryInstructionEvidence([]string{}); err != nil || empty == nil {
		t.Fatal("proven empty source inventory", err)
	}
}

func (f *threadFixture) handleDirectory(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if !strings.HasPrefix(f.mode, "thread-directory-") {
		return false
	}
	switch method {
	case "thread/backgroundTerminals/list":
		var params struct {
			ThreadID domain.ID `json:"threadId"`
			Limit    uint32    `json:"limit"`
		}
		if domain.Decode(raw, &params) != nil || f.thread == nil || params.ThreadID != f.thread["id"] || params.Limit != 1 {
			os.Exit(74)
		}
		write(id, map[string]any{"data": []any{}, "nextCursor": nil})
		return true
	case "config/read":
		var params struct {
			Cwd           string `json:"cwd"`
			IncludeLayers bool   `json:"includeLayers"`
		}
		if domain.Decode(raw, &params) != nil || f.thread == nil || params.Cwd != f.thread["cwd"] || !params.IncludeLayers {
			os.Exit(75)
		}
		write(id, map[string]any{"config": map[string]any{"model": "fixture-model"}, "origins": map[string]any{}, "layers": []any{}})
		return true
	}
	return false
}

func TestDirectorySettingsPreservesImplicitOriginalWriteRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	source := EffectiveSettings{Model: "model", Provider: APIProvider, Cwd: root, ApprovalPolicy: ApprovalOnRequest, ApprovalsReviewer: "user", Sandbox: Sandbox{Type: WorkspaceWrite}}
	selected := source
	selected.Cwd = nested
	selected.WorkspaceRoots = []string{root}
	selected.Sandbox.WritableRoots = []string{root}
	if !DirectorySettingsEqual(source, selected) {
		t.Fatal("implicit original cwd write root lost")
	}
	changed := selected
	changed.Sandbox.WritableRoots = nil
	if DirectorySettingsEqual(source, changed) {
		t.Fatal("selected cwd narrowed original write root without proof")
	}
	changed = selected
	changed.WorkspaceRoots = []string{nested}
	if DirectorySettingsEqual(source, changed) {
		t.Fatal("native roots retargeted")
	}
	changed = selected
	changed.Sandbox.NetworkAccess = true
	if DirectorySettingsEqual(source, changed) {
		t.Fatal("new network permission acquired")
	}
}

func TestDirectoryInstructionChangeDuringResumeIsRefused(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("Original instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Hour)
	after := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, after, after); err != nil {
		t.Fatal(err)
	}
	if _, err := directoryInstructionEvidenceBefore([]string{path}, before); err == nil {
		t.Fatal("post-resume instruction write accepted")
	}
	if err := os.Chtimes(path, before, before); err != nil {
		t.Fatal(err)
	}
	if _, err := directoryInstructionEvidenceBefore([]string{path}, after); err != nil {
		t.Fatal("stable earlier source refused", err)
	}
}

func TestDirectoryConfigurationLayerChangeDuringResumeIsRefused(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.toml")
	if err := os.WriteFile(path, []byte("model = 'fixture'"), 0600); err != nil {
		t.Fatal(err)
	}
	before, after := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	if err := os.Chtimes(path, before, before); err != nil {
		t.Fatal(err)
	}
	layer := func(name map[string]any, config map[string]any) []json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"name": name, "version": "retained-native-layer", "config": config})
		return []json.RawMessage{raw}
	}
	stable := layer(map[string]any{"type": "user", "file": path, "profile": nil}, map[string]any{"model": "fixture"})
	if err := directoryConfigSourcesBefore(stable, after); err != nil {
		t.Fatal("stable source refused", err)
	}
	if err := os.Chtimes(path, after, after); err != nil {
		t.Fatal(err)
	}
	if err := directoryConfigSourcesBefore(stable, before); err == nil {
		t.Fatal("post-Resume config write accepted")
	}
	missing := filepath.Join(root, "missing.toml")
	if err := directoryConfigSourcesBefore(layer(map[string]any{"type": "system", "file": missing}, map[string]any{}), after); err != nil {
		t.Fatal("native proven absent default refused", err)
	}
	if err := directoryConfigSourcesBefore(layer(map[string]any{"type": "system", "file": missing}, map[string]any{"model": "foreign"}), after); err == nil {
		t.Fatal("missing configured source accepted")
	}
	if err := directoryConfigSourcesBefore(layer(map[string]any{"type": "unknown"}, map[string]any{}), after); err == nil {
		t.Fatal("unknown layer accepted")
	}
}
