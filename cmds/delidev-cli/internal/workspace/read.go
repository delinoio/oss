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

// ReadWorkspace observes files without taking or rewriting native execution
// ownership. Its Git children use a distinct owner, including during a live run.
func (m *Manager) ReadWorkspace(ctx context.Context, request ReadRequest) (result domain.WorkspaceReadResult, returned error) {
	if request.ID.Validate() != nil || request.Preparation.validate() != nil || request.Query.Validate() != nil || request.Query.Operation == domain.WorkspaceRoots {
		return result, readUnsupported()
	}
	if request.Deadline.IsZero() || time.Until(request.Deadline) <= 0 || time.Until(request.Deadline) > 16*time.Second {
		return result, readFailure()
	}
	ctx, cancel := context.WithDeadline(ctx, request.Deadline)
	defer cancel()
	if security.CheckPrivateDir(m.Root) != nil || m.initialize() != nil {
		return result, ResultUncertain()
	}
	defer func() {
		if returned != nil {
			m.Logger.WarnContext(ctx, "workspace_read_failed", "read_id", request.ID, "session_id", request.Preparation.SessionID, "operation", request.Query.Operation, "comparison", request.Query.Comparison, "code", domain.SafeError(returned).Code)
		}
	}()
	retained, err := m.Read(request.Preparation.SessionID)
	actual, _ := json.Marshal(retained)
	expected, _ := json.Marshal(request.Manifest)
	if err != nil || !bytes.Equal(actual, expected) || retained.State != Ready {
		return result, ResultUncertain()
	}
	selected := ""
	if retained.Type == domain.GeneralChat && request.Query.RepositoryID == "" {
		selected = retained.PrimaryPath
	}
	for _, repo := range retained.Repositories {
		if repo.ID == request.Query.RepositoryID {
			selected = repo.Path
		}
	}
	if selected == "" {
		return result, readUnsupported()
	}
	before, err := os.Lstat(selected)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return result, ResultUncertain()
	}
	root, err := os.OpenRoot(selected)
	if err != nil {
		return result, readFailure()
	}
	defer root.Close()
	anchored, err := root.Stat(".")
	if err != nil || !os.SameFile(before, anchored) {
		return result, ResultUncertain()
	}
	// Continuation identity allows commits and dirty files without requiring a
	// closed native claim. This observation grants no execution/recovery rights.
	inspection := &Manager{Root: m.Root, Git: m.Git}
	inspection.Git.noFSMonitor = true
	inspection.Git.ProcessRoot = filepath.Join(m.Root, "workspace-read-processes")
	if len(retained.Repositories) > 0 {
		if err := security.PrivateDir(inspection.Git.ProcessRoot); err != nil {
			return result, ResultUncertain()
		}
		owner := filepath.Join(inspection.Git.ProcessRoot, string(request.ID))
		if err := os.Mkdir(owner, 0700); err != nil {
			return result, ResultUncertain()
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer stop()
			// Delete only an empty, fully reconciled read-only process index.
			// Unknown children retain their private journals for recovery.
			if process.ReconcileOwnerContext(cleanup, inspection.Git.ProcessRoot, request.ID) != nil || os.Remove(owner) != nil {
				result, returned = domain.WorkspaceReadResult{}, ResultUncertain()
				return
			}
			if err := os.Remove(owner + ".recovery.lock"); err != nil {
				result, returned = domain.WorkspaceReadResult{}, ResultUncertain()
			}
		}()
	}
	if _, err := inspection.verifyWorkspaceIdentityForOwner(ctx, request.Preparation, retained, continuationIdentity, request.ID); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	if request.Query.Operation == domain.WorkspaceGitDiff {
		git := inspection.Git
		git.OwnerID = request.ID
		result, err = git.readDiff(ctx, request, retained)
	} else {
		result, err = readRoot(ctx, root, request)
	}
	if err != nil {
		return result, err
	}
	after, err := os.Lstat(selected)
	if err != nil || !os.SameFile(before, after) || after.Mode()&os.ModeSymlink != 0 {
		return domain.WorkspaceReadResult{}, ResultUncertain()
	}
	if err := ctx.Err(); err != nil {
		return domain.WorkspaceReadResult{}, domain.SafeError(err)
	}
	if request.Query.Operation == domain.WorkspaceGitDiff {
		if _, err := inspection.verifyWorkspaceIdentityForOwner(ctx, request.Preparation, retained, continuationIdentity, request.ID); err != nil {
			return domain.WorkspaceReadResult{}, err
		}
	}
	return result, result.Validate(request.Query)
}

func readRoot(ctx context.Context, root *os.Root, request ReadRequest) (domain.WorkspaceReadResult, error) {
	var result domain.WorkspaceReadResult
	query := request.Query
	// Do not present links as directories or let an explicit path silently
	// navigate through one. os.Root independently confines replacement races.
	part := ""
	for _, component := range strings.Split(query.Path, "/") {
		part = filepath.Join(part, component)
		info, err := root.Lstat(part)
		if err != nil {
			return result, readFailure()
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return result, readUnsupported()
		}
	}
	file, err := openWorkspaceEntry(root, filepath.FromSlash(query.Path))
	if err != nil {
		return result, readFailure()
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
			stat, err := root.Lstat(filepath.Join(filepath.FromSlash(query.Path), entry.Name()))
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
