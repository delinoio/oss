package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestReviewAnchorsFromActualGitPathsAddDeleteBinaryAndEOF(t *testing.T) {
	m, input, manifest := localExecutionFixture(t)
	root := manifest.PrimaryPath
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	names := []string{"space name.txt", "a b/c.txt", "한글.txt", "😀.txt", "delete.txt", "binary.txt", "mode.txt"}
	for _, name := range names {
		write(name, "old\n")
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-m", "review paths")
	for _, name := range names[:4] {
		write(name, "new")
	}
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	write("binary.txt", "\x00binary")
	write("added.txt", "added\n")
	write("empty.txt", "")
	gitTest(t, root, "add", "added.txt", "empty.txt")
	gitTest(t, root, "update-index", "--chmod=+x", "mode.txt")
	r, err := m.ReadWorkspace(context.Background(), diffRequest(input, manifest, domain.DiffStaged, "."))
	if err != nil {
		t.Fatal(err)
	}
	staged, err := r.Diff.ReviewFiles()
	if err != nil || len(staged) != 3 {
		t.Fatal("staged files", staged, err)
	}
	for _, name := range []string{"added.txt", "empty.txt", "mode.txt"} {
		if _, err := r.Diff.ReviewAnchor(domain.ReviewSelection{Path: name, Kind: domain.ReviewFileAnchor}); err != nil {
			t.Fatal(name, err)
		}
	}
	r, err = m.ReadWorkspace(context.Background(), diffRequest(input, manifest, domain.DiffWorkingTree, "."))
	if err != nil {
		t.Fatal(err)
	}
	files, err := r.Diff.ReviewFiles()
	if err != nil || len(files) < 8 {
		t.Fatal("working files", files, err)
	}
	for _, name := range names[:4] {
		a, err := r.Diff.ReviewAnchor(domain.ReviewSelection{Path: name, Kind: domain.ReviewLineAnchor, Side: domain.ReviewNewSide, Start: 1, End: 1})
		if err != nil || a.Context != "new" {
			t.Fatal(name, a, err)
		}
	}
	a, err := r.Diff.ReviewAnchor(domain.ReviewSelection{Path: "delete.txt", Kind: domain.ReviewLineAnchor, Side: domain.ReviewOldSide, Start: 1, End: 1})
	if err != nil || a.Context != "old\n" {
		t.Fatal(a, err)
	}
	if _, err := r.Diff.ReviewAnchor(domain.ReviewSelection{Path: "binary.txt", Kind: domain.ReviewLineAnchor, Side: domain.ReviewNewSide, Start: 1, End: 1}); err == nil {
		t.Fatal("invented binary line")
	}
}
