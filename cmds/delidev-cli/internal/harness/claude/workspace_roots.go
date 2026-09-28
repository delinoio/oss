package claude

import (
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Omitted roots preserve the original single-directory launch/checkpoint.
// Multiple roots come from the accepted ordered manifest and include the
// primary cwd exactly once. They grant native file scope, never settings or
// external account inheritance; --setting-sources remains explicitly empty.
func validateWorkspaceRoots(config APIStreamConfig) error {
	if len(config.WorkspaceRoots) == 0 {
		return nil
	}
	if len(config.WorkspaceRoots) < 2 || len(config.WorkspaceRoots) > 100 {
		return apiConfigurationError()
	}
	seen := make(map[string]bool, len(config.WorkspaceRoots))
	for _, root := range config.WorkspaceRoots {
		if domain.Text(root, "native workspace root", 4096, true) != nil || !filepath.IsAbs(root) || filepath.Clean(root) != root || seen[root] {
			return apiConfigurationError()
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil || canonical != root {
			return apiConfigurationError()
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return apiConfigurationError()
		}
		seen[root] = true
	}
	if !seen[config.Workspace] {
		return apiConfigurationError()
	}
	return nil
}
