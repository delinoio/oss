package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func workspaceReadFixture(input PrepareRequest, manifest Manifest, operation domain.WorkspaceReadOperation, path string) ReadRequest {
	return ReadRequest{ID: domain.NewID(), Deadline: time.Now().Add(15 * time.Second), Preparation: input, Manifest: manifest, Query: domain.WorkspaceReadQuery{Operation: operation, Path: path, RepositoryID: input.PrimaryRepository}}
}

func TestWorkspaceReadDuringExecutionAndPreviewBounds(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	claim, err := m.readExecutionClaim(input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"plain.txt": "<script>alert(1)</script>\n한국어", "binary": "\x00hidden", "invalid": "\xffhidden", "large": strings.Repeat("a", domain.WorkspacePreviewLimit-1) + "한글"} {
		if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := m.ReadWorkspace(context.Background(), workspaceReadFixture(input, manifest, domain.WorkspaceFile, name))
		if err != nil {
			t.Fatal(name, err)
		}
		switch name {
		case "plain.txt":
			if result.Text != content || result.Binary || result.Truncated {
				t.Fatalf("lost inert text: %+v", result)
			}
		case "binary", "invalid":
			if !result.Binary || result.Text != "" {
				t.Fatal("binary entered preview")
			}
		case "large":
			if result.Binary || !result.Truncated || len(result.Text) != domain.WorkspacePreviewLimit-1 {
				t.Fatal("UTF-8 boundary/truncation lost")
			}
		}
	}
	after, err := m.readExecutionClaim(input.SessionID)
	if err != nil || claim != after {
		t.Fatal("observation changed live execution ownership", err)
	}
}

func TestWorkspaceReadConfinesPathsAndOriginalManifest(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	out := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(out, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(manifest.PrimaryPath, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(out), filepath.Join(manifest.PrimaryPath, "directory-link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../secret", out, "escape", "directory-link/secret", "./escape", "C:/secret", "file:secret", "CON", "nested\\secret", "trailing.", "trailing "} {
		request := workspaceReadFixture(input, manifest, domain.WorkspaceFile, path)
		if _, err := m.ReadWorkspace(context.Background(), request); err == nil {
			t.Fatal("accepted unsafe path", path)
		}
	}
	request := workspaceReadFixture(input, manifest, domain.WorkspaceDirectory, ".")
	result, err := m.ReadWorkspace(context.Background(), request)
	if err != nil || len(result.Entries) != 2 {
		t.Fatal("link descriptors unavailable", err)
	}
	for _, entry := range result.Entries {
		if entry.Kind != domain.WorkspaceEntryLink {
			t.Fatal("link became navigable")
		}
	}
	request.Manifest.PrimaryPath = filepath.Dir(out)
	if _, err := m.ReadWorkspace(context.Background(), request); err == nil {
		t.Fatal("replaced accepted manifest")
	}
	request = workspaceReadFixture(input, manifest, domain.WorkspaceDirectory, ".")
	request.Query.RepositoryID = domain.NewID()
	if _, err := m.ReadWorkspace(context.Background(), request); err == nil {
		t.Fatal("foreign repository accepted")
	}
	request = workspaceReadFixture(input, manifest, domain.WorkspaceDirectory, ".")
	request.Deadline = time.Now().Add(-time.Second)
	if _, err := m.ReadWorkspace(context.Background(), request); err == nil {
		t.Fatal("expired read continued")
	}
}

func TestWorkspaceReadDirectoryPagesRejectMutationAndForeignScope(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	for i := 0; i < 105; i++ {
		if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, fmt.Sprintf("file-%03d", i)), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := workspaceReadFixture(input, manifest, domain.WorkspaceDirectory, ".")
	first, err := m.ReadWorkspace(context.Background(), request)
	if err != nil || len(first.Entries) != 100 || first.NextPageToken == "" {
		t.Fatal("missing first page", err)
	}
	request.ID = domain.NewID()
	request.Query.PageToken = first.NextPageToken
	second, err := m.ReadWorkspace(context.Background(), request)
	if err != nil || len(second.Entries) != 5 || second.Entries[0].Name != "file-100" || second.NextPageToken != "" {
		t.Fatal("missing second page", err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "file-000"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	request.ID = domain.NewID()
	if _, err := m.ReadWorkspace(context.Background(), request); err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("stale page accepted", err)
	}
}

func TestWorkspaceReadLocalPreservesSeparateProcessOwnership(t *testing.T) {
	m, input, manifest := localExecutionFixture(t)
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	claim, err := m.readExecutionClaim(input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.ReadWorkspace(context.Background(), workspaceReadFixture(input, manifest, domain.WorkspaceDirectory, "."))
	if err != nil || len(result.Entries) == 0 {
		t.Fatal("live Local read failed", err)
	}
	remaining, err := os.ReadDir(filepath.Join(m.Root, "workspace-read-processes-v2"))
	if err != nil || len(remaining) != 0 {
		t.Fatal("read children not retired", err)
	}
	after, err := m.readExecutionClaim(input.SessionID)
	if err != nil || claim != after {
		t.Fatal("read altered execution claim", err)
	}
}

func TestWorkspaceReadAnchorsNestedEntries(t *testing.T) {
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "nested", "plain.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fileRequest := ReadRequest{Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceFile, Path: "nested/plain.txt"}}
	preview, err := readRoot(context.Background(), root, fileRequest)
	if err != nil || preview.Text != "original" {
		t.Fatal("nested preview was not read from its anchored directory", err)
	}
	directoryRequest := ReadRequest{Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceDirectory, Path: "nested"}}
	listing, err := readRoot(context.Background(), root, directoryRequest)
	if err != nil || len(listing.Entries) != 1 || listing.Entries[0].Name != "plain.txt" {
		t.Fatal("nested directory listing was not anchored", err)
	}
}

func TestWorkspaceReadRejectsRacedInternalLinks(t *testing.T) {
	path := t.TempDir()
	for _, name := range []string{"original", "other"} {
		if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, name, "file.txt"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	fileBefore, err := root.Lstat("original/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "original", "other.txt"), []byte("different"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(path, "original", "file.txt"), filepath.Join(path, "original", "saved.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.txt", filepath.Join(path, "original", "file.txt")); err != nil {
		t.Skipf("creating a test symlink is unavailable: %v", err)
	}
	original, err := root.OpenRoot("original")
	if err != nil {
		t.Fatal(err)
	}
	defer original.Close()
	if file, err := openVerifiedEntry(original, "file.txt", fileBefore); err == nil {
		file.Close()
		t.Fatal("a replaced final component opened another workspace file")
	}

	directoryBefore, err := root.Lstat("original")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(path, "original"), filepath.Join(path, "saved-original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other", filepath.Join(path, "original")); err != nil {
		t.Fatal(err)
	}
	if directory, err := openVerifiedChildRoot(root, "original", directoryBefore); err == nil {
		directory.Close()
		t.Fatal("a replaced parent component opened another workspace directory")
	}
}
