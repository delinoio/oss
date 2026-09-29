//go:build !darwin && !linux && !windows

package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func claimBackupImage(_, _ string) error {
	return domain.Fail(domain.Unsupported, "Atomic backup claims are unavailable on this platform.", "Use a supported desktop or server platform.")
}
