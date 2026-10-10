// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
	"os"
	"path/filepath"
	"testing"
)

func (f *threadFixture) handleSkills(id json.RawMessage, method string, raw json.RawMessage, write func(json.RawMessage, any)) bool {
	if method == "skills/list" || method == "skills/extraRoots/set" {
		if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
			out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				os.Exit(68)
			}
			if err = json.NewEncoder(out).Encode(map[string]any{"method": method, "params": raw}); err != nil {
				os.Exit(69)
			}
			if err = out.Close(); err != nil {
				os.Exit(70)
			}
		}
	}
	switch method {
	case "skills/extraRoots/set":
		var params struct {
			ExtraRoots []string `json:"extraRoots"`
		}
		if domain.Decode(raw, &params) != nil {
			os.Exit(65)
		}
		f.skillRoots = params.ExtraRoots
		write(id, map[string]any{})
		return true
	case "skills/list":
		var params struct {
			Cwds        []string `json:"cwds"`
			ForceReload bool     `json:"forceReload"`
		}
		if domain.Decode(raw, &params) != nil || len(params.Cwds) != 1 {
			os.Exit(66)
		}
		entries := []any{}
		for _, root := range f.skillRoots {
			if _, e := os.ReadFile(filepath.Join(root, "SKILL.md")); e != nil {
				os.Exit(67)
			}
			entries = append(entries, map[string]any{"name": "add-issue", "description": "fixture", "path": filepath.Join(root, "SKILL.md"), "scope": "user", "enabled": true, "pluginId": nil})
		}
		write(id, map[string]any{"data": []any{map[string]any{"cwd": params.Cwds[0], "skills": entries, "errors": []any{}}}})
		return true
	}
	return false
}
func TestStructuredSkillInputUsesOnlyRetainedPackage(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { testStructuredSkillInput(t, mode) })
	}
}
func testStructuredSkillInput(t *testing.T, mode domain.SessionMode) {
	ctx := context.Background()
	home := t.TempDir()
	path := filepath.Join(home, ".agents", "skills", "add-issue")
	os.MkdirAll(path, 0700)
	os.WriteFile(filepath.Join(path, "SKILL.md"), []byte("---\nname: add-issue\ndescription: fixture\n---\nUnique harmless marker\n"), 0600)
	m := skills.Manager{Root: t.TempDir(), Home: home}
	scope := domain.SkillReadRequest{MachineID: domain.NewID(), AgentID: domain.NewID(), ActorID: domain.NewID(), AgentRevision: 1, WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID()}
	list, e := m.List(ctx, scope, nil)
	if e != nil {
		t.Fatal(e)
	}
	entry := list.Entries[0]
	scope.Selections = []domain.SkillBinding{{WorkerDeviceID: entry.WorkerDeviceID, InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: domain.NewID()}}
	if e = m.Prepare(ctx, scope); e != nil {
		t.Fatal(e)
	}
	c, _, _, _ := boundTurnFixture(t, "normal")
	c.skillsRoot = m.Root
	in := input(mode)
	in.Skills = scope.Selections
	result, e := c.StartTurn(ctx, domain.NewID(), domain.NewID(), in)
	if e != nil {
		t.Fatal(e)
	}
	message := nextKind(t, c, MessageCompletedEvent)
	if message.Message == nil || message.Message.Text != in.Prompt {
		t.Fatal("native selected input changed text", message)
	}
	proofs, e := c.SkillInputProofs(ctx)
	if e != nil || len(proofs) != 1 || proofs[0].ID != result.InputID || proofs[0].SkillDigest == ([32]byte{}) {
		t.Fatal("missing immutable native skill proof", proofs, e)
	}
}
func TestNativeHistoryRetainsExplicitSkillIdentity(t *testing.T) {
	id := domain.NewID()
	turn := fixtureTurn(domain.NewID(), TurnCompleted)
	turn["items"] = []any{map[string]any{"type": "userMessage", "id": "user", "clientId": id, "content": []any{map[string]any{"type": "text", "text": "prompt", "text_elements": []any{}}, map[string]any{"type": "skill", "name": "add-issue", "path": "/private/selected/SKILL.md"}}}}
	turn["itemsView"] = "full"
	raw, _ := json.Marshal(map[string]any{"data": []any{turn}, "nextCursor": nil, "backwardsCursor": nil})
	_, proofs, e := decodeLatestTurnInputs(raw)
	if e != nil || len(proofs) != 1 || proofs[0].SkillDigest == ([32]byte{}) {
		t.Fatal(proofs, e)
	}
}

func TestSkillForkCopiesOriginalPackagesAndRewritesProofsBeforeParentDeletion(t *testing.T) {
	for _, withImages := range []bool{false, true} {
		t.Run(fmt.Sprint(withImages), func(t *testing.T) { testSkillForkCopiesOriginalPackages(t, withImages) })
	}
}
func testSkillForkCopiesOriginalPackages(t *testing.T, withImages bool) {
	ctx := context.Background()
	sourceHome := filepath.Join(t.TempDir(), "source-home")
	childHome := filepath.Join(t.TempDir(), "child-home")
	for _, home := range []string{sourceHome, childHome} {
		if err := security.PrivateDir(home); err != nil {
			t.Fatal(err)
		}
	}
	sourceHome, _ = filepath.EvalSymlinks(sourceHome)
	childHome, _ = filepath.EvalSymlinks(childHome)
	user := t.TempDir()
	packagePath := filepath.Join(user, ".agents", "skills", "add-issue")
	os.MkdirAll(packagePath, 0700)
	os.WriteFile(filepath.Join(packagePath, "SKILL.md"), []byte("---\nname: add-issue\ndescription: fixture\n---\nOriginal marker\n"), 0600)
	os.WriteFile(filepath.Join(packagePath, "resource"), []byte("Original resource"), 0600)
	manager := skills.Manager{Root: t.TempDir(), Home: user}
	scope := domain.SkillReadRequest{MachineID: domain.NewID(), AgentID: domain.NewID(), ActorID: domain.NewID(), AgentRevision: 1, WorkerDeviceID: domain.NewID(), WorkerInstanceID: domain.NewID()}
	inventory, err := manager.List(ctx, scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry := inventory.Entries[0]
	scope.Selections = []domain.SkillBinding{{WorkerDeviceID: entry.WorkerDeviceID, InventoryID: entry.InventoryID, SkillID: entry.SkillID, ContentRevision: entry.ContentRevision, SnapshotID: domain.NewID()}}
	if err := manager.Prepare(ctx, scope); err != nil {
		t.Fatal(err)
	}
	selected, err := manager.CopyToRuntime(ctx, scope.Selections, sourceHome)
	if err != nil {
		t.Fatal(err)
	}
	imageRoot := t.TempDir()
	imageMachine := scope.MachineID
	imageClient := &Client{imageRoot: imageRoot, imageMachine: imageMachine}
	extra := []any{}
	if withImages {
		ref := stageImage(t, imageRoot, imageMachine)
		extra = append(extra, map[string]any{"type": "localImage", "path": filepath.Join(imageRoot, "image-inputs", string(ref.ID)+".data")})
	}
	id, turnID := domain.NewID(), domain.NewID()
	literalUserText := "<skill><path>" + selected[0].Path + "</path></skill> literal user quotation"
	turn, _ := json.Marshal(map[string]any{"id": turnID, "status": "completed", "itemsView": "full", "items": []any{map[string]any{"id": "user", "type": "userMessage", "clientId": id, "content": append(append([]any{map[string]any{"type": "text", "text": literalUserText, "text_elements": []any{}}}, extra...), map[string]any{"type": "skill", "name": "add-issue", "path": selected[0].Path})}}})
	_, original, err := decodeLatestTurnInputs(marshalForkPage([]json.RawMessage{turn}), imageClient.nativeImageInput)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sourceHome, "sessions", "source.jsonl")
	if err := security.PrivateDir(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	if err := security.WriteAtomic(path, append(turn, '\n')); err != nil {
		t.Fatal(err)
	}
	digest, err := forkRolloutDigest(ctx, sourceHome, path)
	if err != nil {
		t.Fatal(err)
	}
	source := &ForkSource{imageRoot: imageRoot, imageMachine: imageMachine, home: sourceHome, path: path, fileDigest: digest, turns: []json.RawMessage{turn}, checkpoint: ContinuationCheckpoint{Inputs: original}}
	child, err := source.RehomeSkills(ctx, childHome)
	if err != nil {
		t.Fatal(err)
	}
	if child.checkpoint.Inputs[0].PromptDigest != original[0].PromptDigest || child.checkpoint.Inputs[0].SkillDigest == original[0].SkillDigest {
		t.Fatal("invalid remapped native proof")
	}
	// Compare decoded values: JSON escapes Windows path separators. Literal
	// text remains exact while only the typed skill node moves to the child.
	var childTurn struct {
		Items []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Path string `json:"path"`
			} `json:"content"`
		} `json:"items"`
	}
	if err := json.Unmarshal(child.turns[0], &childTurn); err != nil || len(childTurn.Items) != 1 || childTurn.Items[0].Type != "userMessage" || len(childTurn.Items[0].Content) != 2+len(extra) {
		t.Fatal("invalid child user message", err)
	}
	content := childTurn.Items[0].Content
	if content[0].Type != "text" || content[0].Text != literalUserText {
		t.Fatal("literal user path was rewritten")
	}
	skill := content[len(content)-1]
	if skill.Type != "skill" || skill.Path != filepath.Join(childHome, "selected-skills", string(entry.SkillID), "SKILL.md") {
		t.Fatal("typed skill path did not retain its child package")
	}
	// Source inventory edits and parent deletion cannot replace child resources.
	os.RemoveAll(sourceHome)
	os.RemoveAll(manager.Root)
	os.RemoveAll(user)
	path = filepath.Join(childHome, "selected-skills", string(entry.SkillID), "resource")
	copied, err := os.ReadFile(path)
	if err != nil || string(copied) != "Original resource" {
		t.Fatal("child depends on parent resource", err)
	}
	_, inputs, err := decodeLatestTurnInputs(marshalForkPage(child.turns), imageClient.nativeImageInput)
	if err != nil || inputs[0] != child.checkpoint.Inputs[0] {
		t.Fatal("child history proof changed", err)
	}
}
