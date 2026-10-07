// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Grok 1.0.46 has no validated switch that disables its unified native file
// logger. Authentication diagnostics can contain token fragments or provider
// response text. Reserve its logs directory as an empty private regular file
// before the first inspection/credential boundary, so native logging cannot
// publish those bytes. This workaround applies only to a fresh owned home and
// may be removed after a pinned native version proves equivalent log isolation.
func createNativeLogGuard(home string) error {
	if security.CheckPrivateDir(home) != nil {
		return incompatible()
	}
	path := filepath.Join(home, "logs")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return incompatible()
	}
	syncErr, closeErr := f.Sync(), f.Close()
	if syncErr != nil || closeErr != nil || security.SyncParent(path) != nil {
		return incompatible()
	}
	return checkNativeLogGuard(path)
}

func checkNativeLogGuard(path string) error {
	raw, err := security.ReadPrivate(path, 1)
	if err != nil || len(raw) != 0 {
		return incompatible()
	}
	return nil
}
