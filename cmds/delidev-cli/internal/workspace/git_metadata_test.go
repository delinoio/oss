package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestBatchedGitMetadataKeepsLinkedAdministrationAndObjectFormat(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source with spaces")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			gitTest(t, source, "init", "--object-format="+format, "-b", "main")
			if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("original\n"), 0600); err != nil {
				t.Fatal(err)
			}
			gitTest(t, source, "add", "tracked.txt")
			gitTest(t, source, "commit", "-m", "initial")
			linked := filepath.Join(t.TempDir(), "linked with spaces")
			gitTest(t, source, "worktree", "add", "--detach", linked, "HEAD")
			g := Git{ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), readOnly: true}
			defer func() {
				if err := process.ReconcileOwner(g.ProcessRoot, g.OwnerID); err != nil {
					t.Error(err)
				}
			}()
			fields, err := g.revParseFields(context.Background(), linked, 2, "--git-common-dir", "--absolute-git-dir")
			if err != nil {
				t.Fatal(err)
			}
			if fields[0] == fields[1] || fields[0] != gitTest(t, source, "rev-parse", "--path-format=absolute", "--git-common-dir") || fields[1] != gitTest(t, linked, "rev-parse", "--absolute-git-dir") {
				t.Fatal("linked administration was replaced by the common directory", fields)
			}
			if err := os.WriteFile(filepath.Join(linked, "tracked.txt"), []byte("changed\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := gitTest(t, linked, "ls-files", "--stage")
			patch, err := g.filterFreeDiff(context.Background(), linked, []string{"diff", "--no-ext-diff", "--no-textconv", gitTest(t, linked, "rev-parse", "HEAD"), "--", "tracked.txt"})
			if err != nil || !strings.Contains(string(patch), "+changed") || gitTest(t, linked, "ls-files", "--stage") != before {
				t.Fatal("batched metadata lost the object format or changed the original index", err)
			}
		})
	}
}

func TestGitMetadataRejectsAmbiguousRecordBoundaries(t *testing.T) {
	for _, raw := range []string{"first\nsecond", "first\n\n", "first\nsecond\nextra\n", "first\x00\nsecond\n", "first\rhidden\nsecond\n"} {
		if _, err := parseGitFields([]byte(raw), 2); err == nil {
			t.Fatalf("accepted ambiguous metadata %q", raw)
		}
	}
	for _, ending := range []string{"\n", "\r\n"} {
		got, err := parseGitFields([]byte(" path with spaces "+ending+"second"+ending), 2)
		if err != nil || !reflect.DeepEqual(got, []string{" path with spaces ", "second"}) {
			t.Fatal("metadata changed path bytes", got, err)
		}
	}
}
