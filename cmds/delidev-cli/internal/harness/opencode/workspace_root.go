package opencode

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type workspaceRootKind uint8

const (
	gitWorkspaceRoot workspaceRootKind = iota + 1
	globalWorkspaceRoot
)

// WorkspaceRoot is private comparison authority constructed from the owning
// Worker's accepted workspace, never from native responses. Native global "/"
// is metadata only; filesystem inspection always uses the independent boundary.
// Its unexported fields prevent callers from inventing aliases or root kinds.
type WorkspaceRoot struct {
	kind      workspaceRootKind
	directory string
	native    string
	boundary  string
}

// GitWorkspaceRoot requires the caller's independently verified Git lease.
func GitWorkspaceRoot(directory, root string) (WorkspaceRoot, error) {
	value := WorkspaceRoot{gitWorkspaceRoot, directory, root, root}
	return checkedWorkspaceRoot(value)
}

// GlobalWorkspaceRoot independently excludes every enclosing Git entry,
// including files, links and uninspectable entries, before selecting "/".
func GlobalWorkspaceRoot(directory string) (WorkspaceRoot, error) {
	boundary := filesystemBoundary(directory)
	value := WorkspaceRoot{globalWorkspaceRoot, directory, "/", boundary}
	return checkedWorkspaceRoot(value)
}

func filesystemBoundary(directory string) string {
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(directory)
		// This profile adds only canonical drive-qualified local directories.
		// UNC/device paths and root-relative aliases need separate evidence.
		if len(volume) != 2 || volume[1] != ':' || !(volume[0] >= 'A' && volume[0] <= 'Z' || volume[0] >= 'a' && volume[0] <= 'z') {
			return ""
		}
		return volume + string(filepath.Separator)
	}
	return string(filepath.Separator)
}

func checkedWorkspaceRoot(value WorkspaceRoot) (WorkspaceRoot, error) {
	if err := value.inspect(); err != nil {
		return WorkspaceRoot{}, err
	}
	return value, nil
}

func (r WorkspaceRoot) inspect() error {
	if !canonicalDirectory(r.directory) || !canonicalDirectory(r.boundary) || !directoryContains(r.boundary, r.directory) {
		return sessionInvalid()
	}
	switch r.kind {
	case gitWorkspaceRoot:
		if r.native != r.boundary {
			return sessionInvalid()
		}
	case globalWorkspaceRoot:
		if r.native != "/" || r.boundary != filesystemBoundary(r.directory) {
			return sessionInvalid()
		}
		for directory, depth := r.directory, 0; depth < 1024; directory, depth = filepath.Dir(directory), depth+1 {
			if _, err := os.Lstat(filepath.Join(directory, ".git")); !os.IsNotExist(err) {
				return domain.Fail(domain.Unsupported, "OpenCode General Chat cannot inherit enclosing Git metadata.", "Preserve the workspace and use a separately verified repository profile.")
			}
			if directory == r.boundary {
				return nil
			}
			if filepath.Dir(directory) == directory {
				return sessionInvalid()
			}
		}
		return sessionInvalid()
	default:
		return sessionInvalid()
	}
	return nil
}

func (c apiSessionConfig) workspaceRoot() (WorkspaceRoot, error) {
	if c.Root != (WorkspaceRoot{}) {
		if c.NativeRoot != "" || c.Root.directory != c.Workspace {
			return WorkspaceRoot{}, sessionInvalid()
		}
		return checkedWorkspaceRoot(c.Root)
	}
	// Preserve the prior private Unix/Git initializer shape. Windows global
	// execution requires explicit independently constructed ownership; a drive
	// root or symbolic slash in this legacy field cannot acquire that profile.
	if runtime.GOOS != "windows" && c.NativeRoot == "/" {
		return GlobalWorkspaceRoot(c.Workspace)
	}
	return GitWorkspaceRoot(c.Workspace, c.NativeRoot)
}

func (r WorkspaceRoot) instructionRoot() string {
	if r.kind == globalWorkspaceRoot {
		return r.directory
	}
	return r.boundary
}

func (r WorkspaceRoot) windowsGlobal() bool {
	return runtime.GOOS == "windows" && r.kind == globalWorkspaceRoot
}
