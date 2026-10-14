package workspace

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func budgetRequest(count int, url string, kind domain.WorkspaceType) PrepareRequest {
	request := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: kind, Repositories: []RepositorySpec{}}
	request.OriginMachineID = request.MachineID
	for i := 0; i < count; i++ {
		spec := RepositorySpec{ID: domain.NewID(), Base: domain.Reference{Type: domain.LocalBranch, Name: "main"}, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}, AutoFetch: true, SourceKind: RemoteCloneSource, RemoteURL: url, DirectoryName: "repository"}
		if kind == domain.Local {
			spec.SourceKind, spec.Checkout, spec.DirectoryName = LocalCheckoutSource, "/work/checkout", ""
		}
		request.Repositories = append(request.Repositories, spec)
	}
	if count > 0 {
		request.PrimaryRepository = request.Repositories[0].ID
	}
	return request
}

// A manifest as the Worker publishes it must never exceed the admission bound.
func realisticManifest(request PrepareRequest, root string) Manifest {
	digest := strings.Repeat("a", 64)
	manifest := Manifest{Version: 1, SessionID: request.SessionID, MachineID: request.MachineID, Type: request.Type, State: Ready, InputDigest: digest, CreatedAt: time.Now().UTC(), Repositories: []PreparedRepository{}}
	for _, spec := range request.Repositories {
		prepared := PreparedRepository{ID: spec.ID, DirectoryName: spec.DirectoryName, SourceKind: spec.SourceKind, RemoteURL: spec.RemoteURL, Base: spec.Base, Starting: spec.Starting, BaseCommit: strings.Repeat("b", 40), StartingCommit: strings.Repeat("b", 40), Owned: request.Type == domain.Worktree}
		if request.Type == domain.Worktree {
			prepared.Path = root + "/workspaces/" + string(request.SessionID) + "/" + RepositoryDirectory(spec.ID, spec.DirectoryName)
			prepared.Source = prepared.Path
			prepared.CloneRootDigest, prepared.CloneIdentityDigest = digest, digest
			manifest.ManagedRootDigest = digest
		} else {
			prepared.Path, prepared.Source = spec.Checkout, spec.Checkout
			prepared.LocalIdentityDigest = digest
		}
		manifest.Repositories = append(manifest.Repositories, prepared)
	}
	manifest.PrimaryPath = manifest.Repositories[0].Path
	return manifest
}

func TestMaxManifestBytesBoundsPublishedManifest(t *testing.T) {
	url := "https://github.com/fixture/" + strings.Repeat("a", 4028-len("https://github.com/fixture/.git")) + ".git"
	for _, tc := range []struct {
		name  string
		count int
		url   string
		kind  domain.WorkspaceType
	}{
		{"single remote", 1, "https://github.com/fixture/repo.git", domain.Worktree},
		{"maximum remote URLs", 100, url, domain.Worktree},
		{"local checkouts", 3, "https://github.com/fixture/repo.git", domain.Local},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := budgetRequest(tc.count, tc.url, tc.kind)
			bound, err := request.MaxManifestBytes()
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(realisticManifest(request, "/home/user/.local/share/delidev"))
			if bound < len(raw) {
				t.Fatalf("bound %d is below the published manifest %d", bound, len(raw))
			}
			if bound > 2*len(raw)+8192 {
				t.Fatalf("bound %d is not tight against the published manifest %d", bound, len(raw))
			}
		})
	}
}

func TestMaxManifestBytesGeneralChat(t *testing.T) {
	request := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
	bound, err := request.MaxManifestBytes()
	if err != nil || bound <= 0 || bound > 4096 {
		t.Fatal("unexpected General Chat manifest bound", bound, err)
	}
}

func TestMaxManifestBytesReservesUnresolvedReferenceMaximum(t *testing.T) {
	request := budgetRequest(1, "https://github.com/fixture/repo.git", domain.Worktree)
	request.Repositories[0].Starting = domain.Reference{}
	request.Repositories[0].Base = domain.Reference{}
	bound, err := request.MaxManifestBytes()
	if err != nil {
		t.Fatal(err)
	}
	// A Worker may resolve the longest accepted branch and remote names, and
	// every '<' or '>' expands to six bytes after JSON escaping.
	resolved := domain.Reference{Type: domain.RemoteBranch, Name: strings.Repeat(">", 1024), Remote: strings.Repeat("&", 256)}
	if resolved.Validate(false) != nil {
		t.Fatal("fixture reference is not an accepted maximum")
	}
	request.Repositories[0].Starting, request.Repositories[0].Base = resolved, resolved
	raw, _ := json.Marshal(realisticManifest(request, "/home/user/.local/share/delidev"))
	request.Repositories[0].Starting, request.Repositories[0].Base = domain.Reference{}, domain.Reference{}
	if bound < len(raw) {
		t.Fatalf("bound %d is below the manifest with maximum resolved references %d", bound, len(raw))
	}
}

// The Worker root repeats in the primary path and every repository path and
// source, so its JSON-escaped size must stay inside the admission bound.
func TestMaxManifestBytesReservesEscapedWorkerRoot(t *testing.T) {
	request := budgetRequest(40, "https://github.com/fixture/repo.git", domain.Worktree)
	bound, err := request.MaxManifestBytes()
	if err != nil {
		t.Fatal(err)
	}
	for name, root := range map[string]string{
		"html sensitive": strings.Repeat("<", ManifestWorkerRootReserveBytes/6),
		"ampersand":      strings.Repeat("&", ManifestWorkerRootReserveBytes/6),
		"windows":        strings.Repeat(`\`, ManifestWorkerRootReserveBytes/2),
		"plain":          strings.Repeat("r", 512),
	} {
		raw, _ := json.Marshal(realisticManifest(request, root))
		if bound < len(raw) {
			t.Fatalf("%s root: bound %d is below the published manifest %d", name, bound, len(raw))
		}
	}
}

// Admission reserves ManifestWorkerRootReserveBytes, so a Worker must refuse a
// data root whose JSON-encoded length exceeds it.
func TestWorkerRootEncodedLengthIsEnforced(t *testing.T) {
	for name, tc := range map[string]struct {
		root string
		ok   bool
	}{
		"plain at limit":      {strings.Repeat("r", ManifestWorkerRootReserveBytes), true},
		"plain over":          {strings.Repeat("r", ManifestWorkerRootReserveBytes+1), false},
		"escaped at limit":    {strings.Repeat("<", ManifestWorkerRootReserveBytes/6), true},
		"escaped over":        {strings.Repeat("<", ManifestWorkerRootReserveBytes/6+1), false},
		"separators at limit": {strings.Repeat(`\`, ManifestWorkerRootReserveBytes/2), true},
		"separators over":     {strings.Repeat(`\`, ManifestWorkerRootReserveBytes/2+1), false},
	} {
		if err := ValidateWorkerRoot(tc.root); (err == nil) != tc.ok {
			t.Fatalf("%s: accepted=%v want %v (%v)", name, err == nil, tc.ok, err)
		}
	}
	m := &Manager{Root: "/" + strings.Repeat("<", ManifestWorkerRootReserveBytes/6+1)}
	if err := m.initialize(); err == nil {
		t.Fatal("initialize accepted an over-long encoded root")
	}
}
