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
	"github.com/delinoio/oss/cmds/derun/internal/errmsg"
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
		return nil, errmsg.Error("state root is empty", map[string]any{
			"state_root": root,
		})
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
		return false, errors.New(errmsg.Runtime("stat meta file", err, map[string]any{
			"session_id": sessionID,
			"meta_path":  metaPath,
		}))
	}

	finalPath, err := s.sessionFile(sessionID, finalFileName)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(finalPath); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, errors.New(errmsg.Runtime("stat final file", err, map[string]any{
			"session_id": sessionID,
			"final_path": finalPath,
		}))
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
		return 0, errors.New(errmsg.Runtime("open output file", err, map[string]any{
			"session_id":  sessionID,
			"output_path": outputPath,
		}))
	}
	defer outputFile.Close()

	offset, err := outputFile.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, errors.New(errmsg.Runtime("seek output file", err, map[string]any{
			"session_id":  sessionID,
			"output_path": outputPath,
		}))
	}
	if _, err := outputFile.Write(data); err != nil {
		return 0, errors.New(errmsg.Runtime("write output file", err, map[string]any{
			"session_id":  sessionID,
			"output_path": outputPath,
			"chunk_size":  len(data),
		}))
	}
	if err := outputFile.Sync(); err != nil {
		return 0, errors.New(errmsg.Runtime("sync output file", err, map[string]any{
			"session_id":  sessionID,
			"output_path": outputPath,
		}))
	}

	entry := session.IndexEntry{
		Offset:    uint64(offset),
		Length:    uint64(len(data)),
		Channel:   channel,
		Timestamp: ts.UTC(),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return 0, errors.New(errmsg.Runtime("marshal index entry", err, map[string]any{
			"session_id": sessionID,
			"channel":    channel,
			"chunk_size": len(data),
		}))
	}
	line = append(line, '\n')

	indexFile, err := os.OpenFile(indexPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, errors.New(errmsg.Runtime("open index file", err, map[string]any{
			"session_id": sessionID,
			"index_path": indexPath,
		}))
	}
	defer indexFile.Close()
	if _, err := indexFile.Write(line); err != nil {
		return 0, errors.New(errmsg.Runtime("write index file", err, map[string]any{
			"session_id": sessionID,
			"index_path": indexPath,
			"line_bytes": len(line),
		}))
	}
	if err := indexFile.Sync(); err != nil {
		return 0, errors.New(errmsg.Runtime("sync index file", err, map[string]any{
			"session_id": sessionID,
			"index_path": indexPath,
		}))
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
		return nil, 0, errors.New(errmsg.Runtime("read sessions directory", err, map[string]any{
			"sessions_path": sessionsPath,
			"state_filter":  stateFilter,
			"limit":         limit,
		}))
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
		return session.Detail{}, errors.New(errmsg.Runtime("read meta file", err, map[string]any{
			"session_id": sessionID,
			"meta_path":  metaPath,
		}))
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
		return session.Detail{}, errors.New(errmsg.Runtime("read final file", err, map[string]any{
			"session_id": sessionID,
			"final_path": finalPath,
		}))
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
		detailedErr := errmsg.Wrap(err, map[string]any{
			"session_id": sessionID,
			"cursor":     cursor,
			"max_bytes":  maxBytes,
		})
		return nil, cursor, false, fmt.Errorf("failed to check session metadata: %w", detailedErr)
	}
	if !hasMetadata {
		return nil, cursor, false, errmsg.Wrap(ErrSessionNotFound, map[string]any{
			"session_id": sessionID,
			"cursor":     cursor,
			"max_bytes":  maxBytes,
		})
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
		return nil, cursor, false, errors.New(errmsg.Runtime("open output file", err, map[string]any{
			"session_id":  sessionID,
			"cursor":      cursor,
			"max_bytes":   maxBytes,
			"output_path": outputPath,
		}))
	}
	defer outputFile.Close()

	fileInfo, err := outputFile.Stat()
	if err != nil {
		return nil, cursor, false, errors.New(errmsg.Runtime("stat output file", err, map[string]any{
			"session_id":  sessionID,
			"cursor":      cursor,
			"max_bytes":   maxBytes,
			"output_path": outputPath,
		}))
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
			return nil, cursor, false, errors.New(errmsg.Runtime("read output chunk", err, map[string]any{
				"session_id":   sessionID,
				"output_path":  outputPath,
				"chunk_start":  chunkStart,
				"chunk_end":    chunkEnd,
				"chunk_length": length,
			}))
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
		return "", errmsg.Error("invalid session path", map[string]any{
			"session_id": sessionID,
			"base_path":  base,
			"dir_path":   dir,
		})
	}

	resolvedBase, err := resolvePathWithSymlinks(base)
	if err != nil {
		return "", errors.New(errmsg.Runtime("resolve sessions path", err, map[string]any{
			"session_id": sessionID,
			"base_path":  base,
		}))
	}
	resolvedDir, err := resolvePathWithSymlinks(dir)
	if err != nil {
		return "", errors.New(errmsg.Runtime("resolve session path", err, map[string]any{
			"session_id": sessionID,
			"dir_path":   dir,
		}))
	}
	if !isWithinPath(resolvedBase, resolvedDir) {
		return "", errmsg.Error("session path symlink escape", map[string]any{
			"session_id":    sessionID,
			"resolved_dir":  resolvedDir,
			"resolved_base": resolvedBase,
		})
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
		return "", errmsg.Error("invalid session file path", map[string]any{
			"session_id": sessionID,
			"file_name":  fileName,
			"file_path":  path,
		})
	}

	resolvedDir, err := resolvePathWithSymlinks(dir)
	if err != nil {
		return "", errors.New(errmsg.Runtime("resolve session directory path", err, map[string]any{
			"session_id": sessionID,
			"dir_path":   dir,
		}))
	}
	resolvedPath, err := resolvePathWithSymlinks(path)
	if err != nil {
		return "", errors.New(errmsg.Runtime("resolve session file path", err, map[string]any{
			"session_id": sessionID,
			"file_path":  path,
		}))
	}
	if !isWithinPath(resolvedDir, resolvedPath) {
		return "", errmsg.Error("session file symlink escape", map[string]any{
			"session_id":    sessionID,
			"file_path":     path,
			"resolved_path": resolvedPath,
			"resolved_dir":  resolvedDir,
		})
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
		return 0, 0, nil, errors.New(errmsg.Runtime("stat output file", err, map[string]any{
			"session_id":  sessionID,
			"output_path": outputPath,
		}))
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
		return nil, errors.New(errmsg.Runtime("scan index file", err, map[string]any{
			"session_id": sessionID,
			"index_path": indexPath,
		}))
	}
	return entries, nil
}

func validateSessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("%w: session id is empty; details: session_id=%s", ErrInvalidSessionID, errmsg.ValueSummary(sessionID))
	}
	if sessionID == "." {
		return fmt.Errorf("%w: session id contains invalid path segment alias; details: session_id=%s", ErrInvalidSessionID, errmsg.ValueSummary(sessionID))
	}
	if strings.Contains(sessionID, "..") {
		return fmt.Errorf("%w: session id contains invalid path segment; details: session_id=%s", ErrInvalidSessionID, errmsg.ValueSummary(sessionID))
	}
	if strings.ContainsAny(sessionID, `/\\`) {
		return fmt.Errorf("%w: session id contains path separator; details: session_id=%s", ErrInvalidSessionID, errmsg.ValueSummary(sessionID))
	}
	return nil
}

func resolvePathWithSymlinks(path string) (string, error) {
	return resolvePathWithSymlinksAtDepth(path, 0)
}

func resolvePathWithSymlinksAtDepth(path string, depth int) (string, error) {
	if depth > 64 {
		return "", errmsg.Error("resolve symlinks depth exceeded", map[string]any{
			"path":  path,
			"depth": depth,
		})
	}

	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return "", errors.New(errmsg.Runtime("resolve absolute path", err, map[string]any{
			"path": path,
		}))
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
			return "", errors.New(errmsg.Runtime("eval symlinks", err, map[string]any{
				"path":  current,
				"depth": depth,
			}))
		}

		// EvalSymlinks returns os.ErrNotExist for dangling symlink targets as well.
		// Detect that case and continue resolution from the symlink target.
		pathInfo, lstatErr := os.Lstat(current)
		if lstatErr == nil && pathInfo.Mode()&os.ModeSymlink != 0 {
			linkTarget, readlinkErr := os.Readlink(current)
			if readlinkErr != nil {
				return "", errors.New(errmsg.Runtime("read symlink", readlinkErr, map[string]any{
					"path":  current,
					"depth": depth,
				}))
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
			return "", errors.New(errmsg.Runtime("lstat path", lstatErr, map[string]any{
				"path":  current,
				"depth": depth,
			}))
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New(errmsg.Runtime("eval symlinks", err, map[string]any{
				"path":  current,
				"depth": depth,
			}))
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
		return errors.New(errmsg.Runtime("decode json file", err, map[string]any{
			"path": path,
		}))
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
		return errors.New(errmsg.Runtime("marshal json", err, map[string]any{
			"path": path,
		}))
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*.json")
	if err != nil {
		return errors.New(errmsg.Runtime("create temp file", err, map[string]any{
			"path": path,
			"dir":  dir,
		}))
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return errors.New(errmsg.Runtime("write temp file", err, map[string]any{
			"path":        path,
			"temp_path":   tmpPath,
			"payload_len": len(payload),
		}))
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		_ = os.Remove(tmpPath)
		return errors.New(errmsg.Runtime("chmod temp file", err, map[string]any{
			"path":      path,
			"temp_path": tmpPath,
		}))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return errors.New(errmsg.Runtime("close temp file", err, map[string]any{
			"path":      path,
			"temp_path": tmpPath,
		}))
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return errors.New(errmsg.Runtime("rename temp file", err, map[string]any{
			"path":      path,
			"temp_path": tmpPath,
		}))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return errors.New(errmsg.Runtime("chmod target file", err, map[string]any{
			"path": path,
		}))
	}
	return nil
}
