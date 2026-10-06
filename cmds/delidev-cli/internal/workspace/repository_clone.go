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
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const RepositoryCloneTimeout = 10 * time.Minute

type CloneRequest struct {
	JobID         domain.ID `json:"job_id"`
	ParentPath    string    `json:"parent_path"`
	URL           string    `json:"url"`
	DirectoryName string    `json:"directory_name"`
}
type clonePhase string

const (
	cloneCreated   clonePhase = "created"
	cloneValidated clonePhase = "validated"
	clonePublished clonePhase = "published"
	cloneFailed    clonePhase = "failed"
	cloneRecovery  clonePhase = "recovery"
)

type cloneClaim struct {
	Version          int         `json:"version"`
	JobID            domain.ID   `json:"job_id"`
	Digest           string      `json:"digest"`
	Parent           string      `json:"parent"`
	ParentIdentity   string      `json:"parent_identity"`
	Staging          string      `json:"staging"`
	StagingIdentity  string      `json:"staging_identity"`
	CheckoutIdentity string      `json:"checkout_identity,omitempty"`
	Final            string      `json:"final"`
	Phase            clonePhase  `json:"phase"`
	Output           *Inspection `json:"output,omitempty"`
}

func cloneRecoveryRequired() error {
	return domain.Fail(domain.RecoveryRequired, "Repository clone ownership or completion is unconfirmed.", "Inspect the original clone job and retained files. Do not repeat the clone or remove its staging by inference.")
}
func cloneConflict() error {
	return domain.Fail(domain.Conflict, "The repository destination already exists.", "Choose another folder name or add that existing checkout. Even an empty destination is not replaced.")
}
func writeCloneClaim(path string, claim cloneClaim) error {
	raw, err := json.Marshal(claim)
	if err != nil {
		return err
	}
	return security.WriteAtomic(path, raw)
}

// Clone has no server credentials. The caller's ordinary once-only job journal
// must be durable before entry; this additional claim binds native file ownership.
// Final publication transfers the checkout to the user's Local ownership, so no
// later failure or configuration deletion may remove it.
func (g Git) Clone(ctx context.Context, privateRoot string, request CloneRequest) (result Inspection, returned error) {
	bounded, cancel := context.WithTimeout(ctx, RepositoryCloneTimeout)
	defer cancel()
	ctx = bounded
	defer func() {
		if returned != nil && g.Logger != nil {
			g.Logger.WarnContext(ctx, "repository_clone_failed", "job_id", request.JobID, "code", domain.SafeError(returned).Code)
		}
	}()
	if request.JobID.Validate() != nil || request.JobID != g.OwnerID {
		return result, cloneRecoveryRequired()
	}
	if _, err := domain.ParseRepositoryCloneURL(request.URL); err != nil {
		return result, err
	}
	if err := domain.ValidateRepositoryCloneDirectory(request.DirectoryName); err != nil {
		return result, err
	}
	if domain.Text(request.ParentPath, "clone parent path", 4096, true) != nil || !filepath.IsAbs(request.ParentPath) {
		return result, domain.Fail(domain.InvalidArgument, "Choose an absolute parent folder on this computer.", "The parent folder must already exist.")
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	parent, err := filepath.EvalSymlinks(request.ParentPath)
	if err != nil {
		return result, domain.Fail(domain.InvalidArgument, "The parent folder is unavailable.", "Choose an existing accessible parent folder.")
	}
	parentIdentity, err := directoryPathIdentity(parent)
	if err != nil {
		return result, err
	}
	final := filepath.Join(parent, request.DirectoryName)
	if domain.Text(final, "final checkout path", 4096, true) != nil {
		return result, domain.Fail(domain.InvalidArgument, "The final checkout path is too long.", "Choose a shorter parent path or folder name.")
	}
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return result, cloneConflict()
		}
		return result, cloneRecoveryRequired()
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	claims := filepath.Join(privateRoot, "repository-clones")
	if err := security.PrivateDir(claims); err != nil {
		return result, domain.SafeError(err)
	}
	lock, err := security.TryLock(filepath.Join(claims, string(request.JobID)+".lock"))
	if err != nil {
		return result, domain.SafeError(err)
	}
	defer lock.Close()
	claimPath := filepath.Join(claims, string(request.JobID)+".json")
	if _, err := os.Lstat(claimPath); !errors.Is(err, os.ErrNotExist) {
		return result, cloneRecoveryRequired()
	}
	staging := filepath.Join(parent, ".delidev-clone-"+string(request.JobID))
	if err := security.CreatePrivateDirExclusive(staging); err != nil {
		return result, cloneRecoveryRequired()
	}
	stagingIdentity, err := directoryPathIdentity(staging)
	if err != nil {
		return result, cloneRecoveryRequired()
	}
	raw, _ := json.Marshal(request)
	digest := sha256.Sum256(raw)
	claim := cloneClaim{Version: 1, JobID: request.JobID, Digest: hex.EncodeToString(digest[:]), Parent: parent, ParentIdentity: parentIdentity, Staging: staging, StagingIdentity: stagingIdentity, Final: final, Phase: cloneCreated}
	if security.CheckPrivateDir(staging) != nil || security.SyncParent(staging) != nil || writeCloneClaim(claimPath, claim) != nil {
		return result, cloneRecoveryRequired()
	}
	published := false
	defer func() {
		if returned == nil {
			return
		}
		if published {
			claim.Phase = cloneRecovery
			_ = writeCloneClaim(claimPath, claim)
			return
		}
		if domain.SafeError(returned).Code == domain.RecoveryRequired {
			claim.Phase = cloneRecovery
			_ = writeCloneClaim(claimPath, claim)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if removeCloneStaging(cleanup, claimPath, claim) != nil {
			returned = cloneRecoveryRequired()
			claim.Phase = cloneRecovery
		} else {
			claim.Phase = cloneFailed
		}
		if writeCloneClaim(claimPath, claim) != nil {
			returned = cloneRecoveryRequired()
		}
	}()
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	if g.Logger != nil {
		g.Logger.InfoContext(ctx, "repository_clone", "job_id", request.JobID, "phase", cloneCreated)
	}
	// Empty hooks/templates and explicit transport policy prevent repository hook
	// or external helper execution. Submodules and optional LFS smudging stay off.
	g.cloneDiagnostics = true
	g.Timeout = RepositoryCloneTimeout
	g.environment = append(gitEnvironment(), "GIT_LFS_SKIP_SMUDGE=1")
	// A null-device hook path cannot acquire executable hooks even if another
	// same-user process changes the staging template while Git is running.
	g.HooksDir = os.DevNull
	template := filepath.Join(staging, "template")
	if err := os.Mkdir(template, 0700); err != nil {
		return result, domain.SafeError(err)
	}
	checkout := filepath.Join(staging, "checkout")
	// Git accepts an existing empty staging checkout. Capture its inode before
	// Git creates content so completion cannot adopt a replacement checkout.
	if err := os.Mkdir(checkout, 0700); err != nil {
		return result, domain.SafeError(err)
	}
	claim.CheckoutIdentity, err = directoryPathIdentity(checkout)
	if err != nil {
		return result, cloneRecoveryRequired()
	}
	if err := writeCloneClaim(claimPath, claim); err != nil {
		return result, cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(parent); err != nil || identity != parentIdentity {
		return result, cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(staging); err != nil || identity != stagingIdentity {
		return result, cloneRecoveryRequired()
	}
	args := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always", "-c", "http.followRedirects=false", "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-c", "fetch.recurseSubmodules=false", "clone", "--origin=origin", "--no-recurse-submodules", "--template=" + template, "--", request.URL, checkout}
	if _, err := g.run(bounded, staging, args...); err != nil {
		return result, err
	}
	if identity, err := directoryPathIdentity(parent); err != nil || identity != parentIdentity {
		return result, cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(staging); err != nil || identity != stagingIdentity {
		return result, cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(checkout); err != nil || identity != claim.CheckoutIdentity {
		return result, cloneRecoveryRequired()
	}
	g.readOnly = true
	result, err = g.Inspect(bounded, checkout)
	if err != nil {
		return Inspection{}, err
	}
	if result.Root != checkout {
		return Inspection{}, cloneRecoveryRequired()
	}
	if err := g.EnrichInspection(bounded, &result); err != nil {
		return Inspection{}, err
	}
	checkoutIdentity, err := directoryPathIdentity(checkout)
	if err != nil || checkoutIdentity != claim.CheckoutIdentity {
		return Inspection{}, cloneRecoveryRequired()
	}
	claim.Phase = cloneValidated
	if err := writeCloneClaim(claimPath, claim); err != nil {
		return Inspection{}, cloneRecoveryRequired()
	}
	if bounded.Err() != nil {
		return Inspection{}, domain.SafeError(bounded.Err())
	}
	if identity, err := directoryPathIdentity(parent); err != nil || identity != parentIdentity {
		return Inspection{}, cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(staging); err != nil || identity != stagingIdentity {
		return Inspection{}, cloneRecoveryRequired()
	}
	if err := storageRenameNoReplace(checkout, final); err != nil {
		if _, exists := os.Lstat(final); exists == nil {
			return Inspection{}, cloneConflict()
		}
		return Inspection{}, cloneRecoveryRequired()
	}
	published = true
	result.Root, result.Name = final, request.DirectoryName
	if identity, err := directoryPathIdentity(final); err != nil || identity != checkoutIdentity {
		return result, cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(parent); err != nil || identity != parentIdentity || security.SyncParent(final) != nil {
		return result, cloneRecoveryRequired()
	}
	claim.Phase, claim.Output = clonePublished, &result
	if err := writeCloneClaim(claimPath, claim); err != nil {
		return result, cloneRecoveryRequired()
	}
	// Only the now-empty wrapper and its empty template directory remain owned.
	if err := removeCloneStaging(bounded, claimPath, claim); err != nil {
		return result, cloneRecoveryRequired()
	}
	if g.Logger != nil {
		g.Logger.InfoContext(ctx, "repository_clone", "job_id", request.JobID, "phase", clonePublished)
	}
	return result, nil
}

func removeCloneStaging(ctx context.Context, claimPath string, expected cloneClaim) error {
	raw, err := security.ReadPrivate(claimPath, 2<<20)
	var current cloneClaim
	if err != nil || domain.DecodeWithLimit(raw, &current, 2<<20) != nil {
		return cloneRecoveryRequired()
	}
	a, _ := json.Marshal(current)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) {
		return cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(expected.Parent); err != nil || identity != expected.ParentIdentity {
		return cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(expected.Staging); err != nil || identity != expected.StagingIdentity {
		return cloneRecoveryRequired()
	}
	quarantine := filepath.Join(expected.Parent, ".delidev-clone-removing-"+string(expected.JobID))
	if err := storageRenameNoReplace(expected.Staging, quarantine); err != nil {
		return cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(quarantine); err != nil || identity != expected.StagingIdentity {
		return cloneRecoveryRequired()
	}
	root, err := os.OpenRoot(quarantine)
	if err != nil {
		return cloneRecoveryRequired()
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return cloneRecoveryRequired()
	}
	identity, err := directoryFileIdentity(file)
	file.Close()
	if err != nil || identity != expected.StagingIdentity {
		return cloneRecoveryRequired()
	}
	remaining := 100000
	if err := removeCloneEntries(ctx, root, &remaining); err != nil {
		return cloneRecoveryRequired()
	}
	if identity, err := directoryPathIdentity(quarantine); err != nil || identity != expected.StagingIdentity {
		return cloneRecoveryRequired()
	}
	if err := os.Remove(quarantine); err != nil {
		return cloneRecoveryRequired()
	}
	return security.SyncParent(quarantine)
}

// Enumerate and unlink only through opened directory handles. Links are removed
// as links. Replacement entries or directories remain protected for recovery.
func removeCloneEntries(ctx context.Context, root *os.Root, remaining *int) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := file.ReadDir(*remaining + 1)
	file.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries) > *remaining {
		return cloneRecoveryRequired()
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		*remaining--
		before, err := root.Lstat(entry.Name())
		if err != nil {
			return err
		}
		if before.IsDir() && before.Mode()&os.ModeSymlink == 0 {
			child, err := openVerifiedChildRoot(root, entry.Name(), before)
			if err != nil {
				return err
			}
			err = removeCloneEntries(ctx, child, remaining)
			child.Close()
			if err != nil {
				return err
			}
		}
		after, err := root.Lstat(entry.Name())
		if err != nil || !os.SameFile(before, after) {
			return cloneRecoveryRequired()
		}
		if err := root.Remove(entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

// Keep only a bounded private prefix for closed failure classification. Discard
// the remainder without failing the process stream; nothing is persisted/logged.
type cloneDiagnosticWriter struct{ output *limitedOutput }

func (w cloneDiagnosticWriter) Write(p []byte) (int, error) {
	n := len(p)
	remaining := w.output.limit - w.output.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = w.output.Buffer.Write(p)
	}
	return n, nil
}
