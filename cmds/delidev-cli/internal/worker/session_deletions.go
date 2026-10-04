// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type sessionDeletionProof struct {
	Version        uint32    `json:"version"`
	Digest         string    `json:"digest"`
	ReportID       domain.ID `json:"report_id"`
	RemovalStarted bool      `json:"removal_started"`
	Complete       bool      `json:"complete"`
}

func sessionDeletionPath(root string, id domain.ID) string {
	return filepath.Join(root, "session-deletions", string(id)+".json")
}

func watchSessionDeletions(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		after := ""
		for ctx.Err() == nil {
			bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
			r, e := client.ListSessionDeletionWork(bounded, authenticated(credential, &pb.ListSessionDeletionWorkRequest{MachineId: string(credential.MachineID), InstanceId: string(instance), AfterSessionId: after}))
			cancel()
			if e != nil {
				if ctx.Err() == nil {
					config.Logger.DebugContext(ctx, "session_deletion_work_unavailable", "code", domain.SafeError(e).Code)
				}
				break
			}
			for _, raw := range r.Msg.WorkJson {
				var w domain.SessionDeletionWork
				if domain.Decode(raw, &w) != nil || w.Validate() != nil || w.ServerID != credential.ServerID || w.DeviceID != credential.DeviceID || w.MachineID != credential.MachineID {
					config.Logger.WarnContext(ctx, "session_deletion_invalid_work", "code", domain.RecoveryRequired)
					continue
				}
				bounded, cancel := context.WithTimeout(ctx, 2*time.Minute)
				proof, e := deleteSessionCopies(bounded, config, w)
				if e == nil {
					_, e = client.ReportSessionDeletion(bounded, authenticated(credential, &pb.ReportSessionDeletionRequest{RequestId: string(proof.ReportID), MachineId: string(w.MachineID), InstanceId: string(instance), SessionId: string(w.SessionID), DeletionId: string(w.DeletionID), WorkDigest: w.Digest()}))
				}
				cancel()
				if e != nil && ctx.Err() == nil {
					config.Logger.WarnContext(ctx, "session_deletion_worker_pending", "deletion_id", w.DeletionID, "code", domain.SafeError(e).Code)
				} else if e == nil {
					config.Logger.InfoContext(ctx, "session_deletion_worker_removed", "deletion_id", w.DeletionID)
				}
			}
			if r.Msg.NextSessionId == "" {
				break
			}
			after = r.Msg.NextSessionId
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Tombstones close native admission before cleanup. A once-persisted plan may
// resume removals after a crash; it never replays preparation or native input.
func deleteSessionCopies(ctx context.Context, config Config, w domain.SessionDeletionWork) (sessionDeletionProof, error) {
	proof := sessionDeletionProof{Version: 1, Digest: w.Digest(), ReportID: domain.NewID()}
	if w.Validate() != nil {
		return proof, domain.SessionDeletionPending()
	}
	root, e := filepath.EvalSymlinks(config.Root)
	if e != nil {
		return proof, domain.SessionDeletionPending()
	}
	if e := security.PrivateDir(filepath.Join(root, "session-deletions")); e != nil {
		return proof, domain.SessionDeletionPending()
	}
	lock, e := security.TryLock(filepath.Join(root, "session-deletions", string(w.SessionID)+".lock"))
	if e != nil {
		return proof, domain.SessionDeletionPending()
	}
	defer lock.Close()
	path := sessionDeletionPath(root, w.SessionID)
	raw, e := security.ReadPrivate(path, 4096)
	if e == nil {
		if domain.Decode(raw, &proof) != nil || proof.Version != 1 || proof.Digest != w.Digest() || proof.ReportID.Validate() != nil {
			return proof, domain.SessionDeletionPending()
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return proof, domain.SessionDeletionPending()
	} else {
		if e := writeJSON(path, proof); e != nil {
			return proof, e
		}
	}
	if proof.Complete {
		// Completion is reusable only while its original copies remain absent.
		// A replacement at a formerly owned path is never blindly removed.
		paths, e := sessionDeletionCopyPaths(ctx, root, w)
		if e != nil {
			return proof, e
		}
		paths = append(paths, filepath.Join(root, "workspaces", string(w.SessionID)))
		for _, path := range paths {
			if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
				return proof, domain.SessionDeletionPending()
			}
		}
		return proof, nil
	}
	// The publisher lock precedes the session lock throughout the Worker. A live
	// owner must finish cancellation and release its handles before removal.
	locks := []*security.Lock{}
	jobLocks := []*security.Lock{}
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Close()
		}
		for i := len(jobLocks) - 1; i >= 0; i-- {
			jobLocks[i].Close()
		}
	}()
	allowAbsentWorkspace := w.Fork == nil
	if w.Fork != nil && !proof.RemovalStarted {
		if _, err := executionCheckpointPath(root, w.Fork.RuntimeID); err != nil {
			return proof, domain.SessionDeletionPending()
		}
		raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(w.Fork.RuntimeID), "fork-completion.json"), maxExecutionCheckpointBytes)
		var checkpoint ForkCheckpoint
		if err != nil || executionInputDigest(raw) != w.Fork.CheckpointDigest || domain.Decode(raw, &checkpoint) != nil || checkpoint.Version != 1 || checkpoint.JobID != w.Fork.JobID || checkpoint.JobInputDigest != w.Fork.JobInputDigest || checkpoint.RuntimeID != w.Fork.RuntimeID || checkpoint.SessionID != w.SessionID || checkpoint.MachineID != w.MachineID || string(mustForkJSON(checkpoint)) != string(raw) {
			return proof, domain.SessionDeletionPending()
		}
	}
	for _, copy := range w.Copies {
		// Native execution and final journal/report publication retain this
		// outer lock after workspace and outbox locks are released. Join it
		// first so a completed operation cannot recreate its journal mid-delete.
		jobLock, e := security.TryLock(filepath.Join(root, "jobs", string(copy.JobID)+".lock"))
		if e != nil {
			return proof, domain.SessionDeletionPending()
		}
		jobLocks = append(jobLocks, jobLock)
		folder := filepath.Join(root, "jobs", string(copy.JobID))
		if _, e := os.Lstat(folder); e == nil {
			if e := security.CheckPrivateDir(folder); e != nil {
				return proof, domain.SessionDeletionPending()
			}
			l, e := security.TryLock(filepath.Join(folder, "publication.lock"))
			if e != nil {
				return proof, domain.SessionDeletionPending()
			}
			locks = append(locks, l)
		} else if !errors.Is(e, os.ErrNotExist) {
			return proof, domain.SessionDeletionPending()
		}
		if !proof.RemovalStarted {
			raw, e := security.ReadPrivate(filepath.Join(root, "jobs", string(copy.JobID)+".json"), 2<<20)
			if e != nil {
				return proof, domain.SessionDeletionPending()
			}
			var j journal
			if domain.Decode(raw, &j) != nil || j.Version != 1 || j.JobID != copy.JobID || j.InstanceID != copy.InstanceID || j.Revision != copy.Revision || j.Digest != copy.Digest || j.ReportID.Validate() != nil {
				return proof, domain.SessionDeletionPending()
			}
			if copy.Type != domain.PrepareWorkspaceJob || j.Problem == nil || len(j.Output) != 0 {
				allowAbsentWorkspace = false
			}
			ownerRoot := filepath.Join(root, "processes", string(copy.JobID))
			if _, e := os.Lstat(ownerRoot); e == nil {
				if e := process.ReconcileOwnerContext(ctx, filepath.Join(root, "processes"), copy.JobID); e != nil {
					return proof, e
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return proof, domain.SessionDeletionPending()
			} else if copy.Type != domain.WorkspaceStorageJob && (j.State == journalStarted || j.Problem != nil && j.Problem.Code == domain.RecoveryRequired) {
				return proof, domain.SessionDeletionPending()
			}
		}
	}
	manager := workspace.Manager{Root: root, Logger: config.Logger}
	e = manager.DeleteOwnedWorkspace(ctx, w, allowAbsentWorkspace, func() error {
		// Admission is tombstoned and every native/publisher owner was joined.
		// Release handles before unlinking their lock files on Windows.
		for _, lock := range locks {
			if e := lock.Close(); e != nil {
				return domain.SessionDeletionPending()
			}
		}
		locks = nil
		if !proof.RemovalStarted {
			proof.RemovalStarted = true
			if e := writeJSON(path, proof); e != nil {
				return e
			}
		}
		paths, e := sessionDeletionCopyPaths(ctx, root, w)
		if e != nil {
			return e
		}
		for _, path := range paths {
			if filepath.Dir(path) == filepath.Join(root, "snapshot-staging") {
				// Workspace cleanup already checked the original native staging
				// identity. A later replacement must remain protected here.
				if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
					return domain.SessionDeletionPending()
				}
				continue
			}
			if e := removeSessionTree(ctx, root, path); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return proof, e
	}
	proof.Complete = true
	return proof, writeJSON(path, proof)
}

// Walk first without following symlinks, then remove deepest paths first. Each
// operation observes cancellation and the explicit entry bound; original Local
// checkouts and shared browser/account profiles never enter this path list.
func removeSessionTree(ctx context.Context, root, path string) error {
	return security.RemoveOwnedTree(ctx, root, path)
}

// Removal and completed-proof replay share one inventory, including dynamic
// title runtimes. A restored or replaced copy blocks acknowledgement; replay
// never gains permission to delete it merely from the earlier completed proof.
func sessionDeletionCopyPaths(ctx context.Context, root string, w domain.SessionDeletionWork) ([]string, error) {
	paths := []string{filepath.Join(root, "execution-claims", string(w.SessionID)+".json"), filepath.Join(root, "execution-history", string(w.SessionID)), filepath.Join(root, "pr-startup", string(w.SessionID)), filepath.Join(root, "processes", string(w.SessionID)), filepath.Join(root, "processes", string(w.SessionID)+".recovery.lock")}
	paths = append(paths, workspace.SessionStorageCopyPaths(root, w)...)
	remnants, err := workspace.SessionStorageRemnantPaths(ctx, root, w)
	if err != nil {
		return nil, err
	}
	paths = append(paths, remnants...)
	if w.Fork != nil {
		// The source job journal may be shared with a live parent or already gone.
		// Only the child-bound immutable checkpoint grants ownership of this runtime.
		paths = append(paths, filepath.Join(root, "runtimes", string(w.Fork.RuntimeID)))
	}
	titlePrefixes := map[string]bool{}
	for _, copy := range w.Copies {
		if e := ctx.Err(); e != nil {
			return nil, domain.SafeError(e)
		}
		paths = append(paths, filepath.Join(root, "jobs", string(copy.JobID)), filepath.Join(root, "jobs", string(copy.JobID)+".json"), filepath.Join(root, "workspace-recovery", string(copy.JobID)+".json"), filepath.Join(root, "processes", string(copy.JobID)), filepath.Join(root, "processes", string(copy.JobID)+".recovery.lock"))
		if copy.ExecutionID != "" {
			paths = append(paths, filepath.Join(root, "runtimes", string(copy.ExecutionID)), filepath.Join(root, "pr-git", string(copy.ExecutionID)))
		}
		if copy.Type == domain.CompactSessionJob {
			// An action owns its replacement runtime and retained checkpoint;
			// the original conversation execution keeps its separate identity.
			paths = append(paths, filepath.Join(root, "runtimes", string(copy.ActionID)), filepath.Join(root, "compaction-checkpoints", string(copy.ActionID)+".json"))
		}
		if copy.Type == domain.GenerateSessionTitleJob {
			titlePrefixes[string(copy.JobID)+"-"] = true
		}
	}
	if len(titlePrefixes) == 0 {
		return paths, nil
	}
	f, e := os.Open(filepath.Join(root, "title-runtimes"))
	if errors.Is(e, os.ErrNotExist) {
		return paths, nil
	}
	if e != nil {
		return nil, domain.SessionDeletionPending()
	}
	defer f.Close()
	entries, e := f.ReadDir(4097)
	if e != nil && !errors.Is(e, io.EOF) || len(entries) > 4096 {
		return nil, domain.SessionDeletionPending()
	}
	// Every validated UUID has the same canonical width. Match the exact job
	// UUID plus separator, without scanning the directory once per title job.
	prefixLength := len(w.SessionID) + 1
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return nil, domain.SafeError(e)
		}
		name := entry.Name()
		if len(name) >= prefixLength && titlePrefixes[name[:prefixLength]] {
			paths = append(paths, filepath.Join(root, "title-runtimes", name))
		}
	}
	return paths, nil
}
