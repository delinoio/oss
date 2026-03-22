package state

import (
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
)

const (
	rootDirName = "derun"
)

func ResolveStateRoot() (string, error) {
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, rootDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", errmsg.Error(errmsg.Runtime("resolve home directory", err, nil), nil)
	}
	return filepath.Join(home, ".local", "state", rootDirName), nil
}

func EnsureDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return errmsg.Error(errmsg.Runtime("mkdir", err, map[string]any{
			"path": path,
		}), nil)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return errmsg.Error(errmsg.Runtime("chmod", err, map[string]any{
			"path": path,
		}), nil)
	}
	return nil
}
