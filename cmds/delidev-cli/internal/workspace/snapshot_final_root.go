// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type storageFinalRootStage string

const (
	storageFinalRootBeforeClaim       storageFinalRootStage = "before-claim"
	storageFinalRootPrepared          storageFinalRootStage = "prepared"
	storageFinalRootRenamed           storageFinalRootStage = "renamed"
	storageFinalRootClaimed           storageFinalRootStage = "claimed"
	storageFinalRootUnlinkReady       storageFinalRootStage = "unlink-ready"
	storageFinalRootVerified          storageFinalRootStage = "verified"
	storageFinalRootBeforeUnlink      storageFinalRootStage = "before-unlink"
	storageFinalRootNativeGone        storageFinalRootStage = "native-unlinked"
	storageFinalRootAfterVerification storageFinalRootStage = "after-verification"
	storageFinalRootUnlinked          storageFinalRootStage = "unlinked"
	storageFinalRootSynced            storageFinalRootStage = "synced"
	storageFinalRootRemoved           storageFinalRootStage = "removed"
)

// The separate private parent is outside the directory retained by source
// writers. Its fresh UUID is claimed without replacement before verification.
// The old removal name never becomes an unlink operand again.
type storageFinalRootClaim struct {
	RootName     domain.ID               `json:"root_name"`
	Version      uint32                  `json:"version"`
	Reference    StorageRemovalReference `json:"reference"`
	IntentDigest string                  `json:"intent_digest"`
	RootIdentity string                  `json:"root_identity"`
	State        storageFinalRootStage   `json:"state"`
}

func (m *Manager) finalRemovalRoot(claim storageFinalRootClaim) string {
	return filepath.Join(m.Root, "workspace-removal-roots", string(claim.Reference.OperationID)+"-"+string(claim.RootName))
}

func (m *Manager) finalRemovalClaimPath(id domain.ID) string {
	return filepath.Join(m.Root, "storage-removal-root-claims", string(id)+".json")
}

func (m *Manager) readFinalRemovalClaim(r StorageRequest, original storageRemovalClaim) (storageFinalRootClaim, error) {
	raw, err := security.ReadPrivate(m.finalRemovalClaimPath(r.OperationID), 4096)
	var claim storageFinalRootClaim
	if err != nil || domain.Decode(raw, &claim) != nil || claim.Version != 1 || claim.RootName.Validate() != nil || claim.Reference != removalReference(r) || claim.IntentDigest != original.IntentDigest || claim.RootIdentity != original.RootIdentity || claim.RootIdentity == "" {
		return claim, ResultUncertain()
	}
	switch claim.State {
	case storageFinalRootPrepared, storageFinalRootClaimed, storageFinalRootUnlinkReady, storageFinalRootUnlinked, storageFinalRootRemoved:
		return claim, nil
	default:
		return claim, ResultUncertain()
	}
}

func (m *Manager) writeFinalRemovalClaim(ctx context.Context, claim storageFinalRootClaim, state storageFinalRootStage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim.State = state
	raw, err := json.Marshal(claim)
	if err != nil {
		return ResultUncertain()
	}
	return security.WriteAtomicOwned(m.finalRemovalClaimPath(claim.Reference.OperationID), raw)
}

func (m *Manager) finalRootFault(stage storageFinalRootStage) error {
	if m.storageFinalRootFault != nil {
		return m.storageFinalRootFault(stage)
	}
	return nil
}

func storageNameAbsent(path string) error {
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	return nil
}

func (m *Manager) finalRemovalAbsent(id domain.ID) error {
	if err := storageNameAbsent(filepath.Join(m.Root, "workspace-removals", string(id))); err != nil {
		return err
	}
	paths, err := finalRemovalNamespacePaths(context.Background(), m.Root, map[domain.ID]bool{id: true})
	if err != nil || len(paths) != 0 {
		return ResultUncertain()
	}
	return nil
}

// Once the claim is decoded, its exact private name is the only expected
// namespace entry. This avoids rescanning the shared namespace during every
// recovery step while still rejecting a reappearing public name or private
// replacement.
func (m *Manager) finalRemovalClaimAbsent(claim storageFinalRootClaim) error {
	if err := storageNameAbsent(filepath.Join(m.Root, "workspace-removals", string(claim.Reference.OperationID))); err != nil {
		return err
	}
	return storageNameAbsent(m.finalRemovalRoot(claim))
}

func (m *Manager) claimFinalRemovalRoot(ctx context.Context, r StorageRequest, original storageRemovalClaim) error {
	if err := m.finalRootFault(storageFinalRootBeforeClaim); err != nil {
		return err
	}
	if err := storageNameAbsent(m.finalRemovalClaimPath(r.OperationID)); err != nil {
		return err
	}
	paths, err := finalRemovalNamespacePaths(ctx, m.Root, map[domain.ID]bool{r.OperationID: true})
	if err != nil || len(paths) != 0 {
		return ResultUncertain()
	}
	claim := storageFinalRootClaim{RootName: domain.NewID(), Version: 1, Reference: removalReference(r), RootIdentity: original.RootIdentity, IntentDigest: original.IntentDigest}
	if err := m.writeFinalRemovalClaim(ctx, claim, storageFinalRootPrepared); err != nil {
		return err
	}
	if err := m.finalRootFault(storageFinalRootPrepared); err != nil {
		return err
	}
	return m.finishFinalRootRemoval(ctx, r, original)
}

// Recovery may resume only this original transition. In particular, a missing
// root with unlink-ready proof is uncertain: a crash between unlink and its
// durable receipt cannot be distinguished from an external namespace move.
func (m *Manager) finishFinalRootRemoval(ctx context.Context, r StorageRequest, original storageRemovalClaim) (returned error) {
	return m.finishFinalRootRemovalWithNamespace(ctx, r, original, nil)
}

// finishFinalRootRemovalWithNamespace optionally consumes one inventory of the
// shared final-root namespace. Permanent session deletion passes that inventory
// through all copies, so recovery checks each operation's names without
// rereading up to 65,536 directory entries for every historical copy.
func (m *Manager) finishFinalRootRemovalWithNamespace(ctx context.Context, r StorageRequest, original storageRemovalClaim, namespace map[domain.ID][]string) (returned error) {
	stage := storageFinalRootPrepared
	defer func() {
		if returned != nil {
			m.Logger.WarnContext(ctx, "workspace_final_root_removal_incomplete", "operation_id", r.OperationID, "action", r.Action, "stage", stage, "code", domain.SafeError(returned).Code)
		}
	}()
	claim, err := m.readFinalRemovalClaim(r, original)
	if err != nil {
		return err
	}
	stage = claim.State
	removal := filepath.Join(m.Root, "workspace-removals", string(r.OperationID))
	private := m.finalRemovalRoot(claim)
	if namespace != nil {
		for _, path := range namespace[claim.Reference.OperationID] {
			if path != private {
				return ResultUncertain()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := security.CheckPrivateDir(filepath.Dir(private)); err != nil {
		return ResultUncertain()
	}
	if claim.State == storageFinalRootUnlinked || claim.State == storageFinalRootRemoved {
		if err := m.finalRemovalClaimAbsent(claim); err != nil {
			return err
		}
		if err := security.SyncParent(private); err != nil {
			return err
		}
		if err := security.SyncParent(removal); err != nil {
			return err
		}
		if err := m.finalRootFault(storageFinalRootSynced); err != nil {
			return err
		}
		if err := m.finalRemovalClaimAbsent(claim); err != nil {
			return err
		}
		return m.writeFinalRemovalClaim(ctx, claim, storageFinalRootRemoved)
	}
	if claim.State == storageFinalRootPrepared {
		if _, err := os.Lstat(private); errors.Is(err, os.ErrNotExist) {
			identity, err := directoryPathIdentity(removal)
			info, statErr := os.Lstat(removal)
			if err != nil || identity != claim.RootIdentity || statErr != nil || info.Mode() != removalWritableDirectoryMode() {
				return ResultUncertain()
			}
			if err := renameStorage(removal, private); err != nil {
				return ResultUncertain()
			}
			if err := m.finalRootFault(storageFinalRootRenamed); err != nil {
				return err
			}
		} else if err != nil {
			return ResultUncertain()
		}
	}
	if err := storageNameAbsent(removal); err != nil {
		return err
	}
	parent, err := os.OpenRoot(filepath.Dir(private))
	if err != nil {
		return ResultUncertain()
	}
	defer parent.Close()
	name := filepath.Base(private)
	before, err := parent.Lstat(name)
	if err != nil || before.Mode() != removalWritableDirectoryMode() {
		return ResultUncertain()
	}
	root, err := openVerifiedChildRoot(parent, name, before)
	if err != nil {
		return ResultUncertain()
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return ResultUncertain()
	}
	identity, identityErr := directoryFileIdentity(file)
	names, readErr := file.Readdirnames(1)
	file.Close()
	if identityErr != nil || identity != claim.RootIdentity || len(names) != 0 || readErr != io.EOF {
		return ResultUncertain()
	}
	// Synchronize both sides even when recovering a rename whose parent flush
	// was interrupted. Publish claimed authority only after anchored validation.
	if err := security.SyncParent(removal); err != nil {
		return err
	}
	if err := security.SyncParent(private); err != nil {
		return err
	}
	if claim.State == storageFinalRootPrepared {
		if err := m.writeFinalRemovalClaim(ctx, claim, storageFinalRootClaimed); err != nil {
			return err
		}
		claim.State = storageFinalRootClaimed
		if err := m.finalRootFault(storageFinalRootClaimed); err != nil {
			return err
		}
	}
	stage = storageFinalRootUnlinkReady
	if err := m.writeFinalRemovalClaim(ctx, claim, stage); err != nil {
		return err
	}
	current, statErr := parent.Lstat(name)
	opened, openedErr := root.Lstat(".")
	if statErr != nil || openedErr != nil || !os.SameFile(before, current) || !os.SameFile(current, opened) || current.Mode() != removalWritableDirectoryMode() || opened.Mode() != removalWritableDirectoryMode() {
		return ResultUncertain()
	}
	if err := m.finalRootFault(storageFinalRootVerified); err != nil {
		return err
	}
	// Closing the child is required on Windows. The unlink remains anchored to
	// its private parent, never to the replaceable old removal name.
	root.Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := storageNameAbsent(removal); err != nil {
		return err
	}
	if err := removeVerifiedFinalRoot(private, claim.RootIdentity, func() error {
		return m.finalRootFault(storageFinalRootBeforeUnlink)
	}, func() error {
		return m.finalRootFault(storageFinalRootAfterVerification)
	}); err != nil {
		return ResultUncertain()
	}
	stage = storageFinalRootNativeGone
	if err := m.finalRootFault(stage); err != nil {
		return err
	}
	if err := m.writeFinalRemovalClaim(ctx, claim, storageFinalRootUnlinked); err != nil {
		return err
	}
	if err := m.finalRootFault(storageFinalRootUnlinked); err != nil {
		return err
	}
	if namespace != nil {
		delete(namespace, claim.Reference.OperationID)
	}
	return m.finishFinalRootRemovalWithNamespace(ctx, r, original, namespace)
}

// Permanent deletion cannot send this namespace through generic copy removal.
// The immutable deletion job must name the same original intent and root proof.
func (m *Manager) cleanupDeletionFinalRoots(ctx context.Context, w domain.SessionDeletionWork) error {
	jobs := map[domain.ID]bool{}
	for _, copy := range w.Copies {
		if copy.Type == domain.WorkspaceStorageJob {
			jobs[copy.JobID] = true
		}
	}
	namespace, err := finalRemovalNamespaceInventory(ctx, m.Root, jobs)
	if err != nil {
		return domain.SessionDeletionPending()
	}
	for _, copy := range w.Copies {
		if copy.Type != domain.WorkspaceStorageJob {
			continue
		}
		_, finalErr := os.Lstat(m.finalRemovalClaimPath(copy.JobID))
		if errors.Is(finalErr, os.ErrNotExist) {
			if len(namespace[copy.JobID]) != 0 {
				return domain.SessionDeletionPending()
			}
			continue
		}
		raw, err := security.ReadPrivate(m.removalIntentPath(copy.JobID), maxSnapshotManifest)
		var intent storageRemovalIntent
		if finalErr != nil || err != nil || domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != copy.JobID || intent.SessionID != w.SessionID || intent.SnapshotID != copy.SnapshotID || (intent.Action != StorageCleanup && intent.Action != StorageDelete) {
			return domain.SessionDeletionPending()
		}
		r := StorageRequest{OperationID: copy.JobID, SnapshotID: copy.SnapshotID, Action: intent.Action, Preparation: PrepareRequest{SessionID: w.SessionID}}
		claim, _, _, err := m.readRemovalClaimState(r, raw)
		if err != nil || m.finishFinalRootRemovalWithNamespace(ctx, r, claim, namespace) != nil {
			return domain.SessionDeletionPending()
		}
		delete(namespace, copy.JobID)
	}
	return nil
}

// Keep the original operation prefix in the fresh private name so deletion and
// completed-proof replay can inventory it even after proof retirement. A missing
// proof never authorizes removing an observed name.
func finalRemovalNamespacePaths(ctx context.Context, root string, jobs map[domain.ID]bool) ([]string, error) {
	namespace, err := finalRemovalNamespaceInventory(ctx, root, jobs)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entries := range namespace {
		paths = append(paths, entries...)
	}
	return paths, nil
}

func finalRemovalNamespaceInventory(ctx context.Context, root string, jobs map[domain.ID]bool) (map[domain.ID][]string, error) {
	parent := filepath.Join(root, "workspace-removal-roots")
	if err := security.CheckPrivateDir(parent); errors.Is(err, os.ErrNotExist) {
		return map[domain.ID][]string{}, nil
	} else if err != nil {
		return nil, ResultUncertain()
	}
	file, err := os.Open(parent)
	if err != nil {
		return nil, ResultUncertain()
	}
	entries, readErr := file.ReadDir(65537)
	closeErr := file.Close()
	if readErr != nil && readErr != io.EOF || closeErr != nil || len(entries) > 65536 {
		return nil, ResultUncertain()
	}
	namespace := map[domain.ID][]string{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if len(name) != 73 || name[36] != '-' || domain.ID(name[:36]).Validate() != nil || domain.ID(name[37:]).Validate() != nil || strings.HasPrefix(name, ".") {
			return nil, ResultUncertain()
		}
		if jobs[domain.ID(name[:36])] {
			operationID := domain.ID(name[:36])
			namespace[operationID] = append(namespace[operationID], filepath.Join(parent, name))
		}
	}
	return namespace, nil
}
