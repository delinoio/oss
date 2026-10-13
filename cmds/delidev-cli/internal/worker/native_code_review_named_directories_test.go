// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"log/slog"
	"testing"
)

func TestNativeReviewWorkspaceManagerCarriesNegotiatedNamedDirectories(t *testing.T) {
	logger := slog.Default()
	for name, negotiated := range map[string]bool{"negotiated": true, "not negotiated": false} {
		t.Run(name, func(t *testing.T) {
			config := Config{Root: t.TempDir(), namedWorkspaceDirectories: negotiated}
			manager := nativeReviewWorkspaceManager(config, logger)
			if manager.NamedDirectories != negotiated || manager.Root != config.Root || manager.Logger != logger {
				t.Fatal("native review manager dropped the Worker configuration")
			}
		})
	}
}
