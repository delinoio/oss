package grok

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const maxHistoryFileBytes = 32 << 20

type historyFiles struct {
	home    string
	root    *os.Root
	entries map[string]os.FileInfo
	logger  *slog.Logger
}

func historyUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The original Grok Build history requires reconciliation.", "Retain the original runtime, input and closure evidence; never reconstruct or resend native work.")
}

func openHistoryFiles(ctx context.Context, home string, logger *slog.Logger) (*historyFiles, error) {
	if ctx.Err() != nil || !filepath.IsAbs(home) || filepath.Clean(home) != home || filepath.Base(home) != "grok" || security.CheckPrivateDir(home) != nil {
		return nil, historyUncertain()
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil || canonical != home {
		return nil, historyUncertain()
	}
	info, err := os.Lstat(home)
	if err != nil {
		return nil, historyUncertain()
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, historyUncertain()
	}
	scope := &historyFiles{home: home, root: root, entries: map[string]os.FileInfo{".": info}, logger: logger}
	if err := scope.check(ctx); err != nil {
		_ = root.Close()
		return nil, err
	}
	return scope, nil
}

func (s *historyFiles) Close() { _ = s.root.Close() }

func sameHistoryFile(before, after os.FileInfo) bool {
	if before == nil || after == nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
		return false
	}
	// Directory creation timestamps/listing sizes do not identify transcript
	// contents. Every file retains exact identity, mode, size and modification.
	return before.IsDir() || before.Size() == after.Size() && before.ModTime() == after.ModTime()
}

func (s *historyFiles) check(ctx context.Context) error {
	if ctx.Err() != nil {
		return historyUncertain()
	}
	canonical, err := filepath.EvalSymlinks(s.home)
	if err != nil || canonical != s.home || security.CheckPrivateDir(s.home) != nil {
		return historyUncertain()
	}
	root, err := os.Lstat(s.home)
	if err != nil || !sameHistoryFile(s.entries["."], root) {
		return historyUncertain()
	}
	for name, before := range s.entries {
		if ctx.Err() != nil {
			return historyUncertain()
		}
		after, err := s.root.Lstat(name)
		if err != nil || !sameHistoryFile(before, after) || !ownedHistoryEntry(filepath.Join(s.home, name), after) {
			if s.logger != nil && before != nil && after != nil {
				s.logger.WarnContext(ctx, "Grok Build retained history entry changed", "root", name == ".", "directory", before.IsDir(), "same_identity", os.SameFile(before, after), "same_mode", before.Mode() == after.Mode(), "same_size", before.Size() == after.Size(), "same_mtime", before.ModTime() == after.ModTime())
			}
			return historyUncertain()
		}
	}
	return nil
}

// Native 1.0.41 percent-encodes all bytes outside RFC 3986's unreserved set,
// including plus, spaces, parentheses and Unicode. PathEscape alone preserves
// plus/colon and does not identify the native workspace directory faithfully.
func historySessionPath(workspace string, session domain.ID) (string, error) {
	if session.Validate() != nil || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace || domain.Text(workspace, "native workspace", 8192, true) != nil {
		return "", historyUncertain()
	}
	encoded := strings.ReplaceAll(url.QueryEscape(workspace), "+", "%20")
	return filepath.Join("sessions", encoded, string(session)), nil
}

func (s *historyFiles) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	if ctx.Err() != nil || !filepath.IsLocal(name) || filepath.Clean(name) != name || limit < 1 || limit > maxHistoryFileBytes {
		return nil, historyUncertain()
	}
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		path := filepath.Join(parts[:i+1]...)
		info, err := s.root.Lstat(path)
		if err != nil || !ownedHistoryEntry(filepath.Join(s.home, path), info) || i < len(parts)-1 && !info.IsDir() || i == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit) {
			return nil, historyUncertain()
		}
		if original, ok := s.entries[path]; ok && !sameHistoryFile(original, info) {
			return nil, historyUncertain()
		}
		s.entries[path] = info
	}
	file, err := s.root.OpenFile(name, historyReadFlags(), 0)
	if err != nil {
		return nil, historyUncertain()
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !sameHistoryFile(s.entries[name], before) || !ownedHistoryEntry(filepath.Join(s.home, name), before) || !ownedHistoryOpenFile(file) {
		return nil, historyUncertain()
	}
	result := make([]byte, 0, int(before.Size()))
	buffer := make([]byte, 32<<10)
	for {
		if ctx.Err() != nil {
			return nil, historyUncertain()
		}
		n, err := file.Read(buffer)
		if int64(len(result))+int64(n) > limit {
			return nil, historyUncertain()
		}
		result = append(result, buffer[:n]...)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, historyUncertain()
		}
	}
	after, err := file.Stat()
	if err != nil || !sameHistoryFile(before, after) || int64(len(result)) != after.Size() || !ownedHistoryOpenFile(file) {
		return nil, historyUncertain()
	}
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
