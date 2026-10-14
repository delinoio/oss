// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"strings"
)

// managedTokenEnvironment builds the supervised Claude child's environment for
// one server-owned setup token. It removes every inherited API, bearer, endpoint
// and login source, points all native state at the fresh private runtime and
// adds the token only here. CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST makes the
// pinned native version strip the token from tool and hook subprocesses.
// The --bare flag is never used: it ignores this token source.
func managedTokenEnvironment(env []string, home string, token []byte) []string {
	filtered := make([]string, 0, len(env)+4)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "CLAUDE_SECURESTORAGE_CONFIG_DIR", "CLAUDE_CODE_HOST_AUTH_ENV_VAR":
			continue
		}
		filtered = append(filtered, entry)
	}
	return append(filtered, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+home, "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST=1", "CLAUDE_CODE_OAUTH_TOKEN="+string(token))
}
