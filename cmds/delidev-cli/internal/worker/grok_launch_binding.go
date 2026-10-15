// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Launch binding. A digest check of the installed executable path proves nothing
// about the bytes the operating system later executes: another updater can
// replace the path atomically (or write the same inode) between the check and
// the exec, and the replacement would receive the managed credential. The Worker
// therefore never launches the installed path. It copies the bytes it hashes
// into a private snapshot in the execution's own journal directory, in one read
// of one open descriptor, accepts the snapshot only when that digest equals the
// digest the Take admitted, and launches the snapshot by path for every native
// run of the execution (inspection and credentialed launch alike). The
// installed path stays the startup evidence; the snapshot is only what runs.
//
// Platform note. A descriptor-based exec (execve of /proc/self/fd/N on Linux,
// fexecve where it exists) would remove even the snapshot, but the native
// launch crosses the owned-process supervisor boundary on Unix and macOS has no
// fexecve, so one snapshot mechanism is used on every platform. The residual is
// a writer that already runs as the Worker's own user and replaces the snapshot
// inside the Worker's private 0700 execution directory; such a writer can read
// the credential file directly, so it is outside the boundary this binding
// protects. The snapshot is re-verified (identity, then digest) immediately
// before the credential is written and removed when the execution ends.

// grokLaunchLimit bounds the snapshot to the size the digest inspection accepts.
const grokLaunchLimit = 512 << 20

// grokLaunchName is the directory of the snapshot inside the execution journal.
const grokLaunchName = "launch"

// bindLaunch snapshots the executable the Take authorized and returns the path
// to launch. dir is the execution's private journal directory.
func (r *grokSubscriptionRun) bindLaunch(ctx context.Context, dir string) (string, error) {
	changed := func() error {
		return domain.Fail(domain.RecoveryRequired, "The authorized native executable changed since it was admitted.", "Refresh Worker discovery; the credential is not delivered to different bytes.")
	}
	executable, _, err := r.executable()
	if err != nil {
		return "", err
	}
	src, err := os.Open(executable)
	if err != nil {
		return "", changed()
	}
	defer src.Close()
	original, err := src.Stat()
	if err != nil || !original.Mode().IsRegular() || original.Size() <= 0 || original.Size() > grokLaunchLimit {
		return "", changed()
	}
	if err := security.CreatePrivateDirExclusive(filepath.Join(dir, grokLaunchName)); err != nil {
		return "", domain.SafeError(err)
	}
	path := filepath.Join(dir, grokLaunchName, filepath.Base(executable))
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", domain.SafeError(err)
	}
	discard := func(cause error) (string, error) {
		_ = dst.Close()
		_ = os.Remove(path)
		return "", cause
	}
	hash := sha256.New()
	copied, err := copyContext(ctx, io.MultiWriter(dst, hash), io.LimitReader(src, original.Size()+1))
	if err != nil {
		return discard(domain.SafeError(err))
	}
	// The path must still name the file that was read, and that file must not have
	// changed while it was read, or the bytes are not a consistent snapshot.
	current, lstatErr := os.Lstat(executable)
	final, statErr := src.Stat()
	if copied != original.Size() || lstatErr != nil || statErr != nil || !os.SameFile(original, current) || !os.SameFile(original, final) || final.Size() != original.Size() || !final.ModTime().Equal(original.ModTime()) {
		return discard(changed())
	}
	if hex.EncodeToString(hash.Sum(nil)) != r.installation.ExecutableSHA256 {
		return discard(changed())
	}
	if err := dst.Sync(); err != nil {
		return discard(domain.SafeError(err))
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(path)
		return "", domain.SafeError(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != original.Size() {
		_ = os.Remove(path)
		return "", changed()
	}
	r.launchPath, r.launchInfo = path, info
	if r.logger != nil {
		r.logger.InfoContext(ctx, "grok_subscription_execution_launch_bound", "phase", "EXECUTION", "step", "launch", "size_bytes", info.Size())
	}
	return path, nil
}

// verifyLaunch is the last gate before the credential can exist: the snapshot
// must still be the file that was verified (identity first, then its bytes). A
// run with no snapshot verifies the installed path instead.
func (r *grokSubscriptionRun) verifyLaunch(ctx context.Context) error {
	if r.launchPath == "" {
		_, _, err := r.verifiedExecutable(ctx)
		return err
	}
	changed := domain.Fail(domain.RecoveryRequired, "The authorized native executable changed since it was admitted.", "Refresh Worker discovery; the credential is not delivered to different bytes.")
	current, err := os.Lstat(r.launchPath)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(r.launchInfo, current) || current.Size() != r.launchInfo.Size() || !current.ModTime().Equal(r.launchInfo.ModTime()) {
		return changed
	}
	digest, err := harness.InspectExecutable(ctx, r.launchPath)
	if err != nil || digest != r.installation.ExecutableSHA256 {
		return changed
	}
	return nil
}

// removeLaunch deletes the snapshot once the execution is over. A snapshot that
// cannot be removed is left in the execution's private directory; it carries no
// credential.
func (r *grokSubscriptionRun) removeLaunch() {
	if r.launchPath == "" {
		return
	}
	if err := os.Remove(r.launchPath); err != nil && !errors.Is(err, os.ErrNotExist) && r.logger != nil {
		r.logger.Warn("grok_subscription_execution_launch_remove_failed", "phase", "EXECUTION", "step", "launch")
	}
	_ = os.Remove(filepath.Dir(r.launchPath))
	r.launchPath, r.launchInfo = "", nil
}

// copyContext copies until EOF while honoring cancellation between buffers.
func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buffer := make([]byte, 64<<10)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			written, writeErr := dst.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return total, nil
		}
		if readErr != nil {
			return total, readErr
		}
	}
}
