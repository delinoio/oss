// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"path"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func normalizeWorkerPath(value, workerOS string) string {
	if workerOS == "windows" {
		return strings.ReplaceAll(value, "\\", "/")
	}
	return value
}

// Validate lexical paths using the original Worker's OS, never the server's.
// Strip a UNC slash only for canonicality checks; the full authority remains
// part of the original root identity used for containment and publication.
func validWorkerAbsolutePath(value, workerOS string) bool {
	if domain.Text(value, "workspace path", 4096, true) != nil {
		return false
	}
	value = normalizeWorkerPath(value, workerOS)
	if workerOS == "windows" {
		if strings.HasPrefix(value, "//") {
			parts := strings.Split(value[2:], "/")
			if len(parts) < 2 || parts[0] == "" || parts[0] == "." || parts[0] == "?" || parts[1] == "" {
				return false
			}
			value = value[1:]
		} else {
			if len(value) < 4 || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z')) || value[1:3] != ":/" {
				return false
			}
			value = value[2:]
		}
		// A colon after the drive, including an alternate stream, is not an
		// ordinary Windows file path. POSIX filenames retain these characters.
		if strings.ContainsAny(value, `<>:"|?*`) {
			return false
		}
	} else if workerOS != "darwin" && workerOS != "linux" {
		return false
	}
	return path.IsAbs(value) && path.Clean(value) == value
}
