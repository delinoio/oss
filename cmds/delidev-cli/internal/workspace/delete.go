// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type deletionWorkspaceProof struct {
	Version  uint32    `json:"version"`
	Digest   string    `json:"digest"`
	Manifest *Manifest `json:"manifest,omitempty"`
	Removed  bool      `json:"removed"`
}

// DeleteOwnedWorkspace holds the same lock as preparation, execution and reads.
// Its external proof retains the original manifest across a crash during Git
// worktree removal. Local paths are never traversed or deleted.
func (m *Manager) DeleteOwnedWorkspace(ctx context.Context, w domain.SessionDeletionWork, allowAbsent bool, removeCopies func() error) (returned error) {
	stage := "validate"
	defer func() {
		if m.Logger != nil {
			code := domain.Code("")
			if returned != nil {
				code = domain.SafeError(returned).Code
			}
			m.Logger.InfoContext(ctx, "workspace_deletion_finished", "session_id", w.SessionID, "stage", stage, "code", code)
		}
	}()
	if w.Validate() != nil {
		return domain.SessionDeletionPending()
	}
	if e := m.initialize(); e != nil {
		return e
	}
	stage = "session-lock"
	lock, e := security.TryLock(filepath.Join(m.Root, "locks", string(w.SessionID)+".lock"))
	if e != nil {
		return domain.SessionDeletionPending()
	}
	defer lock.Close()
	observationLock, err := m.lockWorkspaceObservations(ctx, w.SessionID)
	if err != nil {
		return domain.SessionDeletionPending()
	}
	defer observationLock.Close()
	publication, err := m.lockSnapshotNamespace()
	if err != nil {
		return domain.SessionDeletionPending()
	}
	defer publication.Close()
	if err := m.requireNoSidechatReferences(ctx, w.SessionID); err != nil {
		return err
	}
	stage = "storage-removal-ownership"
	if err := m.cleanupDeletionRemovals(ctx, w); err != nil {
		return err
	}
	stage = "storage-staging-ownership"
	if err := m.cleanupDeletionStaging(ctx, w); err != nil {
		return err
	}
	stage = "final-root-ownership"
	if err := m.cleanupDeletionFinalRoots(ctx, w); err != nil {
		return err
	}
	storedManifest, e := m.deletionSnapshotManifest(ctx, w)
	if e != nil {
		return e
	}
	root := filepath.Join(m.Root, "workspaces", string(w.SessionID))
	stage = "retained-proof"
	proofPath := filepath.Join(m.Root, "session-deletions", SessionDeletionObligationName(w)+"-workspace.json")
	proof := deletionWorkspaceProof{Version: 1, Digest: w.Digest()}
	if err := migrateLegacyWorkspaceProof(m.Root, proofPath, w); err != nil {
		return err
	}
	b, e := security.ReadPrivate(proofPath, 1<<20)
	if e == nil {
		if domain.Decode(b, &proof) != nil || proof.Version != 1 || proof.Digest != w.Digest() {
			return domain.SessionDeletionPending()
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	} else {
		if _, e := os.Lstat(root); e == nil {
			manifest, e := m.Read(w.SessionID)
			if e != nil {
				return e
			}
			if manifest.MachineID != w.MachineID || !slices.Contains(w.PreparationDigests, manifest.InputDigest) || !manifest.Type.Valid() {
				return domain.SessionDeletionPending()
			}
			proof.Manifest = &manifest
		} else if !errors.Is(e, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		} else if storedManifest != nil {
			proof.Manifest = storedManifest
		} else if !allowAbsent {
			// The managed workspace directory is shared by every device obligation
			// of this session and machine. Another obligation may already have
			// removed it; only its session-scoped, content-free removal evidence
			// lets this obligation acknowledge the same absence.
			removed, e := m.sharedWorkspaceRemoved(w)
			if e != nil {
				return e
			}
			if !removed {
				return domain.SessionDeletionPending()
			}
			proof.Removed = true
		}
		if e := writeDeletionWorkspaceProof(proofPath, proof); e != nil {
			return e
		}
	}
	stage = "owned-manifest"
	if proof.Manifest != nil {
		manifest := *proof.Manifest
		if err := m.requireNamedDirectories(PrepareRequest{Repositories: manifestRepositorySpecs(manifest)}); err != nil {
			return err
		}
		if manifest.SessionID != w.SessionID || manifest.MachineID != w.MachineID || !slices.Contains(w.PreparationDigests, manifest.InputDigest) {
			return domain.SessionDeletionPending()
		}
		if _, e := os.Lstat(root); e == nil {
			if e := security.CheckPrivateDir(root); e != nil {
				return domain.SessionDeletionPending()
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
		git := m.Git
		git.OwnerID = w.SessionID
		// Only a present owned process index can prove termination. General Chat
		// preparation itself launches no Git child. Other original job processes
		// are independently reconciled by the Worker before this callback.
		stage = "owned-processes"
		processRoot := filepath.Join(m.Root, "processes", string(w.SessionID))
		if _, e := os.Lstat(processRoot); e == nil {
			if e := process.ReconcileOwnerContext(ctx, git.ProcessRoot, w.SessionID); e != nil {
				return e
			}
		} else if !errors.Is(e, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
		independent := false
		if _, err := os.Lstat(root); err == nil {
			independent, e = m.deletionRestoredWorkspace(ctx, w, manifest)
			if e != nil {
				return e
			}
		}
		if !independent && manifest.ManagedRootDigest != "" {
			if _, err := os.Lstat(root); err == nil {
				current, err := directoryIdentityDigest(root)
				if err != nil || current != manifest.ManagedRootDigest {
					return domain.SessionDeletionPending()
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return domain.SessionDeletionPending()
			}
		}
		stage = "repository-ownership"
		for i := len(manifest.Repositories) - 1; i >= 0; i-- {
			repo := manifest.Repositories[i]
			if manifest.Reference != nil {
				if repo.Owned || manifest.Reference.SessionID.Validate() != nil || manifest.Reference.SessionID == w.SessionID {
					return domain.SessionDeletionPending()
				}
				continue
			}
			if manifest.Type == domain.Local {
				if repo.Owned || repo.Path != repo.Source || sameNativePath(repo.Source, root) || strings.HasPrefix(repo.Source, root+string(filepath.Separator)) {
					return domain.SessionDeletionPending()
				}
				continue
			}
			if manifest.Type != domain.Worktree || !repo.Owned || validateFrozenDirectory(repo.DirectoryName) != nil || repo.ID.Validate() != nil || repo.Path != ownedRepositoryPath(root, repo.ID, repo.DirectoryName) || repo.SourceKind == CheckoutSource && repo.Source == repo.Path {
				return domain.SessionDeletionPending()
			}
			if independent {
				// Independent restored Git belongs to the managed root. Do not run
				// worktree removal against the user's separate source Git store.
				continue
			}
			if repo.SourceKind.managed() {
				if _, e := os.Lstat(root); e == nil {
					if verifyIndependentDirectory(repo, true) != nil {
						return domain.SessionDeletionPending()
					}
				} else if !errors.Is(e, os.ErrNotExist) {
					return domain.SessionDeletionPending()
				}
				continue
			}
			info, e := os.Lstat(repo.Path)
			if e == nil {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					return domain.SessionDeletionPending()
				}
				// Positive common-directory and linked-administration comparison
				// protects an unrelated replacement at the expected managed path.
				stage = "git-common-identity"
				source, e := git.run(ctx, repo.Source, "rev-parse", "--path-format=absolute", "--git-common-dir")
				if e != nil {
					return e
				}
				fields, e := git.revParseFields(ctx, repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
				if e != nil || fields[0] != trimGit(source) {
					return domain.SessionDeletionPending()
				}
				rel, e := filepath.Rel(filepath.Join(fields[0], "worktrees"), fields[1])
				if e != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return domain.SessionDeletionPending()
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return domain.SessionDeletionPending()
			}
			stage = "git-registration"
			list, e := git.run(ctx, repo.Source, "worktree", "list", "--porcelain", "-z")
			if e != nil {
				return e
			}
			present := false
			for _, field := range strings.Split(string(list), "\x00") {
				if path, ok := strings.CutPrefix(field, "worktree "); ok && sameNativePath(path, repo.Path) {
					present = true
				}
			}
			stage = "git-removal"
			if present {
				if _, e := git.run(ctx, repo.Source, "worktree", "remove", "--force", "--", repo.Path); e != nil {
					return e
				}
			} else if info != nil {
				return domain.SessionDeletionPending()
			}
		}
	}
	stage = "workspace-removal"
	if !proof.Removed {
		// The manifest was persisted outside the directory before removal. Missing
		// paths on recovery therefore preserve the exact original deletion intent.
		var removeErr error
		if proof.Manifest != nil && proof.Manifest.Reference != nil {
			removeErr = m.removeSidechatMetadata(ctx, root, *proof.Manifest)
		} else {
			removeErr = security.RemoveOwnedTree(ctx, m.Root, root)
		}
		if e := removeErr; e != nil {
			return domain.SafeError(e)
		}
		if e := security.SyncParent(root); e != nil {
			return domain.SafeError(e)
		}
		// Publish the shared evidence before this obligation's own proof so a
		// crash between the writes never strands another device obligation.
		if e := writeSharedWorkspaceRemoval(m.Root, w); e != nil {
			return e
		}
		proof.Removed = true
		if e := writeDeletionWorkspaceProof(proofPath, proof); e != nil {
			return e
		}
	} else if _, e := os.Lstat(root); !errors.Is(e, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	stage = "copy-removal"
	if e := removeCopies(); e != nil {
		return e
	}
	// Retain only a non-content cleanup acknowledgement after all copies commit.
	proof.Manifest = nil
	return writeDeletionWorkspaceProof(proofPath, proof)
}

// deletionWorkspaceRemoval is the session-scoped, content-free evidence that the
// managed workspace directory of one session on one machine was removed. It
// holds no manifest, path or device identity, so it cannot grant ownership of
// any content; it only lets later device obligations verify the earlier removal.
type deletionWorkspaceRemoval struct {
	Version   uint32    `json:"version"`
	ServerID  domain.ID `json:"server_id"`
	SessionID domain.ID `json:"session_id"`
	MachineID domain.ID `json:"machine_id"`
	Removed   bool      `json:"removed"`
}

func sharedWorkspaceRemovalPath(root string, w domain.SessionDeletionWork) string {
	return filepath.Join(root, "session-deletions", string(w.SessionID)+"-workspace-removed.json")
}

// sharedWorkspaceRemoved reports whether another obligation of the same server,
// session and machine already removed the shared workspace directory. A missing
// record is false; an unreadable or foreign record keeps the obligation pending.
func (m *Manager) sharedWorkspaceRemoved(w domain.SessionDeletionWork) (bool, error) {
	raw, e := security.ReadPrivate(sharedWorkspaceRemovalPath(m.Root, w), 1<<16)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	var evidence deletionWorkspaceRemoval
	if e != nil || domain.Decode(raw, &evidence) != nil || evidence.Version != 1 || !evidence.Removed || evidence.ServerID != w.ServerID || evidence.SessionID != w.SessionID || evidence.MachineID != w.MachineID {
		return false, domain.SessionDeletionPending()
	}
	if m.Logger != nil {
		m.Logger.Info("workspace_deletion_shared_removal_adopted", "session_id", w.SessionID, "deletion_id", w.DeletionID)
	}
	return true, nil
}

func writeSharedWorkspaceRemoval(root string, w domain.SessionDeletionWork) error {
	b, e := json.Marshal(deletionWorkspaceRemoval{Version: 1, ServerID: w.ServerID, SessionID: w.SessionID, MachineID: w.MachineID, Removed: true})
	if e != nil || security.WriteAtomic(sharedWorkspaceRemovalPath(root, w), b) != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}

// SessionDeletionObligationName identifies one original device obligation. Its
// retained proofs and locks never alias another device's obligation.
func SessionDeletionObligationName(w domain.SessionDeletionWork) string {
	return string(w.SessionID) + "-" + string(w.DeletionID) + "-" + string(w.DeviceID)
}

// migrateLegacyWorkspaceProof moves the session-keyed proof to this obligation
// only when its digest is exactly this work. A different legacy digest is left
// in place and keeps this obligation pending.
func migrateLegacyWorkspaceProof(root, proofPath string, w domain.SessionDeletionWork) error {
	legacyPath := filepath.Join(root, "session-deletions", string(w.SessionID)+"-workspace.json")
	raw, e := security.ReadPrivate(legacyPath, 1<<20)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return domain.SessionDeletionPending()
	}
	var legacy deletionWorkspaceProof
	if domain.Decode(raw, &legacy) != nil || legacy.Version != 1 {
		return domain.SessionDeletionPending()
	}
	if legacy.Digest != w.Digest() {
		if _, err := os.Lstat(proofPath); err == nil {
			return nil
		}
		return domain.SessionDeletionPending()
	}
	if _, err := os.Lstat(proofPath); errors.Is(err, os.ErrNotExist) {
		if err := writeDeletionWorkspaceProof(proofPath, legacy); err != nil {
			return err
		}
	} else if err != nil {
		return domain.SessionDeletionPending()
	}
	if legacy.Removed {
		// The legacy session-keyed proof recorded the shared directory's removal
		// for this exact obligation. Preserve it as session-scoped evidence so
		// other device obligations can verify it before the legacy file is gone.
		if err := writeSharedWorkspaceRemoval(root, w); err != nil {
			return err
		}
	}
	if err := os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	return nil
}

// MigrateLegacyWorkspaceProof moves a session-keyed workspace proof to the exact
// obligation that owns it, even when that obligation's session proof is already
// complete and DeleteOwnedWorkspace is never reached. Without a legacy proof it
// does nothing.
func MigrateLegacyWorkspaceProof(root string, w domain.SessionDeletionWork) error {
	return migrateLegacyWorkspaceProof(root, filepath.Join(root, "session-deletions", SessionDeletionObligationName(w)+"-workspace.json"), w)
}

func writeDeletionWorkspaceProof(path string, v deletionWorkspaceProof) error {
	b, e := json.Marshal(v)
	if e != nil {
		return domain.SessionDeletionPending()
	}
	if e := security.WriteAtomic(path, b); e != nil {
		return domain.SessionDeletionPending()
	}
	return nil
}
