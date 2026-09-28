//go:build !darwin && !linux && !windows

package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func volumeSpace(string) (uint64, uint64, error) {
	return 0, 0, domain.Fail(domain.Unsupported, "Filesystem capacity is unsupported on this platform.", "Use a supported DeliDev server platform.")
}
