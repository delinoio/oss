package opencode

import (
	"path/filepath"
	"runtime"
)

func validCheckpointRoots(value nativeCheckpoint) bool {
	switch value.Version {
	case 1:
		// Preserve the original validator and omitted-field canonical bytes.
		return value.FilesystemRoot == "" && checkpointPath(value.NativeRoot) && directoryContains(value.NativeRoot, value.Workspace)
	case 2:
		// Only newly captured Windows global checkpoints carry this authority.
		// Older bytes cannot be rewritten or promoted during replacement.
		return runtime.GOOS == "windows" && value.NativeRoot == "/" && value.Project == "global" && value.Snapshot == nil && value.ProjectAdoption == nil && len(value.References) == 0 && checkpointPath(value.FilesystemRoot) && value.FilesystemRoot == filesystemBoundary(value.Workspace) && directoryContains(value.FilesystemRoot, value.Workspace) && filesystemBoundary(value.RuntimeHome) != ""
	default:
		return false
	}
}

func checkpointMatchesRoot(value nativeCheckpoint, root WorkspaceRoot) bool {
	if value.NativeRoot != root.native || value.Workspace != root.directory {
		return false
	}
	if root.windowsGlobal() {
		return value.Version == 2 && value.FilesystemRoot == root.boundary
	}
	return value.Version == 1 && value.FilesystemRoot == "" && value.NativeRoot == root.boundary
}

func checkpointGlobalRoot(value nativeCheckpoint) bool {
	if value.Version == 2 {
		return validCheckpointRoots(value)
	}
	return value.Project == "global" && filepath.Dir(value.NativeRoot) == value.NativeRoot
}
