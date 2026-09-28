package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func projectInstructionFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "nested", "work")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	return root, directory
}

func writeProjectInstruction(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectInstructionsPreserveNativeFamilyAndAncestorOrder(t *testing.T) {
	for _, selection := range []string{"agents", "context", "empty-agents", "none", "projectless"} {
		t.Run(selection, func(t *testing.T) {
			root, directory := projectInstructionFixture(t)
			writeProjectInstruction(t, filepath.Join(root, "CLAUDE.md"), "disabled compatibility instruction")
			var want []string
			switch selection {
			case "agents", "empty-agents":
				for _, dir := range []string{directory, root} {
					path := filepath.Join(dir, "AGENTS.md")
					text := "private Unicode 지침\r\n"
					if selection == "empty-agents" {
						text = ""
					}
					writeProjectInstruction(t, path, text)
					want = append(want, path)
				}
				writeProjectInstruction(t, filepath.Join(directory, "CONTEXT.md"), "ignored fallback")
			case "context":
				for _, dir := range []string{directory, filepath.Dir(directory), root} {
					path := filepath.Join(dir, "CONTEXT.md")
					writeProjectInstruction(t, path, "private fallback")
					want = append(want, path)
				}
			case "projectless":
				writeProjectInstruction(t, filepath.Join(root, "AGENTS.md"), "unrelated ancestor")
				path := filepath.Join(directory, "AGENTS.md")
				writeProjectInstruction(t, path, "original General Chat instructions")
				want = []string{path}
				root = filepath.VolumeName(root) + string(filepath.Separator)
			}
			project, err := collectProjectInstructions(directory, root)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, source := range project.Sources {
				got = append(got, source.Path)
			}
			if !slices.Equal(got, want) || project.inspect() != nil {
				t.Fatal("native instruction selection/order changed")
			}
		})
	}
}

func TestProjectInstructionBoundsAndUnsafeSources(t *testing.T) {
	for _, change := range []string{"oversized", "aggregate", "invalid-utf8", "nul", "directory", "symlink", "outside", "depth"} {
		t.Run(change, func(t *testing.T) {
			root, directory := projectInstructionFixture(t)
			path := filepath.Join(directory, "AGENTS.md")
			switch change {
			case "oversized":
				writeProjectInstruction(t, path, strings.Repeat("x", maxProjectInstructionBytes+1))
			case "aggregate":
				writeProjectInstruction(t, path, strings.Repeat("x", maxProjectInstructionBytes))
				writeProjectInstruction(t, filepath.Join(root, "AGENTS.md"), "x")
			case "invalid-utf8":
				writeProjectInstruction(t, path, "invalid\xff")
			case "nul":
				writeProjectInstruction(t, path, "invalid\x00")
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(t.TempDir(), "private.txt")
				writeProjectInstruction(t, target, "outside private instructions")
				if err := os.Symlink(target, path); err != nil {
					t.Skip("native symlink privilege unavailable")
				}
			case "outside":
				root, _ = filepath.EvalSymlinks(t.TempDir())
			case "depth":
				for i := 0; i < maxProjectInstructionAncestors; i++ {
					directory = filepath.Join(directory, "d")
				}
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := collectProjectInstructions(directory, root); err == nil || strings.Contains(err.Error(), path) {
				t.Fatal("unsafe project instructions accepted or disclosed")
			}
		})
	}
}

func TestProjectInstructionSelectionComposesBeforePrivateAgentTemplates(t *testing.T) {
	config := fixtureOwnedAPIConfig(t)
	config.NativeRoot, config.Workspace = projectInstructionFixture(t)
	projectPath := filepath.Join(config.NativeRoot, "AGENTS.md")
	writeProjectInstruction(t, projectPath, "original project instruction")
	config.Instructions = privateInstructionsFixture
	env, profile, err := prepareAPISession(config)
	if err != nil {
		t.Fatal(err)
	}
	value, err := profile.config()
	if err != nil || !slices.Equal(value["instructions"].([]string), []string{projectPath, profile.InstructionsPath}) || !slices.Contains(env, "OPENCODE_DISABLE_PROJECT_CONFIG=true") || !slices.Contains(env, "OPENCODE_DISABLE_CLAUDE_CODE=true") {
		t.Fatal("project instructions broadened native configuration or changed order")
	}
	raw, _ := json.Marshal(profile)
	if string(raw) != "{}" || strings.Contains(strings.Join(env, "\n"), "original project instruction") {
		t.Fatal("private source contents entered serialization or environment")
	}
	// An unrelated file change cannot invalidate the original selected inputs.
	writeProjectInstruction(t, filepath.Join(config.Workspace, "unrelated.txt"), "unrelated workspace update")
	if err := profile.inspectInstructions(); err != nil {
		t.Fatal(err)
	}
}

func TestChangedProjectInstructionsBlockBeforeNativeMutation(t *testing.T) {
	for _, stage := range []string{"initialization", "creation", "input"} {
		for _, change := range []string{"bytes", "removed", "new-preferred", "new-ancestor"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				config := fixtureOwnedAPIConfig(t)
				config.NativeRoot, config.Workspace = projectInstructionFixture(t)
				filename := "AGENTS.md"
				if change == "new-preferred" {
					filename = "CONTEXT.md"
				}
				path := filepath.Join(config.Workspace, filename)
				writeProjectInstruction(t, path, "private original project instructions")
				_, profile, err := prepareAPISession(config)
				if err != nil {
					t.Fatal(err)
				}
				f := newSessionFixture(t)
				f.api.apiProfile, f.api.apiVerified, f.api.rejectionPolicy = profile, true, profile.Rejection
				if stage == "input" {
					if _, err := f.api.create(context.Background(), domain.NewID(), config.Settings); err != nil {
						t.Fatal(err)
					}
				}
				changed := path
				switch change {
				case "bytes":
					writeProjectInstruction(t, path, "private changed project instructions")
				case "removed":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "new-preferred":
					changed = filepath.Join(config.NativeRoot, "AGENTS.md")
					writeProjectInstruction(t, changed, "private preferred selection")
				case "new-ancestor":
					changed = filepath.Join(config.NativeRoot, "AGENTS.md")
					writeProjectInstruction(t, changed, "private additional ancestor")
				}
				before := f.postCount()
				var attempted error
				switch stage {
				case "initialization":
					attempted = f.api.verifyAPIProfile(context.Background())
				case "creation":
					_, attempted = f.api.create(context.Background(), domain.NewID(), config.Settings)
				case "input":
					_, attempted = f.api.submitText(context.Background(), domain.NewID(), "private input")
				}
				if attempted == nil || f.api.problem == nil || f.api.apiVerified || f.postCount() != before || len(f.claims) != before {
					t.Fatal("changed project instructions reached native work")
				}
				_ = os.Remove(changed)
				writeProjectInstruction(t, path, "private original project instructions")
				if f.api.verifyAPIProfile(context.Background()) == nil {
					t.Fatal("restoration erased original instruction contradiction")
				}
				if strings.Contains(f.logs.String(), path) || strings.Contains(f.logs.String(), "private original") || strings.Contains(f.logs.String(), "private changed") {
					t.Fatal("project paths or instructions entered diagnostics")
				}
			})
		}
	}
}
