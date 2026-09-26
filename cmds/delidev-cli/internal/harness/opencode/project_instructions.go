package opencode

import (
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxProjectInstructionBytes = 256 << 10
const maxProjectInstructionAncestors = 128

type projectInstructionSource struct {
	Path   string            `json:"-"`
	Digest [sha256.Size]byte `json:"-"`
	Size   int64             `json:"-"`
}

// Only private comparison metadata is retained. The pinned native loader reads
// these exact paths through its additive instructions option; DeliDev never
// replaces the native prompt or merges project files into Agent templates.
type projectInstructions struct {
	Root      string                     `json:"-"`
	Directory string                     `json:"-"`
	Sources   []projectInstructionSource `json:"-"`
}

func collectProjectInstructions(directory, nativeRoot string) (*projectInstructions, error) {
	// Native projectless workspaces report the filesystem root as worktree.
	// Restrict their explicit instruction selection to the session workspace,
	// never unrelated ancestors of a General Chat directory.
	root := nativeRoot
	if filepath.Dir(root) == root {
		root = directory
	}
	if !canonicalDirectory(root) || !canonicalDirectory(directory) || !directoryContains(root, directory) {
		return nil, sessionInvalid()
	}
	beforeRoot, err := os.Lstat(root)
	if err != nil {
		return nil, sessionProblem()
	}
	fs, err := os.OpenRoot(root)
	if err != nil {
		return nil, sessionProblem()
	}
	defer fs.Close()
	opened, err := fs.Stat(".")
	if err != nil || !os.SameFile(beforeRoot, opened) {
		return nil, sessionProblem()
	}
	type ancestor struct {
		name string
		info os.FileInfo
	}
	var ancestors []ancestor
	for current := directory; ; current = filepath.Dir(current) {
		if len(ancestors) >= maxProjectInstructionAncestors {
			return nil, incompatible()
		}
		rel, err := filepath.Rel(root, current)
		if err != nil {
			return nil, sessionProblem()
		}
		info, err := fs.Lstat(rel)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, sessionProblem()
		}
		ancestors = append(ancestors, ancestor{rel, info})
		if current == root {
			break
		}
	}
	result := &projectInstructions{Root: root, Directory: directory}
	// Native selection chooses the first filename family with any matches,
	// retaining every matching ancestor in cwd-to-worktree order. This private
	// profile disables Claude compatibility, so CLAUDE.md is not a candidate.
	for _, filename := range []string{"AGENTS.md", "CONTEXT.md"} {
		var total int64
		for _, dir := range ancestors {
			name := filepath.Join(dir.name, filename)
			info, err := fs.Lstat(name)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxProjectInstructionBytes-total {
				return nil, sessionProblem()
			}
			raw, err := readProjectInstruction(fs, name, info)
			if err != nil {
				return nil, err
			}
			total += int64(len(raw))
			result.Sources = append(result.Sources, projectInstructionSource{Path: filepath.Join(root, name), Digest: sha256.Sum256(raw), Size: int64(len(raw))})
		}
		if len(result.Sources) > 0 {
			break
		}
	}
	for _, dir := range ancestors {
		after, err := fs.Lstat(dir.name)
		if err != nil || !os.SameFile(dir.info, after) || dir.info.Mode() != after.Mode() {
			return nil, sessionProblem()
		}
	}
	afterRoot, err := os.Lstat(root)
	if err != nil || !os.SameFile(beforeRoot, afterRoot) || !canonicalDirectory(directory) {
		return nil, sessionProblem()
	}
	return result, nil
}

func readProjectInstruction(root *os.Root, name string, expected os.FileInfo) ([]byte, error) {
	file, err := root.OpenFile(name, projectInstructionReadFlags(), 0)
	if err != nil {
		return nil, sessionProblem()
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !sameProjectInstruction(expected, before) {
		return nil, sessionProblem()
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxProjectInstructionBytes+1))
	if err != nil || len(raw) > maxProjectInstructionBytes || domain.Text(string(raw), "project instructions", maxProjectInstructionBytes, false) != nil {
		return nil, sessionProblem()
	}
	after, err := file.Stat()
	if err != nil || !sameProjectInstruction(before, after) || after.Size() != int64(len(raw)) {
		return nil, sessionProblem()
	}
	retained, err := root.Lstat(name)
	if err != nil || !sameProjectInstruction(after, retained) {
		return nil, sessionProblem()
	}
	return raw, nil
}

func sameProjectInstruction(a, b os.FileInfo) bool {
	return a != nil && b != nil && a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime() == b.ModTime()
}

func (p *projectInstructions) inspect() error {
	if p == nil {
		return nil
	}
	current, err := collectProjectInstructions(p.Directory, p.Root)
	if err != nil || current.Root != p.Root || !slices.Equal(current.Sources, p.Sources) {
		return sessionProblem()
	}
	return nil
}
