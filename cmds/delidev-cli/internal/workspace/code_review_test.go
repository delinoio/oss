// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
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

// symlinkRepository commits links to a file, a missing target and a directory
// outside the repository so review capture meets each link shape.
func symlinkRepository(t *testing.T) (root, outside string) {
	t.Helper()
	root = repository(t)
	outside = t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link-file": "tracked.txt", "link-dangling": "missing-target", "link-outside": outside, "link-outside-file": filepath.Join(outside, "secret.txt")} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skip("symlinks are unavailable:", err)
		}
		gitTest(t, root, "add", name)
	}
	gitTest(t, root, "commit", "-m", "links")
	return root, outside
}

func TestNativeReviewCapturesTrackedSymlinksWithoutFollowingThem(t *testing.T) {
	m := manager(t)
	source, outside := symlinkRepository(t)
	source, err := filepath.EvalSymlinks(source)
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
	target := domain.NativeCodeReviewTarget{Kind: domain.ReviewUncommitted, RepositoryID: input.PrimaryRepository, DiffRevision: observed.Diff.Revision}
	reached := false
	if err := m.WithNativeCodeReview(context.Background(), request, target, func(ctx context.Context, path string, selection domain.NativeCodeReviewSelection) error {
		reached = true
		return nil
	}); err != nil || !reached {
		t.Fatal("tracked symlinks prevented the native review callback", err)
	}
	// Changing a link target during the review is a stale Conflict.
	err = m.WithNativeCodeReview(context.Background(), request, target, func(ctx context.Context, path string, selection domain.NativeCodeReviewSelection) error {
		link := filepath.Join(path, "link-dangling")
		if err := os.Remove(link); err != nil {
			return err
		}
		return os.Symlink(filepath.Join(outside, "elsewhere"), link)
	})
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != domain.Conflict {
		t.Fatal("changed link target was not a stale conflict", err)
	}
}

func TestForkScanSymlinkPolicyBindsLinkTextAndRejectsByDefault(t *testing.T) {
	source, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(source, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks are unavailable:", err)
	}
	scan := func(policy forkSymlinkPolicy, target string) (string, error) {
		marker, err := captureForkRootMarker(source)
		if err != nil {
			t.Fatal(err)
		}
		return scanForkTreePolicy(context.Background(), policy, source, target, true, maxForkEntries, &marker)
	}
	if _, err := scan(forkSymlinkReject, ""); err == nil {
		t.Fatal("default policy accepted a symlink")
	}
	if _, err := scanForkTreeBounded(context.Background(), source, "", true, maxForkEntries); err == nil {
		t.Fatal("fork scanner accepted a symlink")
	}
	if _, err := scan(forkSymlinkRecord, t.TempDir()); err == nil {
		t.Fatal("recording scan was allowed to publish a copy")
	}
	first, err := scan(forkSymlinkRecord, "")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := scan(forkSymlinkRecord, ""); err != nil || again != first {
		t.Fatal("recording digest is not stable", err)
	}
	// Bytes behind the link are never read, so editing them keeps the digest.
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("changed outside bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if again, err := scan(forkSymlinkRecord, ""); err != nil || again != first {
		t.Fatal("link digest depends on bytes behind the link", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), link); err != nil {
		t.Fatal(err)
	}
	if changed, err := scan(forkSymlinkRecord, ""); err != nil || changed == first {
		t.Fatal("link text change did not change the digest", err)
	}
	// A regular file at the same path binds a different entry kind.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte(filepath.Join(outside, "secret.txt")), 0600); err != nil {
		t.Fatal(err)
	}
	if regular, err := scan(forkSymlinkRecord, ""); err != nil || regular == first {
		t.Fatal("entry kind is not bound", err)
	}
}
