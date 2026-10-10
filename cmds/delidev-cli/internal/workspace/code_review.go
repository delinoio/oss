// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// WithNativeCodeReview retains the original read-only observation lock and
// child ownership until the caller has joined native/auth cleanup. It never
// claims a continuation, rewrites a manifest or advances an execution owner.
func (m *Manager) WithNativeCodeReview(ctx context.Context, request ReadRequest, target domain.NativeCodeReviewTarget, review func(context.Context, string, domain.NativeCodeReviewSelection) error) error {
	if review == nil || target.Validate() != nil || request.ID.Validate() != nil || request.Preparation.validate() != nil || request.PRCandidate != nil || request.Skills != nil {
		return domain.NativeCodeReviewUnavailable()
	}
	admission, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()
	return m.observeWorkspaceOwned(admission, ctx, request, target.RepositoryID, true, func(ctx context.Context, git Git, retained Manifest, root *os.Root, path string) error {
		before, err := captureNativeReview(ctx, git, retained, request, target, path)
		if err != nil {
			return err
		}
		if err := review(ctx, path, before); err != nil {
			return err
		}
		after, err := captureNativeReview(ctx, git, retained, request, target, path)
		if err != nil {
			return err
		}
		expected, _ := json.Marshal(before)
		actual, _ := json.Marshal(after)
		if string(expected) != string(actual) {
			return domain.Fail(domain.Conflict, "The original review target changed.", "Keep the original findings as stale evidence; explicitly refresh the selected target before another review.")
		}
		return nil
	})
}

func captureNativeReview(ctx context.Context, git Git, manifest Manifest, request ReadRequest, target domain.NativeCodeReviewTarget, path string) (domain.NativeCodeReviewSelection, error) {
	var result domain.NativeCodeReviewSelection
	ctx, cancel := context.WithTimeout(ctx, 16*time.Second)
	defer cancel()
	request.Query = domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: target.RepositoryID, Path: ".", Comparison: domain.DiffWorkingTree}
	diff, err := git.readDiff(ctx, request, manifest)
	if err != nil {
		return result, err
	}
	if diff.Diff == nil || diff.Diff.Revision != target.DiffRevision {
		return result, domain.Fail(domain.Conflict, "The selected review revision is stale.", "Refresh the original repository comparison before explicitly starting a new review.")
	}
	marker, err := captureForkRootMarker(path)
	if err != nil {
		return result, err
	}
	digest, err := scanForkTreePinned(ctx, path, "", true, maxForkEntries, &marker)
	if err != nil {
		return result, err
	}
	result = domain.NativeCodeReviewSelection{Target: target, HeadCommit: diff.Diff.HeadCommit, ContentDigest: digest}
	if target.Kind == domain.ReviewBaseBranch || target.Kind == domain.ReviewCommit {
		raw, err := git.run(ctx, path, "rev-parse", "--verify", "--end-of-options", target.Reference+"^{commit}")
		if err != nil {
			return result, err
		}
		commit := trimGit(raw)
		if target.Kind == domain.ReviewBaseBranch {
			result.BaseCommit = commit
		} else if commit != target.Reference {
			return result, domain.NativeCodeReviewUnavailable()
		}
	}
	if result.Validate() != nil {
		return result, domain.NativeCodeReviewUnavailable()
	}
	return result, nil
}
