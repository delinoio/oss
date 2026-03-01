package state

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/derun/internal/contracts"
	"github.com/delinoio/oss/cmds/derun/internal/session"
)

const (
	metaFileName   = "meta.json"
	outputFileName = "output.bin"
	indexFileName  = "index.jsonl"
	finalFileName  = "final.json"
	lockFileName   = "append.lock"
)

var (
	ErrSessionNotFound  = errors.New("session not found")
	ErrInvalidSessionID = errors.New("invalid session id")
)

type Store struct {
	root string
}

func New(root string) (*Store, error) {
	if root == "" {
		return nil, errors.New("state root is empty")
	}
	if err := EnsureDir(root); err != nil {
		return nil, err
	}
	if err := EnsureDir(filepath.Join(root, "sessions")); err != nil {
		return nil, err
	}
	return &Store{root: root}, nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) EnsureSessionDir(sessionID string) error {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return err
	}
	return EnsureDir(dir)
}

func (s *Store) HasSessionMetadata(sessionID string) (bool, error) {
	metaPath, err := s.sessionFile(sessionID, metaFileName)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(metaPath); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat meta file: %w", err)
	}

	finalPath, err := s.sessionFile(sessionID, finalFileName)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(finalPath); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat final file: %w", err)
	}

	return false, nil
}

func (s *Store) WriteMeta(meta session.Meta) error {
	if err := s.EnsureSessionDir(meta.SessionID); err != nil {
		return err
	}
	path, err := s.sessionFile(meta.SessionID, metaFileName)
	if err != nil {
		return err
	}
	return writeAtomicJSON(path, meta)
}

func (s *Store) WriteFinal(final session.Final) error {
	if err := s.EnsureSessionDir(final.SessionID); err != nil {
		return err
	}
	path, err := s.sessionFile(final.SessionID, finalFileName)
	if err != nil {
		return err
	}
	return writeAtomicJSON(path, final)
}

func (s *Store) AppendOutput(sessionID string, channel contracts.DerunOutputChannel, data []byte, ts time.Time) (uint64, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := s.EnsureSessionDir(sessionID); err != nil {
		return 0, err
	}

	lockPath, err := s.sessionFile(sessionID, lockFileName)
	if err != nil {
		return 0, err
	}
	outputPath, err := s.sessionFile(sessionID, outputFileName)
	if err != nil {
		return 0, err
	}
	indexPath, err := s.sessionFile(sessionID, indexFileName)
	if err != nil {
		return 0, err
	}

	lockHandle, err := lockFile(lockPath)
	if err != nil {
		return 0, err
	}
	defer unlockFile(lockHandle)

	outputFile, err := os.OpenFile(outputPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open output file: %w", err)
	}
	defer outputFile.Close()

	offset, err := outputFile.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, fmt.Errorf("seek output file: %w", err)
	}
	if _, err := outputFile.Write(data); err != nil {
		return 0, fmt.Errorf("write output file: %w", err)
	}
	if err := outputFile.Sync(); err != nil {
		return 0, fmt.Errorf("sync output file: %w", err)
	}

	entry := session.IndexEntry{
		Offset:    uint64(offset),
		Length:    uint64(len(data)),
		Channel:   channel,
		Timestamp: ts.UTC(),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return 0, fmt.Errorf("marshal index entry: %w", err)
	}
	line = append(line, '\n')

	indexFile, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open index file: %w", err)
	}
	defer indexFile.Close()
	if _, err := indexFile.Write(line); err != nil {
		return 0, fmt.Errorf("write index file: %w", err)
	}
	if err := indexFile.Sync(); err != nil {
		return 0, fmt.Errorf("sync index file: %w", err)
	}

	return uint64(offset), nil
}

func (s *Store) ListSessions(stateFilter contracts.DerunSessionState, limit int) ([]session.Summary, int, error) {
	sessionsPath := filepath.Join(s.root, "sessions")
	entries, err := os.ReadDir(sessionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("read sessions directory: %w", err)
	}

	summaries := make([]session.Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		detail, err := s.GetSession(entry.Name())
		if err != nil {
			continue
		}
		if stateFilter != "" && detail.State != stateFilter {
			continue
		}
		summaries = append(summaries, detail.Summary)
	}

	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].StartedAt.After(summaries[j].StartedAt)
	})

	total := len(summaries)
	if limit > 0 && len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries, total, nil
}

func (s *Store) GetSession(sessionID string) (session.Detail, error) {
	metaPath, err := s.sessionFile(sessionID, metaFileName)
	if err != nil {
		return session.Detail{}, err
	}
	var meta session.Meta
	if err := readJSON(metaPath, &meta); err != nil {
		return session.Detail{}, fmt.Errorf("read meta file: %w", err)
	}

	state := contracts.DerunSessionStateRunning
	var endedAt *time.Time
	var exitCode *int
	var signal string
	var finalErr string

	finalPath, err := s.sessionFile(sessionID, finalFileName)
	if err != nil {
		return session.Detail{}, err
	}
	var final session.Final
	if err := readJSON(finalPath, &final); err == nil {
		state = final.State
		copyEndedAt := final.EndedAt
		endedAt = &copyEndedAt
		exitCode = final.ExitCode
		signal = final.Signal
		finalErr = final.Error
	} else if !os.IsNotExist(err) {
		return session.Detail{}, fmt.Errorf("read final file: %w", err)
	} else {
		if !processAlive(meta.PID) {
			state = contracts.DerunSessionStateFailed
		}
	}

	outputBytes, chunkCount, lastChunkAt, err := s.outputStats(sessionID)
	if err != nil {
		return session.Detail{}, err
	}

	return session.Detail{
		Summary: session.Summary{
			SessionID:        meta.SessionID,
			State:            state,
			StartedAt:        meta.StartedAt,
			EndedAt:          endedAt,
			TransportMode:    meta.TransportMode,
			TTYAttached:      meta.TTYAttached,
			RetentionSeconds: meta.RetentionSeconds,
			PID:              meta.PID,
		},
		ExitCode:    exitCode,
		Signal:      signal,
		Error:       finalErr,
		OutputBytes: outputBytes,
		ChunkCount:  chunkCount,
		LastChunkAt: lastChunkAt,
	}, nil
}

func (s *Store) ReadOutput(sessionID string, cursor uint64, maxBytes int) ([]session.OutputChunk, uint64, bool, error) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}

	hasMetadata, err := s.HasSessionMetadata(sessionID)
	if err != nil {
		return nil, cursor, false, fmt.Errorf("check session metadata: %w", err)
	}
	if !hasMetadata {
		return nil, cursor, false, ErrSessionNotFound
	}

	entries, err := s.readIndexEntries(sessionID)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, cursor, true, nil
		}
		return nil, cursor, false, err
	}

	outputPath, err := s.sessionFile(sessionID, outputFileName)
	if err != nil {
		return nil, cursor, false, err
	}
	outputFile, err := os.Open(outputPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, cursor, true, nil
		}
		return nil, cursor, false, fmt.Errorf("open output file: %w", err)
	}
	defer outputFile.Close()

	fileInfo, err := outputFile.Stat()
	if err != nil {
		return nil, cursor, false, fmt.Errorf("stat output file: %w", err)
	}
	outputSize := uint64(fileInfo.Size())
	if cursor > outputSize {
		cursor = outputSize
	}

	remaining := maxBytes
	chunks := make([]session.OutputChunk, 0)
	nextCursor := cursor

	for _, entry := range entries {
		entryStart := entry.Offset
		entryEnd := entry.Offset + entry.Length
		if entryEnd <= cursor {
			continue
		}
		if remaining <= 0 {
			break
		}

		chunkStart := entryStart
		if cursor > chunkStart {
			chunkStart = cursor
		}
		chunkEnd := entryEnd
		maxEnd := chunkStart + uint64(remaining)
		if chunkEnd > maxEnd {
			chunkEnd = maxEnd
		}
		if chunkEnd <= chunkStart {
			continue
		}

		length := chunkEnd - chunkStart
		buf := make([]byte, length)
		if _, err := outputFile.ReadAt(buf, int64(chunkStart)); err != nil && !errors.Is(err, io.EOF) {
			return nil, cursor, false, fmt.Errorf("read output chunk: %w", err)
		}

		chunks = append(chunks, session.OutputChunk{
			Channel:     entry.Channel,
			StartCursor: strconv.FormatUint(chunkStart, 10),
			EndCursor:   strconv.FormatUint(chunkEnd, 10),
			DataBase64:  base64.StdEncoding.EncodeToString(buf),
			Timestamp:   entry.Timestamp,
		})

		nextCursor = chunkEnd
		remaining -= int(length)
		if remaining <= 0 {
			break
		}
	}

	eof := nextCursor >= outputSize
	return chunks, nextCursor, eof, nil
}

func (s *Store) sessionDir(sessionID string) (string, error) {
	if err := validateSessionID(sessionID); err != nil {
		return "", err
	}
	base := filepath.Clean(filepath.Join(s.root, "sessions"))
	dir := filepath.Clean(filepath.Join(base, sessionID))
	if !isWithinPath(base, dir) {
		return "", fmt.Errorf("invalid session path")
	}

	resolvedBase, err := resolvePathWithSymlinks(base)
	if err != nil {
		return "", fmt.Errorf("resolve sessions path: %w", err)
	}
	resolvedDir, err := resolvePathWithSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve session path: %w", err)
	}
	if !isWithinPath(resolvedBase, resolvedDir) {
		return "", fmt.Errorf("session path symlink escape: resolved=%s base=%s", resolvedDir, resolvedBase)
	}

	return dir, nil
}

func (s *Store) sessionFile(sessionID, fileName string) (string, error) {
	dir, err := s.sessionDir(sessionID)
	if err != nil {
		return "", err
	}
	path := filepath.Clean(filepath.Join(dir, fileName))
	if !isWithinPath(dir, path) {
		return "", fmt.Errorf("invalid session file path")
	}

	resolvedDir, err := resolvePathWithSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolve session directory path: %w", err)
	}
	resolvedPath, err := resolvePathWithSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve session file path: %w", err)
	}
	if !isWithinPath(resolvedDir, resolvedPath) {
		return "", fmt.Errorf("session file symlink escape: file=%s resolved=%s session=%s", path, resolvedPath, resolvedDir)
	}

	return path, nil
}

func (s *Store) outputStats(sessionID string) (uint64, uint64, *time.Time, error) {
	outputPath, err := s.sessionFile(sessionID, outputFileName)
	if err != nil {
		return 0, 0, nil, err
	}
	var outputBytes uint64
	if info, err := os.Stat(outputPath); err == nil {
		outputBytes = uint64(info.Size())
	} else if !os.IsNotExist(err) {
		return 0, 0, nil, fmt.Errorf("stat output file: %w", err)
	}

	entries, err := s.readIndexEntries(sessionID)
	if err != nil && !os.IsNotExist(err) {
		return 0, 0, nil, err
	}
	chunkCount := uint64(len(entries))
	var lastChunkAt *time.Time
	if len(entries) > 0 {
		last := entries[len(entries)-1].Timestamp
		lastChunkAt = &last
	}
	return outputBytes, chunkCount, lastChunkAt, nil
}

func (s *Store) readIndexEntries(sessionID string) ([]session.IndexEntry, error) {
	indexPath, err := s.sessionFile(sessionID, indexFileName)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(indexPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)
	entries := make([]session.IndexEntry, 0)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry session.IndexEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan index file: %w", err)
	}
	return entries, nil
}

func validateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("%w: session id is empty", ErrInvalidSessionID)
	}
	if sessionID == "." {
		return fmt.Errorf("%w: session id contains invalid path segment alias", ErrInvalidSessionID)
	}
	if strings.Contains(sessionID, "..") {
		return fmt.Errorf("%w: session id contains invalid path segment", ErrInvalidSessionID)
	}
	if strings.ContainsAny(sessionID, `/\\`) {
		return fmt.Errorf("%w: session id contains path separator", ErrInvalidSessionID)
	}
	return nil
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

		// EvalSymlinks returns os.ErrNotExist for dangling symlink targets as well.
		// Detect that case and continue resolution from the symlink target.
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

func isWithinPath(basePath, candidatePath string) bool {
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

func readJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode json file %s: %w", path, err)
	}
	return nil
}

func writeAtomicJSON(path string, value any) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("chmod target file: %w", err)
	}
	return nil
}
