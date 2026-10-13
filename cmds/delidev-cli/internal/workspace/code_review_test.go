// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestNativeReviewPinsUntrackedBytesWithoutChangingExecutionOwner(t *testing.T) {
	for _, drift := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "changed-untracked-content"}[drift], func(t *testing.T) {
			m := manager(t)
			source, err := filepath.EvalSymlinks(repository(t))
			if err != nil {
				t.Fatal(err)
			}
			input, _ := requestFor(source)
			manifest, err := m.Prepare(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			owner, err := m.readExecutionClaim(input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(manifest.PrimaryPath, "untracked.txt")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			request := diffRequest(input, manifest, domain.DiffWorkingTree, ".")
			observed, err := m.ReadWorkspace(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: input.PrimaryRepository, DiffRevision: observed.Diff.Revision}
			err = m.WithNativeCodeReview(context.Background(), request, target, func(ctx context.Context, path string, selection domain.NativeCodeReviewSelection) error {
				if path != manifest.PrimaryPath || selection.Target != target || selection.Validate() != nil {
					t.Fatal("original target not retained")
				}
				if drift {
					return os.WriteFile(filepath.Join(path, "untracked.txt"), []byte("different bytes same path"), 0600)
				}
				return nil
			})
			if drift && err == nil || !drift && err != nil {
				t.Fatal("review drift classification", err)
			}
			after, err := m.readExecutionClaim(input.SessionID)
			if err != nil || after != owner {
				t.Fatal("review advanced original execution predecessor", err)
			}
		})
	}
}

func TestNativeReviewCommitAndBaseTargetsKeepExactCommit(t *testing.T) {
	for _, kind := range []domain.NativeCodeReviewTargetKind{domain.ReviewCommit, domain.ReviewBaseBranch} {
		t.Run(string(kind), func(t *testing.T) {
			m := manager(t)
			source, err := filepath.EvalSymlinks(repository(t))
			if err != nil {
				t.Fatal(err)
			}
			input, _ := requestFor(source)
			manifest, err := m.Prepare(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			request := diffRequest(input, manifest, domain.DiffWorkingTree, ".")
			observed, err := m.ReadWorkspace(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			head := gitTest(t, manifest.PrimaryPath, "rev-parse", "HEAD")
			target := domain.NativeCodeReviewTarget{Kind: kind, RepositoryID: input.PrimaryRepository, DiffRevision: observed.Diff.Revision, Reference: head}
			if kind == domain.ReviewBaseBranch {
				target.Reference = "HEAD"
			}
			if err = m.WithNativeCodeReview(context.Background(), request, target, func(ctx context.Context, path string, selection domain.NativeCodeReviewSelection) error {
				if selection.HeadCommit != head || kind == domain.ReviewBaseBranch && selection.BaseCommit != head || kind == domain.ReviewCommit && selection.Target.Reference != head {
					t.Fatal("original exact commit changed")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Native review accepts every target kind in both Git object formats and
// forwards the repository's full-width HEAD object name to the callback.
func TestNativeReviewSupportsSHA1AndSHA256ObjectFormats(t *testing.T) {
	for _, format := range []struct {
		name  string
		width int
	}{{"sha1", 40}, {"sha256", 64}} {
		for _, kind := range []domain.NativeCodeReviewTargetKind{domain.ReviewUncommitted, domain.ReviewBaseBranch, domain.ReviewCommit, domain.ReviewCustom} {
			t.Run(format.name+"/"+string(kind), func(t *testing.T) {
				m := manager(t)
				source, err := filepath.EvalSymlinks(repositoryWithObjectFormat(t, format.name))
				if err != nil {
					t.Fatal(err)
				}
				input, _ := requestFor(source)
				manifest, err := m.Prepare(context.Background(), input)
				if err != nil {
					t.Fatal(err)
				}
				lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
				if err != nil {
					t.Fatal(err)
				}
				defer lease.Close()
				request := diffRequest(input, manifest, domain.DiffWorkingTree, ".")
				observed, err := m.ReadWorkspace(context.Background(), request)
				if err != nil {
					t.Fatal(err)
				}
				head := gitTest(t, manifest.PrimaryPath, "rev-parse", "HEAD")
				if len(head) != format.width {
					t.Fatalf("fixture object format mismatch: %d", len(head))
				}
				target := domain.NativeCodeReviewTarget{Kind: kind, RepositoryID: input.PrimaryRepository, DiffRevision: observed.Diff.Revision}
				switch kind {
				case domain.ReviewBaseBranch:
					target.Reference = "HEAD"
				case domain.ReviewCommit:
					target.Reference = head
				case domain.ReviewCustom:
					target.Instructions = "Review the selected changes."
				}
				reached := false
				if err = m.WithNativeCodeReview(context.Background(), request, target, func(ctx context.Context, path string, selection domain.NativeCodeReviewSelection) error {
					reached = true
					if selection.HeadCommit != head || len(selection.HeadCommit) != format.width || selection.Validate() != nil {
						t.Fatal("selection did not keep the exact repository object name")
					}
					if kind == domain.ReviewBaseBranch && selection.BaseCommit != head {
						t.Fatal("base commit changed")
					}
					return nil
				}); err != nil || !reached {
					t.Fatal("native review capture failed", err)
				}
			})
		}
	}
}
