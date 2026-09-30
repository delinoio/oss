// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"os"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func forkNonblockFlag() int { return 0 }

// Windows Go permission bits do not describe access control. Verify the same
// owner-only DACL used for private Worker state instead of a Unix write mask.
func forkPrivateRolloutNode(path string, info os.FileInfo) bool {
	if info.IsDir() {
		return security.CheckPrivateDir(path) == nil
	}
	return security.RegularPrivate(path) == nil
}
