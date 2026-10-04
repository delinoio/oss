// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const CodexSidechatReferenceV1 ForkProfile = "codex-sidechat-reference-v1"

// Recheck the original reference after joined native Fork without running any
// workspace preparation or repairing ownership. Ordinary execution performs
// the same check at its own claim and continuation boundaries.
func (m *Manager) VerifySidechatReference(ctx context.Context, input PrepareRequest, manifest Manifest) error {
	if input.SidechatSource == nil || manifest.Reference == nil || m.initialize() != nil {
		return ResultUncertain()
	}
	lock, err := m.lockWorkspaceObservations(ctx, input.SessionID)
	if err != nil {
		return err
	}
	defer lock.Close()
	_, err = m.verifyWorkspaceIdentity(ctx, input, manifest, continuationIdentity)
	return err
}

// Retain the complete original prepared identity rather than reinterpreting an
// owned Worktree as Local. Nested references are outside this closed profile.
type SidechatSource struct {
	Preparation PrepareRequest `json:"preparation"`
	Manifest    Manifest       `json:"manifest"`
}

type SidechatReference struct {
	SessionID         domain.ID `json:"session_id"`
	PreparationDigest string    `json:"preparation_digest"`
	ManifestDigest    string    `json:"manifest_digest"`
	DirectoryIdentity string    `json:"directory_identity"`
	MetadataIdentity  string    `json:"metadata_identity"`
}

func validateSidechatPreparation(input PrepareRequest) error {
	source := input.SidechatSource
	if input.ForkProfile != CodexSidechatReferenceV1 || source == nil || source.Preparation.SidechatSource != nil || source.Manifest.Reference != nil || source.Preparation.ForkProfile != "" || source.Preparation.ForkSourceID != "" || source.Preparation.ForkSourcePath != "" || source.Preparation.validateStructure() != nil || domain.UniqueIDs([]domain.ID{input.SessionID, source.Preparation.SessionID}) != nil || input.MachineID != source.Preparation.MachineID || input.Type != source.Preparation.Type || input.ForkSourceID != source.Preparation.SessionID || input.ForkSourcePath != "" || input.OriginMachineID != source.Preparation.OriginMachineID || input.PrimaryRepository != source.Preparation.PrimaryRepository {
		return ResultUncertain()
	}
	actual, _ := json.Marshal(input.Repositories)
	expected, _ := json.Marshal(source.Preparation.Repositories)
	if string(actual) != string(expected) {
		return ResultUncertain()
	}
	return nil
}

func referencedRepositories(source Manifest) []PreparedRepository {
	repos := slices.Clone(source.Repositories)
	for i := range repos {
		repos[i].Owned = false
	}
	return repos
}

func validateSidechatResult(input PrepareRequest, result Manifest, workerOS string) error {
	if validateSidechatPreparation(input) != nil || result.Reference == nil {
		return ResultUncertain()
	}
	source := input.SidechatSource
	if ValidateResult(source.Preparation, source.Manifest, workerOS) != nil {
		return ResultUncertain()
	}
	r := result.Reference
	if result.Version != 1 || result.State != Ready || result.SessionID != input.SessionID || result.MachineID != input.MachineID || result.Type != input.Type || result.InputDigest != preparationDigest(input) || result.CreatedAt.IsZero() || result.PrimaryPath != source.Manifest.PrimaryPath || r.SessionID != input.ForkSourceID || r.PreparationDigest != source.Manifest.InputDigest || r.ManifestDigest != manifestDigest(source.Manifest) || !digestValid(r.DirectoryIdentity) || !digestValid(r.MetadataIdentity) {
		return ResultUncertain()
	}
	actual, _ := json.Marshal(result.Repositories)
	expected, _ := json.Marshal(referencedRepositories(source.Manifest))
	if string(actual) != string(expected) {
		return ResultUncertain()
	}
	return nil
}

// Sidechat owns only its private metadata directory. This operation never runs
// preparation Git, copies files, fetches, changes registration or removes a
// source. The parent's observation gate serializes publication with cleanup.
func (m *Manager) PrepareSidechatReference(ctx context.Context, child domain.ID, sourceInput PrepareRequest, source Manifest) (input PrepareRequest, result Manifest, returned error) {
	input = sourceInput
	input.SessionID, input.ForkProfile, input.ForkSourceID = child, CodexSidechatReferenceV1, source.SessionID
	input.SidechatSource = &SidechatSource{Preparation: sourceInput, Manifest: source}
	if validateSidechatPreparation(input) != nil || ValidateResult(sourceInput, source, runtime.GOOS) != nil {
		return input, result, ResultUncertain()
	}
	if err := m.initialize(); err != nil {
		return input, result, err
	}
	parentLock, err := m.lockWorkspaceObservations(ctx, source.SessionID)
	if err != nil {
		return input, result, err
	}
	defer parentLock.Close()
	childLock, err := security.TryLock(filepath.Join(m.Root, "locks", string(child)+".lock"))
	if err != nil {
		return input, result, err
	}
	defer childLock.Close()
	childViews, err := m.lockWorkspaceObservations(ctx, child)
	if err != nil {
		return input, result, err
	}
	defer childViews.Close()
	for _, id := range []domain.ID{source.SessionID, child} {
		if _, err := os.Lstat(filepath.Join(m.Root, "session-deletions", string(id)+".json")); !errors.Is(err, os.ErrNotExist) {
			return input, result, domain.SessionDeletionPending()
		}
	}
	retained, err := m.Read(source.SessionID)
	if err != nil || manifestDigest(retained) != manifestDigest(source) {
		return input, result, ResultUncertain()
	}
	if err := security.PrivateDir(filepath.Join(m.Git.ProcessRoot, string(child))); err != nil {
		return input, result, ResultUncertain()
	}
	inspection := &Manager{Root: m.Root, Git: m.Git, Logger: m.Logger}
	inspection.Git.readOnly = true
	if _, err := inspection.verifyWorkspaceIdentityForOwner(ctx, sourceInput, source, continuationIdentity, child); err != nil {
		return input, result, err
	}
	identity, err := m.sidechatDirectoryIdentity(source)
	if err != nil {
		return input, result, err
	}
	root := filepath.Join(m.Root, "workspaces", string(child))
	if err := os.Mkdir(root, 0700); err != nil {
		return input, result, ResultUncertain()
	}
	metadataIdentity, err := sidechatMetadataIdentity(root)
	if err != nil {
		return input, result, err
	}
	// Exclusive metadata creation is covered by the original once-only fork
	// claim. Failure retains that scope for joined original-operation cleanup.
	result = Manifest{Version: 1, SessionID: child, MachineID: source.MachineID, Type: source.Type, State: Ready, InputDigest: preparationDigest(input), PrimaryPath: source.PrimaryPath, Repositories: referencedRepositories(source), CreatedAt: time.Now().UTC(), Reference: &SidechatReference{SessionID: source.SessionID, PreparationDigest: source.InputDigest, ManifestDigest: manifestDigest(source), DirectoryIdentity: identity, MetadataIdentity: metadataIdentity}}
	defer func() {
		if returned != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.removeSidechatMetadata(cleanupCtx, root, result); err != nil {
				returned = ResultUncertain()
				m.Logger.WarnContext(cleanupCtx, "sidechat_reference_rollback_pending", "session_id", child, "parent_session_id", source.SessionID, "code", domain.SafeError(err).Code)
			}
		}
	}()
	if validateSidechatResult(input, result, runtime.GOOS) != nil {
		return input, result, ResultUncertain()
	}
	if m.sidechatBeforeMetadataPublish != nil {
		m.sidechatBeforeMetadataPublish()
	}
	if err := ctx.Err(); err != nil {
		return input, result, domain.SafeError(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return input, result, ResultUncertain()
	}
	if err := security.WriteAtomic(filepath.Join(root, "manifest.json"), raw); err != nil {
		return input, result, ResultUncertain()
	}
	if err := security.SyncParent(root); err != nil {
		return input, result, ResultUncertain()
	}
	if m.sidechatAfterMetadataPublish != nil {
		m.sidechatAfterMetadataPublish()
	}
	if err := ctx.Err(); err != nil {
		return input, result, domain.SafeError(err)
	}
	if _, err := m.verifySidechatReference(ctx, input, result, child); err != nil {
		return input, result, err
	}
	m.Logger.InfoContext(ctx, "sidechat_workspace_reference_prepared", "session_id", child, "parent_session_id", source.SessionID)
	return input, result, nil
}

func (m *Manager) sidechatDirectoryIdentity(source Manifest) (string, error) {
	paths := []string{filepath.Join(m.Root, "workspaces", string(source.SessionID))}
	paths = append(paths, source.WorkspaceRoots()...)
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		id, err := directoryPathIdentity(path)
		if err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	raw, _ := json.Marshal(ids)
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (m *Manager) verifySidechatReference(ctx context.Context, input PrepareRequest, result Manifest, owner domain.ID) (string, error) {
	if validateSidechatResult(input, result, runtime.GOOS) != nil {
		return "", ResultUncertain()
	}
	metadataIdentity, err := sidechatMetadataIdentity(filepath.Join(m.Root, "workspaces", string(input.SessionID)))
	if err != nil || metadataIdentity != result.Reference.MetadataIdentity {
		return "", ResultUncertain()
	}
	source := input.SidechatSource
	retained, err := m.Read(source.Preparation.SessionID)
	if err != nil || retained.State != Ready || manifestDigest(retained) != result.Reference.ManifestDigest {
		return "", domain.Fail(domain.Unavailable, "The parent workspace is unavailable.", "Wait for its original cleanup or restore; Sidechat cannot adopt another workspace.")
	}
	identity, err := m.sidechatDirectoryIdentity(retained)
	if err != nil || identity != result.Reference.DirectoryIdentity {
		return "", ResultUncertain()
	}
	inspection := &Manager{Root: m.Root, Git: m.Git, Logger: m.Logger}
	inspection.Git.readOnly = true
	original, err := inspection.verifyWorkspaceIdentityForOwner(ctx, source.Preparation, retained, continuationIdentity, owner)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(manifestDigest(result) + ":" + original + ":" + identity))
	return hex.EncodeToString(hash[:]), nil
}

// The Worker independently refuses parent file removal while any child metadata
// still retains a reference. Server cascade dispatch cannot bypass this gate.
// Malformed/unreadable directories stay protected; never skip unknown ownership.
func (m *Manager) requireNoSidechatReferences(ctx context.Context, parent domain.ID) error {
	root, err := os.Open(filepath.Join(m.Root, "workspaces"))
	if err != nil {
		return ResultUncertain()
	}
	defer root.Close()
	names, err := root.Readdirnames(4097)
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, io.EOF) {
		return ResultUncertain()
	}
	if len(names) > 4096 {
		return ResultUncertain()
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		id := domain.ID(name)
		if id.Validate() != nil {
			return ResultUncertain()
		}
		if id == parent {
			continue
		}
		manifest, err := m.Read(id)
		if err != nil {
			return ResultUncertain()
		}
		if manifest.Reference != nil && (manifest.Reference.SessionID.Validate() != nil || manifest.Reference.SessionID == parent) {
			return domain.Fail(domain.Conflict, "Dependent Sidechat cleanup is pending.", "Stop and delete every dependent Sidechat through its original Worker before removing the parent workspace.")
		}
	}
	return nil
}

func sidechatMetadataIdentity(root string) (string, error) {
	identity, err := directoryPathIdentity(root)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(hash[:]), nil
}

// Remove only the single original metadata file from the inode-bound child
// scope. No source root or referenced repository becomes removal authority.
func (m *Manager) removeSidechatMetadata(ctx context.Context, root string, manifest Manifest) error {
	ref := manifest.Reference
	if root != filepath.Join(m.Root, "workspaces", string(manifest.SessionID)) || manifest.SessionID.Validate() != nil || ref == nil || ref.SessionID.Validate() != nil || ref.SessionID == manifest.SessionID || !digestValid(ref.MetadataIdentity) || slices.ContainsFunc(manifest.Repositories, func(r PreparedRepository) bool { return r.Owned }) {
		return ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	identity, err := sidechatMetadataIdentity(root)
	if err != nil || identity != ref.MetadataIdentity {
		return ResultUncertain()
	}
	anchored, err := os.OpenRoot(root)
	if err != nil {
		return ResultUncertain()
	}
	defer anchored.Close()
	file, err := anchored.Open(".")
	if err != nil {
		return ResultUncertain()
	}
	names, readErr := file.Readdirnames(2)
	file.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) || len(names) > 1 || len(names) == 1 && names[0] != "manifest.json" {
		return ResultUncertain()
	}
	if len(names) == 1 {
		raw, err := security.ReadPrivate(filepath.Join(root, "manifest.json"), 1<<20)
		if err != nil {
			return ResultUncertain()
		}
		var stored Manifest
		if domain.Decode(raw, &stored) != nil || manifestDigest(stored) != manifestDigest(manifest) {
			return ResultUncertain()
		}
		if err := anchored.Remove("manifest.json"); err != nil {
			return ResultUncertain()
		}
	}
	identity, err = sidechatMetadataIdentity(root)
	if err != nil || identity != ref.MetadataIdentity {
		return ResultUncertain()
	}
	// Go's Windows OpenRoot(path) denies delete sharing. Close the original
	// anchored view after the final identity check before removing its name;
	// no file operation may use the closed view or adopt another directory.
	if err := anchored.Close(); err != nil {
		return ResultUncertain()
	}
	if err := os.Remove(root); err != nil {
		return ResultUncertain()
	}
	return security.SyncParent(root)
}

// Failed original Fork work owns only this unpublished inode-bound metadata.
// Called after native owners join; never follow or remove the referenced parent.
func (m *Manager) DiscardUnpublishedSidechatReference(ctx context.Context, input PrepareRequest, manifest Manifest) error {
	if validateSidechatResult(input, manifest, runtime.GOOS) != nil || m.initialize() != nil {
		return ResultUncertain()
	}
	parent, err := m.lockWorkspaceObservations(ctx, input.ForkSourceID)
	if err != nil {
		return err
	}
	defer parent.Close()
	child, err := security.TryLock(filepath.Join(m.Root, "locks", string(input.SessionID)+".lock"))
	if err != nil {
		return err
	}
	defer child.Close()
	views, err := m.lockWorkspaceObservations(ctx, input.SessionID)
	if err != nil {
		return err
	}
	defer views.Close()
	return m.removeSidechatMetadata(ctx, filepath.Join(m.Root, "workspaces", string(input.SessionID)), manifest)
}
