package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/ttlc/internal/messages"
)

const defaultCacheRelativePath = ".ttl/cache/cache.sqlite3"
const maxSymlinkDepth = 64

type Paths struct {
	WorkspaceRoot string
	EntryPath     string
	OutDir        string
	CacheDir      string
	CacheDBPath   string
}

func ResolvePaths(cwd string, entryPath string, outDir string) (Paths, error) {
	if strings.TrimSpace(cwd) == "" {
		resolvedCwd, err := os.Getwd()
		if err != nil {
			return Paths{}, messages.WrapError(messages.ErrorResolveCwd, err)
		}
		cwd = resolvedCwd
	}

	workspaceRoot, err := resolvePathWithSymlinks(cwd)
	if err != nil {
		return Paths{}, messages.WrapError(messages.ErrorResolveWorkspaceRoot, err, cwd)
	}

	entryCandidate := entryPath
	if !filepath.IsAbs(entryCandidate) {
		entryCandidate = filepath.Join(workspaceRoot, entryCandidate)
	}
	entryResolved, err := resolvePathWithSymlinks(entryCandidate)
	if err != nil {
		return Paths{}, messages.WrapError(messages.ErrorResolveEntryPath, err, entryPath, workspaceRoot)
	}
	if !isWithinPath(workspaceRoot, entryResolved) {
		return Paths{}, messages.NewError(messages.ErrorEntryEscapesWorkspace, entryPath, workspaceRoot)
	}
	if strings.ToLower(filepath.Ext(entryResolved)) != ".ttl" {
		return Paths{}, messages.NewError(messages.ErrorEntryFileExtension, entryPath, filepath.Ext(entryResolved))
	}

	outDirCandidate := outDir
	if !filepath.IsAbs(outDirCandidate) {
		outDirCandidate = filepath.Join(workspaceRoot, outDirCandidate)
	}
	outDirResolved, err := resolvePathWithSymlinks(outDirCandidate)
	if err != nil {
		return Paths{}, messages.WrapError(messages.ErrorResolveOutDirPath, err, outDir, workspaceRoot)
	}
	if !isWithinPath(workspaceRoot, outDirResolved) {
		return Paths{}, messages.NewError(messages.ErrorOutDirEscapesWorkspace, outDir, workspaceRoot)
	}

	cacheDBCandidate := filepath.Join(workspaceRoot, defaultCacheRelativePath)
	cacheDBResolved, err := resolvePathWithSymlinks(cacheDBCandidate)
	if err != nil {
		return Paths{}, messages.WrapError(messages.ErrorResolveCacheDBPath, err, cacheDBCandidate)
	}
	if !isWithinPath(workspaceRoot, cacheDBResolved) {
		return Paths{}, messages.NewError(messages.ErrorCacheDBEscapesWorkspace, cacheDBResolved, workspaceRoot)
	}

	return Paths{
		WorkspaceRoot: workspaceRoot,
		EntryPath:     entryResolved,
		OutDir:        outDirResolved,
		CacheDir:      filepath.Dir(cacheDBResolved),
		CacheDBPath:   cacheDBResolved,
	}, nil
}

func resolvePathWithSymlinks(path string) (string, error) {
	return resolvePathWithSymlinksAtDepth(path, 0)
}

func resolvePathWithSymlinksAtDepth(path string, depth int) (string, error) {
	if depth > maxSymlinkDepth {
		return "", messages.NewError(messages.ErrorSymlinkDepthExceeded, path, maxSymlinkDepth)
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", messages.WrapError(messages.ErrorResolveAbsolutePath, err, path)
	}
	current := filepath.Clean(absolutePath)
	missingSegments := make([]string, 0, 4)

	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			resolvedPath := filepath.Clean(resolved)
			for i := len(missingSegments) - 1; i >= 0; i-- {
				resolvedPath = filepath.Join(resolvedPath, missingSegments[i])
			}
			return resolvedPath, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", messages.WrapError(messages.ErrorEvaluateSymlinks, err, current)
		}

		pathInfo, lstatErr := os.Lstat(current)
		if lstatErr == nil && pathInfo.Mode()&os.ModeSymlink != 0 {
			linkTarget, readlinkErr := os.Readlink(current)
			if readlinkErr != nil {
				return "", messages.WrapError(messages.ErrorReadSymlink, readlinkErr, current)
			}
			resolvedLink := linkTarget
			if !filepath.IsAbs(resolvedLink) {
				resolvedLink = filepath.Join(filepath.Dir(current), resolvedLink)
			}
			resolvedLink = filepath.Clean(resolvedLink)
			for i := len(missingSegments) - 1; i >= 0; i-- {
				resolvedLink = filepath.Join(resolvedLink, missingSegments[i])
			}
			return resolvePathWithSymlinksAtDepth(resolvedLink, depth+1)
		}
		if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
			return "", messages.WrapError(messages.ErrorStatPath, lstatErr, current)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", messages.WrapError(messages.ErrorEvaluateSymlinks, err, current)
		}
		missingSegments = append(missingSegments, filepath.Base(current))
		current = parent
	}
}

func ResolveImportPath(workspaceRoot string, currentFilePath string, importPath string) (string, error) {
	if strings.TrimSpace(importPath) == "" {
		return "", messages.NewError(messages.ErrorImportPathEmpty)
	}

	var candidate string
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		currentDir := filepath.Dir(currentFilePath)
		candidate = filepath.Join(currentDir, importPath)
	} else {
		candidate = filepath.Join(workspaceRoot, importPath)
	}

	if !strings.HasSuffix(candidate, ".ttl") {
		candidate += ".ttl"
	}

	resolved, err := resolvePathWithSymlinks(candidate)
	if err != nil {
		return "", messages.WrapError(messages.ErrorResolveImportPath, err, importPath, currentFilePath)
	}
	resolvedRoot, err := resolvePathWithSymlinks(workspaceRoot)
	if err != nil {
		return "", messages.WrapError(messages.ErrorResolveImportWorkspaceRoot, err, workspaceRoot)
	}
	if !isWithinPath(resolvedRoot, resolved) {
		return "", messages.NewError(messages.ErrorImportEscapesWorkspace, importPath, resolvedRoot)
	}
	return resolved, nil
}

func isWithinPath(basePath string, candidatePath string) bool {
	relPath, err := filepath.Rel(basePath, candidatePath)
	if err != nil {
		return false
	}
	if relPath == "." {
		return true
	}
	if relPath == ".." {
		return false
	}
	if strings.HasPrefix(relPath, ".."+string(os.PathSeparator)) {
		return false
	}
	return !filepath.IsAbs(relPath)
}
