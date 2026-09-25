package claude

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type historyFilePhase string

const (
	historyScopePhase historyFilePhase = "scope"
	historyReadPhase  historyFilePhase = "read"
	historyProofPhase historyFilePhase = "proof"
)

// ReadMainTranscript reads the fixed native session path without creating or
// repairing state. The caller must hold its exclusive runtime/workspace lease
// and independently prove owned process cleanup before using these bytes as a
// checkpoint. This read neither stops a process nor grants Resume authority.
func ReadMainTranscript(ctx context.Context, home string, session domain.ID, workspace string, messages []HistoryMessageProof, compactions []HistoryCompactionProof, actions []HistoryCompactionActionProof, logger *slog.Logger) (TranscriptObservation, error) {
	return readMainTranscript(ctx, home, session, workspace, messages, compactions, actions, nil, logger)
}

func readMainTranscript(ctx context.Context, home string, session domain.ID, workspace string, messages []HistoryMessageProof, compactions []HistoryCompactionProof, actions []HistoryCompactionActionProof, resumes []historyResumeProof, logger *slog.Logger) (observation TranscriptObservation, returned error) {
	phase := historyScopePhase
	defer func() { logHistoryRead(ctx, logger, session, false, phase, returned) }()
	if session.Validate() != nil {
		return observation, historyUncertain()
	}
	scope, err := openHistoryFiles(ctx, home)
	if err != nil {
		return observation, err
	}
	defer scope.Close()
	phase = historyReadPhase
	raw, err := scope.read(ctx, filepath.Join("projects", "delidev", string(session)+".jsonl"), maxHistoryTranscript)
	if err != nil {
		return observation, err
	}
	phase = historyProofPhase
	return verifyResumedTranscript(ctx, raw, session, workspace, messages, nil, compactions, actions, resumes)
}

// ReadChildTranscript derives filenames from the independently retained native
// task, never from sidecar contents or a model-provided path. The original pair
// stays pinned until both reads finish; native 0644 Unix sidecars remain behind
// the checked owner-only root without relaxing security.ReadPrivate.
func ReadChildTranscript(ctx context.Context, home string, session domain.ID, workspace string, binding ChildHistoryBinding, messages []HistoryMessageProof, logger *slog.Logger) (observation ChildTranscriptObservation, returned error) {
	phase := historyScopePhase
	defer func() { logHistoryRead(ctx, logger, session, true, phase, returned) }()
	if session.Validate() != nil || !nativeHistoryTaskFilename(binding.TaskID) {
		return observation, historyUncertain()
	}
	scope, err := openHistoryFiles(ctx, home)
	if err != nil {
		return observation, err
	}
	defer scope.Close()
	phase = historyReadPhase
	base := filepath.Join("projects", "delidev", string(session), "subagents", "agent-"+binding.TaskID)
	raw, err := scope.read(ctx, base+".jsonl", maxHistoryTranscript)
	if err != nil {
		return observation, err
	}
	metadata, err := scope.read(ctx, base+".meta.json", 64<<10)
	if err != nil {
		return observation, err
	}
	if err := scope.check(ctx); err != nil {
		return observation, err
	}
	phase = historyProofPhase
	return VerifyChildTranscript(ctx, raw, metadata, session, workspace, binding, messages)
}

func logHistoryRead(ctx context.Context, logger *slog.Logger, session domain.ID, child bool, phase historyFilePhase, err error) {
	if logger == nil {
		return
	}
	// Do not log paths, native task names, payloads or filesystem error strings.
	// Even rejected caller session IDs are omitted until their type is checked.
	if session.Validate() != nil {
		session = ""
	}
	if err != nil {
		logger.WarnContext(ctx, "Claude Code retained transcript inspection failed", "session_id", session, "child", child, "phase", phase, "code", domain.SafeError(err).Code)
	} else {
		logger.InfoContext(ctx, "Claude Code retained transcript inspected", "session_id", session, "child", child)
	}
}

func nativeHistoryTaskFilename(task string) bool {
	if len(task) == 0 || len(task) > 128 {
		return false
	}
	for _, c := range task {
		// Original native Agent IDs are ASCII filesystem tokens. Proving a task
		// owner alone must not grant path separators, drives, ADS or aliases.
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

type historyFiles struct {
	home    string
	root    *os.Root
	entries map[string]os.FileInfo
}

func openHistoryFiles(ctx context.Context, home string) (*historyFiles, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || filepath.Base(home) != "claude" || security.CheckPrivateDir(home) != nil {
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
	scope := &historyFiles{home: home, root: root, entries: map[string]os.FileInfo{".": info}}
	if err := scope.check(ctx); err != nil {
		root.Close()
		return nil, err
	}
	return scope, nil
}

func (scope *historyFiles) Close() { _ = scope.root.Close() }

func (scope *historyFiles) check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	canonical, err := filepath.EvalSymlinks(scope.home)
	if err != nil || canonical != scope.home || security.CheckPrivateDir(scope.home) != nil {
		return historyUncertain()
	}
	root, err := os.Lstat(scope.home)
	if err != nil || !sameHistoryFile(scope.entries["."], root) {
		return historyUncertain()
	}
	for name, before := range scope.entries {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		after, err := scope.root.Lstat(name)
		if err != nil || !sameHistoryFile(before, after) || !ownedHistoryEntry(filepath.Join(scope.home, name), after) {
			return historyUncertain()
		}
	}
	return nil
}

func sameHistoryFile(before, after os.FileInfo) bool {
	return os.SameFile(before, after) && before.Mode() == after.Mode() && before.Size() == after.Size() && before.ModTime() == after.ModTime()
}

func (scope *historyFiles) read(ctx context.Context, name string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if !filepath.IsLocal(name) || filepath.Clean(name) != name || limit < 1 || limit > maxHistoryTranscript {
		return nil, historyUncertain()
	}
	parts := strings.Split(name, string(filepath.Separator))
	for i := range parts {
		path := filepath.Join(parts[:i+1]...)
		info, err := scope.root.Lstat(path)
		if err != nil || !ownedHistoryEntry(filepath.Join(scope.home, path), info) || (i < len(parts)-1 && !info.IsDir()) || (i == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() < 1 || info.Size() > limit)) {
			return nil, historyUncertain()
		}
		if original, ok := scope.entries[path]; ok && !sameHistoryFile(original, info) {
			return nil, historyUncertain()
		}
		scope.entries[path] = info
	}
	file, err := scope.root.OpenFile(name, historyReadFlags(), 0)
	if err != nil {
		return nil, historyUncertain()
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil || !sameHistoryFile(scope.entries[name], before) || !ownedHistoryEntry(filepath.Join(scope.home, name), before) || !ownedHistoryOpenFile(file) {
		return nil, historyUncertain()
	}
	result := make([]byte, 0, int(before.Size()))
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, domain.SafeError(err)
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
	if err := scope.check(ctx); err != nil {
		return nil, err
	}
	return result, nil
}
