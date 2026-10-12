// SPDX-License-Identifier: Apache-2.0
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/skills"
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

// sessionDeletionPath is the session-level admission tombstone. Its existence
// closes native admission for the session; it is content-independent for every
// gate reader. Retained proofs live at per-obligation paths.
func sessionDeletionPath(root string, id domain.ID) string {
	return filepath.Join(root, "session-deletions", string(id)+".json")
}

// sessionDeletionTombstoneVersion marks a tombstone that owns no proof. Version 1
// is the legacy session-keyed proof, which only its exact original obligation
// may adopt.
const sessionDeletionTombstoneVersion = 2

// Each original device obligation owns its proof and lock, so two obligations
// for one session and machine never share or replay retained state.
func sessionDeletionObligationPath(root string, w domain.SessionDeletionWork, suffix string) string {
	return filepath.Join(root, "session-deletions", workspace.SessionDeletionObligationName(w)+suffix)
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
				if domain.DecodeWithLimit(raw, &w, domain.MaxSessionDeletionBytes) != nil || w.Validate() != nil || w.ServerID != credential.ServerID || w.DeviceID != credential.DeviceID || w.MachineID != credential.MachineID {
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
	for _, copy := range w.Copies {
		if copy.Type == domain.ForkSessionJob && copy.ExecutionID != "" && copy.UnpublishedSidechatID == "" && copy.UnpublishedChildProcessID == "" && !copy.SidechatRetry {
			return proof, domain.SessionDeletionPending()
		}
	}
	root, e := filepath.EvalSymlinks(config.Root)
	if e != nil {
		return proof, domain.SessionDeletionPending()
	}
	if e := security.PrivateDir(filepath.Join(root, "session-deletions")); e != nil {
		return proof, domain.SessionDeletionPending()
	}
	lock, e := security.TryLock(sessionDeletionObligationPath(root, w, ".lock"))
	if e != nil {
		return proof, domain.SessionDeletionPending()
	}
	defer lock.Close()
	path := sessionDeletionObligationPath(root, w, ".json")
	if err := loadSessionDeletionProof(root, path, w, &proof); err != nil {
		return proof, err
	}
	if proof.Complete {
		if err := cleanupGeneratedCopies(root, w, true); err != nil {
			return proof, err
		}
		for _, ref := range w.Images {
			if err := (imageinput.Manager{Root: root}).Removed(w.MachineID, ref); err != nil {
				return proof, err
			}
		}
		// Completion is reusable only while its original copies remain absent.
		// A replacement at a formerly owned path is never blindly removed.
		paths, e := sessionDeletionCopyPaths(ctx, root, w)
		if e != nil {
			return proof, e
		}
		if len(w.Copies) != 0 || w.Fork != nil {
			paths = append(paths, filepath.Join(root, "workspaces", string(w.SessionID)))
		}
		if w.Fork != nil {
			paths = append(paths, workspace.SidechatForkClaimPath(root, w.Fork.JobID))
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
		raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(w.Fork.RuntimeID), "fork-completion.json"), maxOpenCodeExecutionCheckpointBytes)
		var checkpoint ForkCheckpoint
		valid := err == nil && executionInputDigest(raw) == w.Fork.CheckpointDigest
		if valid {
			var native openCodeForkCheckpoint
			if domain.DecodeWithLimit(raw, &native, maxOpenCodeExecutionCheckpointBytes) == nil && native.Version == 2 {
				valid = native.JobID == w.Fork.JobID && native.JobInputDigest == w.Fork.JobInputDigest && native.RuntimeID == w.Fork.RuntimeID && native.SessionID == w.SessionID && native.MachineID == w.MachineID && string(mustForkJSON(native)) == string(raw)
			} else {
				valid = len(raw) <= maxExecutionCheckpointBytes && domain.DecodeWithLimit(raw, &checkpoint, maxExecutionCheckpointBytes) == nil && ((checkpoint.Version == 1 && checkpoint.SidechatPolicy == "") || (checkpoint.Version == 3 && checkpoint.SidechatPolicy == domain.CodexReadOnlySidechatV1 && checkpoint.Native.Effective.Sandbox.Type == codex.ReadOnly && checkpoint.Native.Effective.ApprovalPolicy == codex.ApprovalNever)) && checkpoint.JobID == w.Fork.JobID && checkpoint.JobInputDigest == w.Fork.JobInputDigest && checkpoint.RuntimeID == w.Fork.RuntimeID && checkpoint.SessionID == w.SessionID && checkpoint.MachineID == w.MachineID && string(mustForkJSON(checkpoint)) == string(raw)
			}
		}
		if !valid {
			return proof, domain.SessionDeletionPending()
		}
	}
	if !proof.RemovalStarted {
		for _, f := range w.RetryForks {
			raw, err := security.ReadPrivate(filepath.Join(root, "runtimes", string(f.RuntimeID), "fork-completion.json"), maxExecutionCheckpointBytes)
			var checkpoint ForkCheckpoint
			if err != nil || executionInputDigest(raw) != f.CheckpointDigest || domain.Decode(raw, &checkpoint) != nil || checkpoint.Version != 3 || checkpoint.SidechatPolicy != domain.CodexReadOnlySidechatV1 || checkpoint.JobID != f.JobID || checkpoint.JobInputDigest != f.JobInputDigest || checkpoint.RuntimeID != f.RuntimeID || checkpoint.SessionID != w.SessionID || checkpoint.MachineID != w.MachineID || checkpoint.Native.Effective.Sandbox.Type != codex.ReadOnly || checkpoint.Native.Effective.ApprovalPolicy != codex.ApprovalNever || string(mustForkJSON(checkpoint)) != string(raw) {
				return proof, domain.SessionDeletionPending()
			}
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
			if copy.UnpublishedChildProcessID != "" && len(j.Output) != 0 {
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
	// Ownership validation is complete. Bind every managed path to the object (or
	// proven absence) observed now, so removal never adopts a later replacement.
	validated, e := captureSessionCopyIdentities(ctx, root, w)
	if e != nil {
		config.Logger.WarnContext(ctx, "session_deletion_identity_capture_failed", "deletion_id", w.DeletionID, "code", domain.SafeError(e).Code)
		return proof, domain.SessionDeletionPending()
	}
	manager := workspace.Manager{NamedDirectories: config.namedWorkspaceDirectories, Root: root, Logger: config.Logger}
	for _, copy := range w.Copies {
		if copy.UnpublishedChildProcessID != "" {
			if proof.RemovalStarted {
				for _, path := range []string{filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)), filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)+".recovery.lock")} {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						return proof, domain.SessionDeletionPending()
					}
				}
			} else if err := process.RetireCompletedOwnerContext(ctx, filepath.Join(root, "processes"), copy.UnpublishedChildProcessID, nil); err != nil {
				return proof, domain.SessionDeletionPending()
			}
		}
		if copy.UnpublishedSidechatID != "" {
			if err := manager.DiscardInterruptedSidechatFork(ctx, copy.JobID, w.SessionID, copy.UnpublishedSidechatID); err != nil {
				return proof, err
			}
		}
	}
	unpublishedPaths := map[string]bool{}
	for _, copy := range w.Copies {
		if copy.UnpublishedChildProcessID != "" {
			unpublishedPaths[filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID))] = true
			unpublishedPaths[filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)+".recovery.lock")] = true
		}
		if copy.UnpublishedSidechatID != "" {
			unpublishedPaths[filepath.Join(root, "workspaces", string(copy.UnpublishedSidechatID))] = true
			unpublishedPaths[workspace.SidechatForkClaimPath(root, copy.JobID)] = true
		}
	}
	if len(w.Copies) != 0 || w.Fork != nil {
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
			for _, binding := range w.SkillSnapshots {
				if err := (skills.Manager{Root: root}).DeletePreparedSnapshot(ctx, w.MachineID, binding); err != nil {
					return domain.SessionDeletionPending()
				}
			}
			if sessionDeletionBeforeRemovalHook != nil {
				sessionDeletionBeforeRemovalHook()
			}
			paths, e := sessionDeletionCopyPaths(ctx, root, w)
			if e != nil {
				return e
			}
			for _, path := range paths {
				if unpublishedPaths[path] {
					if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
						return domain.SessionDeletionPending()
					}
					continue
				}
				// A path missing from the validated inventory was proven absent.
				identity, e := sessionCopyIdentity(root, path, validated)
				if e == nil {
					e = removeSessionCopy(ctx, root, path, identity)
				}
				if e != nil {
					config.Logger.WarnContext(ctx, "session_deletion_copy_removal_pending", "deletion_id", w.DeletionID, "code", domain.SafeError(e).Code)
					return e
				}
			}
			// Re-inventory dynamic storage namespaces at the completion boundary. A
			// final root published after the first inventory is absence-only: never
			// let the generic remover adopt it, and keep the deletion recoverable.
			remaining, e := workspace.SessionStorageRemnantPaths(ctx, root, w)
			if e != nil {
				return e
			}
			if len(remaining) != 0 {
				return domain.SessionDeletionPending()
			}
			return nil
		})
		if e != nil {
			return proof, e
		}
	}
	if w.Fork != nil {
		if err := manager.RetirePublishedSidechatFork(ctx, w.Fork.JobID, w.SessionID); err != nil {
			return proof, err
		}
	}
	// All original native and workspace owners have joined before image
	// removal. Opaque references never grant access to original Local files.
	if !proof.RemovalStarted {
		proof.RemovalStarted = true
		if err := writeJSON(path, proof); err != nil {
			return proof, err
		}
	}
	for _, binding := range w.SkillSnapshots {
		if err := (skills.Manager{Root: root}).DeletePreparedSnapshot(ctx, w.MachineID, binding); err != nil {
			return proof, domain.SessionDeletionPending()
		}
	}
	if err := cleanupGeneratedCopies(root, w, false); err != nil {
		return proof, err
	}
	images := imageinput.Manager{Root: root}
	for _, ref := range w.Images {
		if _, err := images.Transfer(w.MachineID, &pb.AttachmentTransfer{Id: string(domain.NewID()), Attachment: imageinput.ToProto(ref), Operation: pb.AttachmentTransferOperation_ATTACHMENT_TRANSFER_OPERATION_DELETE}); err != nil {
			return proof, err
		}
		if err := images.Removed(w.MachineID, ref); err != nil {
			return proof, err
		}
	}
	proof.Complete = true
	return proof, writeJSON(path, proof)
}

// loadSessionDeletionProof recovers or creates the proof owned by exactly this
// obligation. The session tombstone is written first so admission closes before
// any cleanup. A legacy session-keyed proof migrates only for the exact original
// obligation (keeping its report ID); any other legacy scope stays pending.
func loadSessionDeletionProof(root, path string, w domain.SessionDeletionWork, proof *sessionDeletionProof) error {
	tombstone := sessionDeletionPath(root, w.SessionID)
	legacy := sessionDeletionProof{}
	legacyProof := false
	raw, e := security.ReadPrivate(tombstone, 4096)
	switch {
	case errors.Is(e, os.ErrNotExist):
		if err := writeJSON(tombstone, sessionDeletionProof{Version: sessionDeletionTombstoneVersion}); err != nil {
			return err
		}
	case e != nil:
		return domain.SessionDeletionPending()
	case domain.Decode(raw, &legacy) != nil:
		return domain.SessionDeletionPending()
	case legacy.Version == sessionDeletionTombstoneVersion && legacy.Digest == "" && legacy.ReportID == "" && !legacy.RemovalStarted && !legacy.Complete:
	case legacy.Version == 1 && legacy.ReportID.Validate() == nil:
		legacyProof = true
	default:
		return domain.SessionDeletionPending()
	}
	raw, e = security.ReadPrivate(path, 4096)
	if e == nil {
		if domain.Decode(raw, proof) != nil || proof.Version != 1 || proof.ReportID.Validate() != nil {
			return domain.SessionDeletionPending()
		}
		if proof.Digest != w.Digest() {
			// An untouched proof can admit only the same immutable work plus
			// server-proved child owners. Removed evidence cannot be rebuilt.
			if !sessionDeletionUpgradable(*proof, w) {
				return domain.SessionDeletionPending()
			}
			proof.Digest = w.Digest()
			if err := writeJSON(path, *proof); err != nil {
				return err
			}
		}
		if legacyProof && (legacy.Digest == w.Digest() || sessionDeletionUpgradable(legacy, w)) {
			// A crash left the already migrated legacy proof in place. Its
			// workspace proof moves before the tombstone drops the legacy scope.
			if legacy.Digest == w.Digest() {
				if err := workspace.MigrateLegacyWorkspaceProof(root, w); err != nil {
					return err
				}
			}
			return writeJSON(tombstone, sessionDeletionProof{Version: sessionDeletionTombstoneVersion})
		}
		return nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	if legacyProof {
		if legacy.Digest != w.Digest() && !sessionDeletionUpgradable(legacy, w) {
			return domain.SessionDeletionPending()
		}
		// A completed legacy proof takes the Complete fast path and never reaches
		// workspace cleanup, so the exact obligation's session-keyed workspace
		// proof migrates here, before the tombstone drops the legacy scope.
		exact := legacy.Digest == w.Digest()
		legacy.Digest = w.Digest()
		*proof = legacy
		if err := writeJSON(path, *proof); err != nil {
			return err
		}
		if exact {
			if err := workspace.MigrateLegacyWorkspaceProof(root, w); err != nil {
				return err
			}
		}
		return writeJSON(tombstone, sessionDeletionProof{Version: sessionDeletionTombstoneVersion})
	}
	return writeJSON(path, *proof)
}

func sessionDeletionUpgradable(proof sessionDeletionProof, w domain.SessionDeletionWork) bool {
	if proof.RemovalStarted || proof.Complete {
		return false
	}
	legacy := w
	legacy.Copies = append([]domain.SessionDeletionCopy(nil), w.Copies...)
	for i := range legacy.Copies {
		legacy.Copies[i].UnpublishedChildProcessID = ""
	}
	return proof.Digest == legacy.Digest()
}

// sessionDeletionBeforeRemovalHook runs after ownership validation and identity
// capture, immediately before managed copies are removed. Tests use it to
// install replacements deterministically; it is nil in production.
var sessionDeletionBeforeRemovalHook func()

// captureSessionCopyIdentities records the identity of every inventoried
// managed path (or proven absence) once ownership validation has finished.
func captureSessionCopyIdentities(ctx context.Context, root string, w domain.SessionDeletionWork) (map[string]security.PathIdentity, error) {
	paths, e := sessionDeletionCopyPaths(ctx, root, w)
	if e != nil {
		return nil, e
	}
	identities := make(map[string]security.PathIdentity, len(paths))
	for _, path := range paths {
		if e := ctx.Err(); e != nil {
			return nil, domain.SafeError(e)
		}
		identity, e := security.CapturePathIdentity(path)
		if e != nil {
			return nil, e
		}
		identities[path] = identity
	}
	return identities, nil
}

// sessionCopyIdentity returns the identity that bounds removal of path. Process
// owner maintenance locks are the one exception to proven absence: the Worker's
// own owner reconciliation creates them while the workspace is retired, after
// validation. They are adopted only when they are empty private regular files,
// so a foreign object or directory at that name still stays pending.
func sessionCopyIdentity(root, path string, validated map[string]security.PathIdentity) (security.PathIdentity, error) {
	identity := validated[path]
	if identity.Present() || filepath.Dir(path) != filepath.Join(root, "processes") || !strings.HasSuffix(path, ".recovery.lock") {
		return identity, nil
	}
	info, e := os.Lstat(path)
	if errors.Is(e, os.ErrNotExist) {
		return identity, nil
	}
	if e != nil || !info.Mode().IsRegular() || info.Size() != 0 || security.RegularPrivate(path) != nil {
		return identity, domain.SessionDeletionPending()
	}
	return security.CapturePathIdentity(path)
}

// Workspace cleanup already validated original storage namespace ownership.
// Reappearance cannot give the generic copy remover new traversal authority.
// The validated identity is the only object the generic remover may delete.
func removeSessionCopy(ctx context.Context, root, path string, validated security.PathIdentity) error {
	parent := filepath.Dir(path)
	name := filepath.Base(path)
	canonicalFinalClaim := parent == filepath.Join(root, "storage-removal-root-claims") && len(name) == 41 && name[36:] == ".json" && domain.ID(name[:36]).Validate() == nil
	if parent == filepath.Join(root, "snapshot-staging") || parent == filepath.Join(root, "workspace-removals") || parent == filepath.Join(root, "workspace-removal-roots") || parent == filepath.Join(root, "workspace-removal-quarantine") || canonicalFinalClaim {
		// Workspace cleanup already checked the original native staging,
		// public removal, final-root identity, or final-root claim. A later
		// replacement or an old name without a published proof remains
		// protected here. The generic session remover must never acquire
		// authority over it.
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
		return nil
	}
	if parent == filepath.Join(root, "skill-snapshots") {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
		return nil
	}
	return removeSessionTree(ctx, root, path, validated)
}

// Walk first without following symlinks, then remove deepest paths first. Each
// operation observes cancellation and the explicit entry bound; original Local
// checkouts and shared browser/account profiles never enter this path list.
func removeSessionTree(ctx context.Context, root, path string, validated security.PathIdentity) error {
	return security.RemoveOwnedTreeIdentity(ctx, root, path, validated)
}

// Removal and completed-proof replay share one inventory, including dynamic
// title runtimes. A restored or replaced copy blocks acknowledgement; replay
// never gains permission to delete it merely from the earlier completed proof.
func sessionDeletionCopyPaths(ctx context.Context, root string, w domain.SessionDeletionWork) ([]string, error) {
	paths := make([]string, 0, len(w.SkillSnapshots)+5)
	for _, binding := range w.SkillSnapshots {
		paths = append(paths, filepath.Join(root, "skill-snapshots", string(binding.SnapshotID)))
	}
	// Skill-only and skill/image plans still observe every original snapshot
	// on completed replay. They grant no execution or workspace authority.
	if len(w.Copies) == 0 && w.Fork == nil {
		return paths, nil
	}
	paths = append(paths, filepath.Join(root, "execution-claims", string(w.SessionID)+".json"), filepath.Join(root, "execution-history", string(w.SessionID)), filepath.Join(root, "pr-startup", string(w.SessionID)), filepath.Join(root, "processes", string(w.SessionID)), filepath.Join(root, "processes", string(w.SessionID)+".recovery.lock"))
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
	for _, f := range w.RetryForks {
		paths = append(paths, filepath.Join(root, "runtimes", string(f.RuntimeID)))
	}
	titlePrefixes := map[string]bool{}
	for _, copy := range w.Copies {
		if e := ctx.Err(); e != nil {
			return nil, domain.SafeError(e)
		}
		paths = append(paths, filepath.Join(root, "jobs", string(copy.JobID)), filepath.Join(root, "jobs", string(copy.JobID)+".json"), filepath.Join(root, "workspace-recovery", string(copy.JobID)+".json"), filepath.Join(root, "processes", string(copy.JobID)), filepath.Join(root, "processes", string(copy.JobID)+".recovery.lock"))
		if copy.UnpublishedChildProcessID != "" {
			paths = append(paths, filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)), filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)+".recovery.lock"))
		}
		if copy.UnpublishedSidechatID != "" {
			paths = append(paths, workspace.SidechatForkClaimPath(root, copy.JobID), filepath.Join(root, "workspaces", string(copy.UnpublishedSidechatID)))
		}
		if copy.ExecutionID != "" {
			paths = append(paths, filepath.Join(root, "runtimes", string(copy.ExecutionID)), filepath.Join(root, "pr-git", string(copy.ExecutionID)))
		}
		if copy.Type == domain.NativeCodeReviewJob {
			paths = append(paths, filepath.Join(root, "runtimes", string(copy.ActionID)))
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

// Match only the original server-issued ownership envelope. Generic unavailable,
// canceled or recovery errors never imply deletion authority or permit replay.
func retiringAssignment(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID, resource *pb.Resource) bool {
	if resource == nil || domain.ID(resource.SessionId).Validate() != nil || domain.ID(resource.Id).Validate() != nil {
		return false
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r, err := client.ListSessionDeletionWork(bounded, authenticated(credential, &pb.ListSessionDeletionWorkRequest{MachineId: string(credential.MachineID), InstanceId: string(instance), OriginalSessionId: resource.SessionId, OriginalJobId: resource.Id}))
	if err != nil || len(r.Msg.WorkJson) != 1 || r.Msg.NextSessionId != "" {
		return false
	}
	var w domain.SessionDeletionWork
	if domain.DecodeWithLimit(r.Msg.WorkJson[0], &w, domain.MaxSessionDeletionBytes) != nil || w.Validate() != nil || w.ServerID != credential.ServerID || w.DeviceID != credential.DeviceID || w.MachineID != credential.MachineID || string(w.SessionID) != resource.SessionId {
		return false
	}
	for _, copy := range w.Copies {
		if string(copy.JobID) == resource.Id && copy.InstanceID == instance && copy.Revision == resource.Revision && copy.Digest == executionInputDigest(resource.DocumentJson) {
			return true
		}
	}
	return false
}

// Original retained copies already passed process, workspace and generation
// checks above. Their durable output intents include never-published bytes.
func cleanupGeneratedCopies(root string, w domain.SessionDeletionWork, observe bool) error {
	manager := imageinput.Manager{Root: root}
	for _, copy := range w.Copies {
		if copy.Type != domain.ExecuteSessionJob || copy.ExecutionID == "" {
			continue
		}
		owner := imageinput.GenerationOwner{JobID: copy.JobID, ExecutionID: copy.ExecutionID, InstanceID: copy.InstanceID, MachineID: w.MachineID}
		if err := manager.CleanupGenerated(owner, w.PreservedGeneratedImages, observe); err != nil {
			return err
		}
	}
	return nil
}
