package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func localExecutionFixture(t *testing.T) (*Manager, PrepareRequest, Manifest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(root)
	input.Type = domain.Local
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return m, input, manifest
}

func TestLocalExecutionPreservesCurrentTreeAndIndependentSharing(t *testing.T) {
	m, input, manifest := localExecutionFixture(t)
	root := manifest.PrimaryPath
	// User edits after preparation remain authoritative at first execution.
	gitTest(t, root, "switch", "-c", "user-selected")
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("commit"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "new.txt")
	gitTest(t, root, "commit", "-m", "user changes after preparation")
	head := gitTest(t, root, "rev-parse", "HEAD")
	dirty := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(dirty, []byte("retain dirty checkout"), 0600); err != nil {
		t.Fatal(err)
	}
	first := ExecutionPredecessor{domain.NewID(), domain.NewID()}
	lease, err := m.ClaimFirstExecution(context.Background(), first.JobID, first.ExecutionID, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	shared := input
	shared.SessionID = domain.NewID()
	sharedManifest, err := m.Prepare(context.Background(), shared)
	if err != nil {
		t.Fatal(err)
	}
	other, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), shared, sharedManifest)
	if err != nil {
		t.Fatal("explicit Local sharing was refused", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	inspection, err := m.InspectClosedExecution(context.Background(), first, input, manifest)
	if err != nil {
		t.Fatal("Local closed ownership could not be inspected", err)
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), first, input, manifest)
	if err != nil {
		t.Fatal("Local continuation refused retained changes", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	if gitTest(t, root, "rev-parse", "HEAD") != head || gitTest(t, root, "branch", "--show-current") != "user-selected" {
		t.Fatal("Local execution changed user HEAD/branch")
	}
	if raw, err := os.ReadFile(dirty); err != nil || string(raw) != "retain dirty checkout" {
		t.Fatal("Local execution lost dirty files", err)
	}
	if err := m.cleanup(context.Background(), filepath.Join(m.Root, "workspaces", string(shared.SessionID)), sharedManifest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Fatal("Local metadata cleanup removed original checkout", err)
	}
}

func TestLocalExecutionRejectsUnprovenOriginAndAdministrativeReplacement(t *testing.T) {
	for _, scenario := range []string{"origin", "missing-identity", "foreign-identity", "replaced-git", "missing-git"} {
		t.Run(scenario, func(t *testing.T) {
			m, input, manifest := localExecutionFixture(t)
			switch scenario {
			case "origin":
				input.OriginMachineID = domain.NewID()
			case "missing-identity":
				manifest.Repositories[0].LocalIdentityDigest = ""
			case "foreign-identity":
				manifest.Repositories[0].LocalIdentityDigest = string(make([]byte, 64))
			case "replaced-git", "missing-git":
				metadata := filepath.Join(manifest.PrimaryPath, ".git")
				if err := os.Rename(metadata, metadata+".held"); err != nil {
					t.Fatal(err)
				}
				if scenario == "replaced-git" {
					foreign := repository(t)
					if err := os.WriteFile(metadata, []byte("gitdir: "+filepath.Join(foreign, ".git")+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest); err == nil {
				t.Fatal("unproven Local identity allowed execution")
			}
			if _, err := os.Stat(manifest.PrimaryPath); err != nil {
				t.Fatal("failed Local claim removed checkout", err)
			}
		})
	}
}

func TestLocalLinkedCheckoutRejectsChangedRegistration(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(parent, "existing-linked")
	gitTest(t, root, "worktree", "add", "--detach", linked, "HEAD")
	input, _ := requestFor(linked)
	input.Type = domain.Local
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	first := closeFirstExecution(t, m, input, manifest)
	admin := gitTest(t, linked, "rev-parse", "--absolute-git-dir")
	backlink := filepath.Join(admin, "gitdir")
	original, err := os.ReadFile(backlink)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(parent, "foreign")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backlink, []byte(filepath.Join(foreign, ".git")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), first, input, manifest); err == nil {
		t.Fatal("redirected Local registration authorized continuation")
	}
	if err := os.WriteFile(backlink, original, 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), first, input, manifest)
	if err != nil {
		t.Fatal("restored Local registration refused", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
}
