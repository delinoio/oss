// SPDX-License-Identifier: Apache-2.0

package clone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const sourceOpenFlags = os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK

func nativeClone(ctx context.Context, src *os.File, dst string) (err error) {
	if err = ctx.Err(); err != nil {
		return err
	}
	var sourceFS, destinationFS unix.Statfs_t
	if err = unix.Fstatfs(int(src.Fd()), &sourceFS); err != nil {
		return err
	}
	if err = unix.Statfs(filepath.Dir(dst), &destinationFS); err != nil {
		return err
	}
	supported := func(kind int64) bool { return kind == unix.BTRFS_SUPER_MAGIC || kind == unix.XFS_SUPER_MAGIC }
	if !supported(sourceFS.Type) || !supported(destinationFS.Type) {
		return ErrUnsupported
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			err = fmt.Errorf("close native clone (prior error: %v): %w", err, closeErr)
		}
	}()
	return capabilityError(unix.IoctlFileClone(int(out.Fd()), int(src.Fd())))
}

func capabilityError(err error) error {
	// Do not classify EINVAL/EBADF as capability errors: these also indicate
	// invalid arguments or descriptors and must not conceal implementation bugs.
	if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOTTY) || errors.Is(err, unix.ENOSYS) {
		return errors.Join(ErrUnsupported, err)
	}
	return err
}
