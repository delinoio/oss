// SPDX-License-Identifier: Apache-2.0

package clone

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const sourceOpenFlags = os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK

func nativeClone(ctx context.Context, src *os.File, dst string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var sourceFS, destinationFS unix.Statfs_t
	if err := unix.Fstatfs(int(src.Fd()), &sourceFS); err != nil {
		return err
	}
	if err := unix.Statfs(filepath.Dir(dst), &destinationFS); err != nil {
		return err
	}
	if unix.ByteSliceToString(sourceFS.Fstypename[:]) != "apfs" || unix.ByteSliceToString(destinationFS.Fstypename[:]) != "apfs" {
		return ErrUnsupported
	}
	// fclonefileat clones from the verified open inode, not a replaceable source
	// pathname. CLONE_NOOWNERCOPY keeps the newly created cache user-owned.
	err := unix.Fclonefileat(int(src.Fd()), unix.AT_FDCWD, dst, unix.CLONE_NOOWNERCOPY)
	return capabilityError(err)
}

func capabilityError(err error) error {
	if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) {
		return errors.Join(ErrUnsupported, err)
	}
	return err
}
