//go:build darwin || linux

package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/unix"
	"math"
)

func volumeSpace(path string) (uint64, uint64, error) {
	var value unix.Statfs_t
	if err := unix.Statfs(path, &value); err != nil {
		return 0, 0, err
	}
	if value.Bsize <= 0 || value.Blocks > math.MaxUint64/uint64(value.Bsize) || value.Bavail > math.MaxUint64/uint64(value.Bsize) {
		return 0, 0, domain.Fail(domain.Unavailable, "Filesystem capacity is unavailable.", "Inspect capacity on the server computer.")
	}
	return value.Blocks * uint64(value.Bsize), value.Bavail * uint64(value.Bsize), nil
}
