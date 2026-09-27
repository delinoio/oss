package opencode

import (
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Pinned core/v2 configuration ignores the legacy project-disable flag and
// discovers sources before native reference/skill/plugin initialization. Keep
// every native ancestor source absent; never read or rewrite unselected config.
// This guard can be relaxed only with verified native loader isolation.
type projectConfigScope struct {
	Directory string
	Root      string
}

func (s *projectConfigScope) inspect() error {
	if s == nil {
		return nil // Package-private fixtures may omit a production filesystem scope.
	}
	if !canonicalDirectory(s.Directory) || !canonicalDirectory(s.Root) || !directoryContains(s.Root, s.Directory) {
		return projectConfigUnsupported()
	}
	for directory, depth := s.Directory, 0; depth < 1024; directory, depth = filepath.Dir(directory), depth+1 {
		if !referenceSourcesEmpty(directory) {
			return projectConfigUnsupported()
		}
		if directory == s.Root {
			return nil
		}
		if filepath.Dir(directory) == directory {
			return projectConfigUnsupported()
		}
	}
	return projectConfigUnsupported()
}

func referenceSourcesEmpty(directory string) bool {
	for _, name := range []string{"opencode.json", "opencode.jsonc", ".opencode"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			return false
		}
	}
	return true
}

func projectConfigUnsupported() error {
	return domain.Fail(domain.Unsupported, "This OpenCode version cannot isolate the workspace's native configuration sources.", "The workspace configuration is preserved. Use a separately supported native configuration profile before executing it.")
}
