package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type ReadRequest struct {
	PRCandidate *domain.PRGitTarget       `json:"pr_candidate,omitempty"`
	ID          domain.ID                 `json:"id"`
	Deadline    time.Time                 `json:"deadline"`
	Preparation PrepareRequest            `json:"preparation"`
	Manifest    Manifest                  `json:"manifest"`
	Query       domain.WorkspaceReadQuery `json:"query"`
}

func readFailure() error {
	return domain.Fail(domain.Unavailable, "The workspace entry could not be read.", "Refresh the directory and check access on the execution machine.")
}
func readUnsupported() error {
	return domain.Fail(domain.Unsupported, "The entry is not a supported portable directory or regular file.", "Select a regular file or directory inside this workspace.")
}
func readChanged() error {
	return domain.Fail(domain.Conflict, "The workspace entry changed while it was read.", "Refresh the file or directory.")
}

// Root confines escapes but permits internal links. Open each component from
// the preceding anchored directory and compare its identity with both lstat
// observations before any bytes or directory children can be read.
func openVerifiedChildRoot(parent *os.Root, name string, expected os.FileInfo) (*os.Root, error) {
	if expected.Mode()&os.ModeSymlink != 0 || !expected.IsDir() {
		return nil, readUnsupported()
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, readFailure()
	}
	opened, err := child.Stat(".")
	if err != nil || !os.SameFile(expected, opened) {
		child.Close()
		return nil, readChanged()
	}
	current, err := parent.Lstat(name)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, current) {
		child.Close()
		return nil, readChanged()
	}
	return child, nil
}

func openVerifiedEntry(parent *os.Root, name string, expected os.FileInfo) (*os.File, error) {
	if expected.Mode()&os.ModeSymlink != 0 || (!expected.IsDir() && !expected.Mode().IsRegular()) {
		return nil, readUnsupported()
	}
	file, err := openWorkspaceEntry(parent, name)
	if err != nil {
		return nil, readFailure()
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(expected, opened) {
		file.Close()
		return nil, readChanged()
	}
	current, err := parent.Lstat(name)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(expected, current) {
		file.Close()
		return nil, readChanged()
	}
	return file, nil
}

// ReadWorkspace observes files without taking or rewriting native execution
// ownership. Its Git children use a distinct owner, including during a live run.
func (m *Manager) ReadWorkspace(ctx context.Context, request ReadRequest) (domain.WorkspaceReadResult, error) {
	var result domain.WorkspaceReadResult
	if request.PRCandidate != nil || request.Query.Validate() != nil || request.Query.Operation == domain.WorkspaceRoots {
		return result, readUnsupported()
	}
	err := m.observeWorkspace(ctx, request, request.Query.RepositoryID, request.Query.Operation == domain.WorkspaceGitDiff, func(ctx context.Context, git Git, retained Manifest, root *os.Root, _ string) error {
		var err error
		if request.Query.Operation == domain.WorkspaceGitDiff {
			result, err = git.readDiff(ctx, request, retained)
		} else {
			result, err = readRoot(ctx, root, request)
		}
		return err
	})
	if err != nil {
		return domain.WorkspaceReadResult{}, err
	}
	return result, result.Validate(request.Query)
}

// File views and private PR matching share original manifest, filesystem anchor
// and independently cleaned read-process ownership. Neither takes an execution
// lease or replaces a native startup/continuation check.
func (m *Manager) observeWorkspace(ctx context.Context, request ReadRequest, repositoryID domain.ID, verifyAfter bool, observe func(context.Context, Git, Manifest, *os.Root, string) error) (returned error) {
	if request.ID.Validate() != nil || request.Preparation.validate() != nil {
		return readUnsupported()
	}
	if request.Deadline.IsZero() || time.Until(request.Deadline) <= 0 || time.Until(request.Deadline) > 16*time.Second {
		return readFailure()
	}
	ctx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	if security.CheckPrivateDir(m.Root) != nil || m.initialize() != nil {
		return ResultUncertain()
	}
	defer func() {
		if returned != nil {
			m.Logger.WarnContext(ctx, "workspace_read_failed", "read_id", request.ID, "session_id", request.Preparation.SessionID, "operation", request.Query.Operation, "pr_candidate", request.PRCandidate != nil, "comparison", request.Query.Comparison, "code", domain.SafeError(returned).Code)
		}
	}()
	observationLock, err := m.lockWorkspaceObservations(ctx, request.Preparation.SessionID)
	if err != nil {
		return err
	}
	defer observationLock.Close()
	if _, err := os.Lstat(filepath.Join(m.Root, "session-deletions", string(request.Preparation.SessionID)+".json")); !errors.Is(err, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	retained, err := m.Read(request.Preparation.SessionID)
	actual, _ := json.Marshal(retained)
	expected, _ := json.Marshal(request.Manifest)
	if err != nil || !bytes.Equal(actual, expected) || retained.State != Ready {
		return ResultUncertain()
	}
	selected := ""
	if retained.Type == domain.GeneralChat && repositoryID == "" {
		selected = retained.PrimaryPath
	}
	for _, repo := range retained.Repositories {
		if repo.ID == repositoryID {
			selected = repo.Path
		}
	}
	if selected == "" {
		return readUnsupported()
	}
	before, err := os.Lstat(selected)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return ResultUncertain()
	}
	root, err := os.OpenRoot(selected)
	if err != nil {
		return readFailure()
	}
	defer root.Close()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(before, anchored) {
		return ResultUncertain()
	}
	// Continuation identity allows commits and dirty files without requiring a
	// closed native claim. This observation grants no execution/recovery rights.
	inspection := &Manager{Root: m.Root, Git: m.Git, Logger: m.Logger}
	inspection.Git.readOnly = true
	inspection.Git.ProcessRoot = m.readProcessRoot(request.Preparation.SessionID)
	if len(retained.Repositories) > 0 {
		for _, directory := range []string{filepath.Join(m.Root, "workspace-read-processes"), filepath.Join(m.Root, "workspace-read-processes-v2"), inspection.Git.ProcessRoot} {
			if err := security.PrivateDir(directory); err != nil {
				return ResultUncertain()
			}
		}
		owner := filepath.Join(inspection.Git.ProcessRoot, string(request.ID))
		if err := os.Mkdir(owner, 0700); err != nil {
			return ResultUncertain()
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			// Delete only an empty, fully reconciled read-only process index.
			// Unknown children retain their private journals for recovery.
			if process.ReconcileOwnerContext(cleanup, inspection.Git.ProcessRoot, request.ID) != nil {
				returned = ResultUncertain()
				return
			}
			if err := os.Remove(owner + ".recovery.lock"); err != nil {
				returned = ResultUncertain()
				return
			}
			// Keep the owner index until the adjacent recovery lock has been
			// retired. This ordering leaves a recoverable owner directory if the
			// Worker stops between the two durable namespace changes.
			if err := os.Remove(owner); err != nil {
				returned = ResultUncertain()
				return
			}
			if err := os.Remove(inspection.Git.ProcessRoot); err != nil || security.SyncParent(inspection.Git.ProcessRoot) != nil {
				returned = ResultUncertain()
			}
		}()
	}
	if _, err := inspection.verifyWorkspaceIdentityForOwner(ctx, request.Preparation, retained, continuationIdentity, request.ID); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	git := inspection.Git
	git.OwnerID = request.ID
	if err := observe(ctx, git, retained, root, selected); err != nil {
		return err
	}
	after, err := os.Lstat(selected)
	if err != nil || !os.SameFile(before, after) || after.Mode()&os.ModeSymlink != 0 {
		return ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if verifyAfter {
		if _, err := inspection.verifyWorkspaceIdentityForOwner(ctx, request.Preparation, retained, continuationIdentity, request.ID); err != nil {
			return err
		}
	}
	return nil
}

func readRoot(ctx context.Context, root *os.Root, request ReadRequest) (domain.WorkspaceReadResult, error) {
	var result domain.WorkspaceReadResult
	query := request.Query
	components := strings.Split(query.Path, "/")
	parent := root
	for _, component := range components[:len(components)-1] {
		info, err := parent.Lstat(component)
		if err != nil {
			return result, readFailure()
		}
		child, err := openVerifiedChildRoot(parent, component, info)
		if err != nil {
			return result, err
		}
		defer child.Close()
		parent = child
	}
	name := components[len(components)-1]
	expected, err := parent.Lstat(name)
	if err != nil {
		return result, readFailure()
	}
	file, err := openVerifiedEntry(parent, name, expected)
	if err != nil {
		return result, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return result, readFailure()
	}
	if query.Operation == domain.WorkspaceFile {
		if !info.Mode().IsRegular() {
			return result, readUnsupported()
		}
		raw, err := io.ReadAll(io.LimitReader(file, domain.WorkspacePreviewLimit+1))
		if err != nil {
			return result, readFailure()
		}
		fresh, err := file.Stat()
		if err != nil || fresh.Size() != info.Size() || !fresh.ModTime().Equal(info.ModTime()) || !os.SameFile(info, fresh) || int64(len(raw)) > info.Size() {
			return result, domain.Fail(domain.Conflict, "The file changed while it was read.", "Refresh the file preview.")
		}
		result.Size, result.Truncated = info.Size(), len(raw) > domain.WorkspacePreviewLimit
		if result.Truncated {
			raw = raw[:domain.WorkspacePreviewLimit]
			// An otherwise valid UTF-8 preview can end inside its final rune.
			for n := 0; n < utf8.UTFMax-1 && len(raw) > 0 && !utf8.Valid(raw); n++ {
				start := len(raw) - 1
				for start > 0 && !utf8.RuneStart(raw[start]) {
					start--
				}
				if utf8.FullRune(raw[start:]) {
					break
				}
				raw = raw[:start]
			}
		}
		result.Binary = !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0
		if !result.Binary {
			result.Text = string(raw)
		}
		return result, nil
	}
	if !info.IsDir() {
		return result, readUnsupported()
	}
	// ReadDir and child Lstat must observe the same anchored directory even if
	// its name is replaced after the file handle was opened.
	directory, err := openVerifiedChildRoot(parent, name, info)
	if err != nil {
		return result, err
	}
	defer directory.Close()
	entries := make([]domain.WorkspaceEntry, 0)
	for {
		if err := ctx.Err(); err != nil {
			return result, domain.SafeError(err)
		}
		batch, err := file.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return result, readFailure()
		}
		if len(entries)+len(batch) > domain.WorkspaceDirectoryLimit {
			return result, domain.Fail(domain.ResourceExhausted, "The directory exceeds the 10,000-entry limit.", "Select a smaller subdirectory by its relative path.")
		}
		for _, entry := range batch {
			if !domain.WorkspacePath(entry.Name()) || strings.Contains(entry.Name(), "/") {
				return result, readUnsupported()
			}
			stat, err := directory.Lstat(entry.Name())
			if err != nil {
				return result, readFailure()
			}
			kind := domain.WorkspaceEntryOther
			switch {
			case stat.Mode()&os.ModeSymlink != 0:
				kind = domain.WorkspaceEntryLink
			case stat.IsDir():
				kind = domain.WorkspaceEntryDirectory
			case stat.Mode().IsRegular():
				kind = domain.WorkspaceEntryFile
			}
			entries = append(entries, domain.WorkspaceEntry{Name: entry.Name(), Kind: kind, Size: stat.Size(), ModifiedAt: stat.ModTime().UTC().Format(time.RFC3339Nano)})
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	slices.SortFunc(entries, func(a, b domain.WorkspaceEntry) int { return strings.Compare(a.Name, b.Name) })
	scope, _ := json.Marshal(struct {
		Session    domain.ID
		Repository domain.ID
		Path       string
		Entries    []domain.WorkspaceEntry
	}{request.Preparation.SessionID, query.RepositoryID, query.Path, entries})
	digest := sha256.Sum256(scope)
	type cursor struct {
		Digest string `json:"digest"`
		Offset int    `json:"offset"`
	}
	page := cursor{Digest: hex.EncodeToString(digest[:])}
	if query.PageToken != "" {
		raw, err := base64.RawURLEncoding.DecodeString(query.PageToken)
		var previous cursor
		if err != nil || domain.Decode(raw, &previous) != nil || previous.Digest != page.Digest || previous.Offset <= 0 || previous.Offset >= len(entries) || previous.Offset%domain.WorkspacePageSize != 0 {
			return result, domain.Fail(domain.Conflict, "The directory page is stale or invalid.", "Refresh the directory from its first page.")
		}
		page.Offset = previous.Offset
	}
	end := min(page.Offset+domain.WorkspacePageSize, len(entries))
	result.Entries = entries[page.Offset:end]
	if end < len(entries) {
		page.Offset = end
		raw, _ := json.Marshal(page)
		result.NextPageToken = base64.RawURLEncoding.EncodeToString(raw)
	}
	return result, nil
}
