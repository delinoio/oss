package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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
		paths := []string{filepath.Join(root, "workspaces", string(w.SessionID)), filepath.Join(root, "execution-claims", string(w.SessionID)+".json"), filepath.Join(root, "execution-history", string(w.SessionID)), filepath.Join(root, "pr-startup", string(w.SessionID))}
		for _, copy := range w.Copies {
			paths = append(paths, filepath.Join(root, "jobs", string(copy.JobID)), filepath.Join(root, "jobs", string(copy.JobID)+".json"), filepath.Join(root, "workspace-recovery", string(copy.JobID)+".json"))
			if copy.ExecutionID != "" {
				paths = append(paths, filepath.Join(root, "runtimes", string(copy.ExecutionID)))
			}
		}
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
	defer func() {
		for i := len(locks) - 1; i >= 0; i-- {
			locks[i].Close()
		}
	}()
	allowAbsentWorkspace := true
	for _, copy := range w.Copies {
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
			} else if j.State == journalStarted || j.Problem != nil && j.Problem.Code == domain.RecoveryRequired {
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
		for _, copy := range w.Copies {
			paths := []string{filepath.Join(root, "jobs", string(copy.JobID)), filepath.Join(root, "jobs", string(copy.JobID)+".json"), filepath.Join(root, "workspace-recovery", string(copy.JobID)+".json"), filepath.Join(root, "processes", string(copy.JobID)), filepath.Join(root, "processes", string(copy.JobID)+".recovery.lock")}
			if copy.ExecutionID != "" {
				paths = append(paths, filepath.Join(root, "runtimes", string(copy.ExecutionID)))
			}
			if copy.Type == domain.GenerateSessionTitleJob {
				f, e := os.Open(filepath.Join(root, "title-runtimes"))
				if e == nil {
					entries, e := f.ReadDir(4097)
					f.Close()
					if e != nil && !errors.Is(e, io.EOF) || len(entries) > 4096 {
						return domain.SessionDeletionPending()
					}
					for _, entry := range entries {
						if strings.HasPrefix(entry.Name(), string(copy.JobID)+"-") {
							paths = append(paths, filepath.Join(root, "title-runtimes", entry.Name()))
						}
					}
				} else if !errors.Is(e, os.ErrNotExist) {
					return domain.SessionDeletionPending()
				}
			}
			for _, path := range paths {
				if e := removeSessionTree(ctx, root, path); e != nil {
					return e
				}
			}
		}
		for _, path := range []string{filepath.Join(root, "execution-claims", string(w.SessionID)+".json"), filepath.Join(root, "execution-history", string(w.SessionID)), filepath.Join(root, "pr-startup", string(w.SessionID)), filepath.Join(root, "processes", string(w.SessionID)), filepath.Join(root, "processes", string(w.SessionID)+".recovery.lock")} {
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
