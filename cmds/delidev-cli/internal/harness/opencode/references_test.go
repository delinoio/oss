package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func referenceFixture(t *testing.T) apiSessionConfig {
	t.Helper()
	c := fixtureOwnedAPIConfig(t)
	c.NativeRoot = c.Workspace
	for range 2 {
		path, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		c.References = append(c.References, WorkspaceReference{domain.NewID(), path})
	}
	return c
}

func TestWorkspaceReferencesRefuseUnownedAndAmbiguousScope(t *testing.T) {
	for _, mode := range []string{"duplicate-id", "duplicate-path", "primary", "runtime", "ancestor", "descendant", "relative", "glob", "substitution", "missing", "linked", "project-json", "project-jsonc", "project-directory", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			c := referenceFixture(t)
			switch mode {
			case "duplicate-id":
				c.References[1].RepositoryID = c.References[0].RepositoryID
			case "duplicate-path":
				c.References[1].Path = c.References[0].Path
			case "primary":
				c.References[0].Path = c.Workspace
			case "runtime":
				c.References[0].Path = filepath.Dir(c.Probe.Home)
			case "ancestor":
				c.References[0].Path = filepath.Dir(c.Workspace)
			case "descendant":
				c.References[0].Path = filepath.Join(c.Workspace, "child")
			case "relative":
				c.References[0].Path = "relative"
			case "glob":
				c.References[0].Path += "*"
			case "substitution":
				c.References[0].Path += "{env:PRIVATE}"
			case "missing":
				if err := os.Remove(c.References[0].Path); err != nil {
					t.Fatal(err)
				}
			case "linked":
				path := c.References[0].Path
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(c.References[1].Path, path); err != nil {
					t.Skip("symbolic links unavailable")
				}
			case "project-json", "project-jsonc":
				name := "opencode.json"
				if mode == "project-jsonc" {
					name += "c"
				}
				if err := os.WriteFile(filepath.Join(c.Workspace, name), []byte(`{"references":{"foreign":"https://invalid.example/repo"}}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "project-directory":
				if err := os.Mkdir(filepath.Join(c.Workspace, ".opencode"), 0700); err != nil {
					t.Fatal(err)
				}
			case "overflow":
				c.References = make([]WorkspaceReference, 100)
			}
			if _, _, err := prepareAPISession(c); err == nil {
				t.Fatal("invalid reference scope acquired configuration")
			}
			if _, err := os.Lstat(filepath.Join(c.Probe.Home, "opencode.json")); !os.IsNotExist(err) {
				t.Fatal("invalid scope wrote native config")
			}
		})
	}
}

func TestWorkspaceReferencesKeepPrivateConfigurationAndOriginalOrder(t *testing.T) {
	c := referenceFixture(t)
	want := slices.Clone(c.References)
	_, p, err := prepareAPISession(c)
	if err != nil {
		t.Fatal(err)
	}
	c.References[0].Path = "changed caller array"
	if !slices.Equal(p.References, want) {
		t.Fatal("caller mutated retained scope")
	}
	raw, err := security.ReadPrivate(p.ReferencePath, maxHTTPBody)
	if err != nil || bytes.Contains(raw, []byte(c.Token)) {
		t.Fatal("reference bridge lost privacy")
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(raw, &document) != nil || len(document) != 2 || document["references"] == nil || document["$schema"] == nil {
		t.Fatal("bridge gained unrelated native settings")
	}
	if p.inspectReferences() != nil {
		t.Fatal("original bridge was not reusable")
	}
	for _, agent := range []PrimaryAgent{BuildAgent, PlanAgent} {
		expected, err := expectedPrimaryAgent(agent, filepath.Dir(c.Probe.Home), c.NativeRoot, want...)
		if err != nil {
			t.Fatal(err)
		}
		agents := fixtureAgentInventory(filepath.Dir(c.Probe.Home))
		index := 0
		if agent == PlanAgent {
			index = 1
		}
		agents[index] = expected
		encoded, _ := json.Marshal(agents)
		if validatePrimaryAgent(encoded, agent, filepath.Dir(c.Probe.Home), c.NativeRoot, want...) != nil {
			t.Fatal("valid native reference policy rejected")
		}
		reversed := []WorkspaceReference{want[1], want[0]}
		if validatePrimaryAgent(encoded, agent, filepath.Dir(c.Probe.Home), c.NativeRoot, reversed...) == nil {
			t.Fatal("reordered native policy accepted")
		}
		if validatePrimaryAgent(encoded, agent, filepath.Dir(c.Probe.Home), c.NativeRoot) == nil {
			t.Fatal("additional native scope accepted by older profile")
		}
	}
	if err := os.WriteFile(p.ReferencePath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if p.inspectReferences() == nil {
		t.Fatal("changed reference bridge accepted")
	}
}

func TestCheckpointReferencesRequireSnapshotAndOriginalScope(t *testing.T) {
	value := snapshotMetadataFixture(t)
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value.References = []WorkspaceReference{{domain.NewID(), path}}
	if !validCheckpointReferences(value) {
		t.Fatal("valid retained context rejected")
	}
	value.Snapshot = nil
	if validCheckpointReferences(value) || checkpointReplacementProfile(value) == nil {
		t.Fatal("references bypassed native snapshot evidence")
	}
}

func TestWorkspaceReferenceChangesLatchBeforeFurtherMutations(t *testing.T) {
	for _, mode := range []string{"file", "parent-link", "missing-root", "project-source"} {
		t.Run(mode, func(t *testing.T) {
			c := referenceFixture(t)
			_, p, err := prepareAPISession(c)
			if err != nil {
				t.Fatal(err)
			}
			s := &sessionAPI{apiProfile: p, apiVerified: true}
			switch mode {
			case "file":
				err = os.Remove(p.ReferencePath)
			case "parent-link":
				parent := filepath.Dir(p.ReferencePath)
				target := filepath.Join(t.TempDir(), "moved-config")
				if err = os.Rename(parent, target); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink(target, parent); err != nil {
					t.Skip("symbolic links unavailable")
				}
			case "missing-root":
				err = os.Remove(c.References[0].Path)
			case "project-source":
				err = os.WriteFile(filepath.Join(c.Workspace, "opencode.jsonc"), []byte(`{}`), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.verifyInstructions(context.Background(), "reference-test") == nil || s.apiVerified || s.problem == nil {
				t.Fatal("changed scope remained usable")
			}
		})
	}
}

func TestOriginalCheckpointCannotAcquireAdditionalReferencesDuringInspectionOrResume(t *testing.T) {
	r, c, _ := checkpointStageFixture(t)
	ctx := context.Background()
	if InspectReplacementWorkspace(ctx, r.source.RuntimeHome, r.raw, r.ref, r.source.Workspace, r.source.NativeRoot) != nil {
		t.Fatal("original checkpoint lost its context")
	}
	path, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.References = []WorkspaceReference{{domain.NewID(), path}}
	if InspectReplacementWorkspace(ctx, r.source.RuntimeHome, r.raw, r.ref, r.source.Workspace, r.source.NativeRoot, c.References...) == nil {
		t.Fatal("inspection changed original context")
	}
	claims := 0
	c.Claim = func(context.Context, SessionClaim) error { claims++; return nil }
	if api, err := OpenResumedAPI(ctx, c, r.source.RuntimeHome, r.raw, r.ref, r.request, r.previousAgent, false); err == nil || api != nil || claims != 0 {
		t.Fatal("changed references reached a resume claim")
	}
}
