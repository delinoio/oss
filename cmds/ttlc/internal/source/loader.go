package source

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultCacheRelativePath = ".ttl/cache/cache.sqlite3"

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
			return Paths{}, fmt.Errorf("resolve cwd: %w", err)
		}
		cwd = resolvedCwd
	}

	workspaceRoot, err := resolvePathWithSymlinks(cwd)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve workspace root: %w", err)
	}

	entryCandidate := entryPath
	if !filepath.IsAbs(entryCandidate) {
		entryCandidate = filepath.Join(workspaceRoot, entryCandidate)
	}
	entryResolved, err := resolvePathWithSymlinks(entryCandidate)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve entry path: %w", err)
	}
	if !isWithinPath(workspaceRoot, entryResolved) {
		return Paths{}, fmt.Errorf("entry path escapes workspace root: %s", entryPath)
	}
	if strings.ToLower(filepath.Ext(entryResolved)) != ".ttl" {
		return Paths{}, fmt.Errorf("entry file must use .ttl extension: %s", entryPath)
	}

	outDirCandidate := outDir
	if !filepath.IsAbs(outDirCandidate) {
		outDirCandidate = filepath.Join(workspaceRoot, outDirCandidate)
	}
	outDirResolved, err := resolvePathWithSymlinks(outDirCandidate)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve out-dir path: %w", err)
	}
	if !isWithinPath(workspaceRoot, outDirResolved) {
		return Paths{}, fmt.Errorf("out-dir path escapes workspace root: %s", outDir)
	}

	cacheDBCandidate := filepath.Join(workspaceRoot, defaultCacheRelativePath)
	cacheDBResolved, err := resolvePathWithSymlinks(cacheDBCandidate)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cache db path: %w", err)
	}
	if !isWithinPath(workspaceRoot, cacheDBResolved) {
		return Paths{}, fmt.Errorf("cache db path escapes workspace root: %s", cacheDBResolved)
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
	if depth > 64 {
		return "", fmt.Errorf("resolve symlinks depth exceeded for %s", path)
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %s: %w", path, err)
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
			return "", fmt.Errorf("eval symlinks %s: %w", current, err)
		}

		pathInfo, lstatErr := os.Lstat(current)
		if lstatErr == nil && pathInfo.Mode()&os.ModeSymlink != 0 {
			linkTarget, readlinkErr := os.Readlink(current)
			if readlinkErr != nil {
				return "", fmt.Errorf("read symlink %s: %w", current, readlinkErr)
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
			return "", fmt.Errorf("lstat path %s: %w", current, lstatErr)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("eval symlinks %s: %w", current, err)
		}
		missingSegments = append(missingSegments, filepath.Base(current))
		current = parent
	}
}

func ResolveImportPath(workspaceRoot string, currentFilePath string, importPath string) (string, error) {
	if strings.TrimSpace(importPath) == "" {
		return "", fmt.Errorf("import path is empty")
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
		return "", fmt.Errorf("resolve import path %q: %w", importPath, err)
	}
	resolvedRoot, err := resolvePathWithSymlinks(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root for import: %w", err)
	}
	if !isWithinPath(resolvedRoot, resolved) {
		return "", fmt.Errorf("import path escapes workspace root: %s", importPath)
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
