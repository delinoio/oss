// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

const maxSessionCopyRemovalPlanBytes = 64 << 20

type sessionCopyRemovalPlan struct {
	Version uint32                     `json:"version"`
	Digest  string                     `json:"digest"`
	Copies  []security.RemovalSnapshot `json:"copies"`
}

func sessionCopyPlanPath(root string, session domain.ID) string {
	return filepath.Join(root, "session-deletions", string(session)+"-copies.json")
}

func readSessionCopyPlan(ctx context.Context, root string, w domain.SessionDeletionWork) (*sessionCopyRemovalPlan, error) {
	raw, err := security.ReadPrivate(sessionCopyPlanPath(root, w.SessionID), maxSessionCopyRemovalPlanBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var plan sessionCopyRemovalPlan
	if err != nil || domain.DecodeWithLimit(raw, &plan, maxSessionCopyRemovalPlanBytes) != nil || plan.Version != 1 || plan.Digest != w.Digest() || plan.Copies == nil || len(plan.Copies) > 100000 {
		return nil, domain.SessionDeletionPending()
	}
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, path := range paths {
		allowed[path] = true
	}
	seen := map[string]bool{}
	for _, copy := range plan.Copies {
		path := filepath.Join(root, copy.Path)
		title := false
		if filepath.Dir(path) == filepath.Join(root, "title-runtimes") {
			for _, owner := range w.Copies {
				if owner.Type == domain.GenerateSessionTitleJob && strings.HasPrefix(filepath.Base(path), string(owner.JobID)+"-") {
					title = true
				}
			}
		}
		if !allowed[path] && !title || sessionCopyAbsenceOnly(root, path) || unpublishedSessionCopy(w, root, path) {
			return nil, domain.SessionDeletionPending()
		}
		if copy.Path == "" || copy.Path == "." || filepath.IsAbs(copy.Path) || filepath.Clean(copy.Path) != copy.Path || seen[copy.Path] {
			return nil, domain.SessionDeletionPending()
		}
		seen[copy.Path] = true
	}
	return &plan, nil
}

func captureSessionCopyRoots(ctx context.Context, root string, w domain.SessionDeletionWork) (map[string]security.RemovalRoot, error) {
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return nil, err
	}
	roots := map[string]security.RemovalRoot{}
	for _, path := range paths {
		if sessionCopyAbsenceOnly(root, path) || unpublishedSessionCopy(w, root, path) {
			continue
		}
		if _, found := roots[path]; found {
			continue
		}
		original, err := security.ObserveRemovalRoot(ctx, root, path)
		if err != nil {
			return nil, err
		}
		roots[path] = original
	}
	// Fork publication is validated from this exact nested file, not merely
	// from its runtime directory name or content digest after replacement.
	forks := append([]domain.SessionDeletionFork(nil), w.RetryForks...)
	if w.Fork != nil {
		forks = append(forks, *w.Fork)
	}
	for _, fork := range forks {
		path := filepath.Join(root, "runtimes", string(fork.RuntimeID), "fork-completion.json")
		original, err := security.ObserveRemovalRoot(ctx, root, path)
		if err != nil {
			return nil, err
		}
		roots[path] = original
	}
	return roots, nil
}

func captureSessionCopyPlan(ctx context.Context, root string, w domain.SessionDeletionWork, roots map[string]security.RemovalRoot) (*sessionCopyRemovalPlan, error) {
	plan := &sessionCopyRemovalPlan{Version: 1, Digest: w.Digest(), Copies: []security.RemovalSnapshot{}}
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, path := range paths {
		if sessionCopyAbsenceOnly(root, path) || unpublishedSessionCopy(w, root, path) || seen[path] {
			continue
		}
		seen[path] = true
		original, present := roots[path]
		if !present {
			return nil, domain.SessionDeletionPending()
		}
		snapshot, err := security.CaptureRemovalSnapshot(ctx, root, path, original)
		if err != nil {
			return nil, err
		}
		plan.Copies = append(plan.Copies, snapshot)
	}
	// Retain independently validated nested file identities through the final
	// snapshot too. They are already part of their original runtime tree plan.
	for path, original := range roots {
		if seen[path] {
			continue
		}
		if _, err := security.CaptureRemovalSnapshot(ctx, root, path, original); err != nil {
			return nil, err
		}
	}
	raw, err := json.Marshal(plan)
	if err != nil || len(raw) > maxSessionCopyRemovalPlanBytes {
		return nil, domain.SessionDeletionPending()
	}
	if err := security.WriteAtomic(sessionCopyPlanPath(root, w.SessionID), raw); err != nil {
		return nil, err
	}
	return plan, nil
}

func unpublishedSessionCopy(w domain.SessionDeletionWork, root, path string) bool {
	for _, copy := range w.Copies {
		if copy.UnpublishedChildProcessID != "" && (path == filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)) || path == filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)+".recovery.lock")) {
			return true
		}
		if copy.UnpublishedSidechatID != "" && (path == filepath.Join(root, "workspaces", string(copy.UnpublishedSidechatID)) || path == workspace.SidechatForkClaimPath(root, copy.JobID)) {
			return true
		}
	}
	return false
}

func removeSessionCopyPlan(ctx context.Context, root string, w domain.SessionDeletionWork, plan *sessionCopyRemovalPlan, started bool) error {
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return err
	}
	snapshots := map[string]security.RemovalSnapshot{}
	if plan != nil {
		for _, snapshot := range plan.Copies {
			snapshots[filepath.Join(root, snapshot.Path)] = snapshot
		}
	}
	for _, path := range paths {
		if sessionCopyAbsenceOnly(root, path) || unpublishedSessionCopy(w, root, path) {
			if err := requireSessionCopyAbsent(path); err != nil {
				return err
			}
			continue
		}
		if _, present := snapshots[path]; !present {
			if err := requireSessionCopyAbsent(path); err != nil {
				return err
			}
		}
	}
	// Include retained dynamic roots even after their original names disappear
	// from a new inventory. They cannot be refreshed from any replacement copy.
	for _, snapshot := range planCopies(plan) {
		if err := security.RemoveSnapshotTree(ctx, root, snapshot, started); err != nil {
			return err
		}
	}
	return nil
}

func planCopies(plan *sessionCopyRemovalPlan) []security.RemovalSnapshot {
	if plan == nil {
		return nil
	}
	return plan.Copies
}

func requireSessionCopyAbsent(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	return nil
}
