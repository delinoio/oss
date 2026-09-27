package domain

import (
	"io/fs"
	"strings"
	"unicode/utf8"
)

type WorkspaceReadOperation string

const (
	WorkspaceRoots          WorkspaceReadOperation = "roots"
	WorkspaceDirectory      WorkspaceReadOperation = "directory"
	WorkspaceFile           WorkspaceReadOperation = "file"
	WorkspaceGitDiff        WorkspaceReadOperation = "git-diff"
	WorkspacePageSize                              = 100
	WorkspaceDirectoryLimit                        = 10000
	WorkspacePreviewLimit                          = 64 << 10
)

type WorkspaceReadQuery struct {
	Operation    WorkspaceReadOperation  `json:"operation"`
	RepositoryID ID                      `json:"repository_id,omitempty"`
	Path         string                  `json:"path,omitempty"`
	PageToken    string                  `json:"page_token,omitempty"`
	Comparison   WorkspaceDiffComparison `json:"comparison,omitempty"`
}

// A single portable namespace prevents alternate streams and platform-specific
// normalization from selecting a different file than the client displays.
func WorkspacePath(value string) bool {
	if !utf8.ValidString(value) || len(value) > 4096 || strings.Count(value, "/") >= 64 || !fs.ValidPath(value) || strings.ContainsAny(value, "\\:\x00<>\"|?*") {
		return false
	}
	for _, r := range value {
		if r < 32 {
			return false
		}
	}
	if value == "." {
		return true
	}
	for _, part := range strings.Split(value, "/") {
		if len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base := strings.ToUpper(strings.TrimRight(strings.SplitN(part, ".", 2)[0], " "))
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') || base == "COM¹" || base == "COM²" || base == "COM³" || base == "LPT¹" || base == "LPT²" || base == "LPT³" {
			return false
		}
	}
	return true
}

func (q WorkspaceReadQuery) Validate() error {
	valid := (q.RepositoryID == "" || q.RepositoryID.Validate() == nil) && (q.Operation == WorkspaceGitDiff || q.Comparison == "")
	switch q.Operation {
	case WorkspaceRoots:
		valid = valid && q.RepositoryID == "" && q.Path == "" && q.PageToken == ""
	case WorkspaceDirectory:
		valid = valid && WorkspacePath(q.Path) && len(q.PageToken) <= 256
	case WorkspaceFile:
		valid = valid && q.Path != "." && WorkspacePath(q.Path) && q.PageToken == ""
	case WorkspaceGitDiff:
		valid = valid && q.RepositoryID.Validate() == nil && WorkspacePath(q.Path) && q.PageToken == "" && q.Comparison.Valid()
	default:
		valid = false
	}
	if !valid {
		return Fail(InvalidArgument, "Invalid workspace file query.", "Select a prepared repository and a canonical relative path.")
	}
	return nil
}

type WorkspaceEntryKind string

const (
	WorkspaceEntryDirectory WorkspaceEntryKind = "directory"
	WorkspaceEntryFile      WorkspaceEntryKind = "file"
	WorkspaceEntryLink      WorkspaceEntryKind = "link"
	WorkspaceEntryOther     WorkspaceEntryKind = "other"
)

type WorkspaceRoot struct {
	RepositoryID ID     `json:"repository_id,omitempty"`
	Name         string `json:"name"`
	Primary      bool   `json:"primary"`
}
type WorkspaceEntry struct {
	Name       string             `json:"name"`
	Kind       WorkspaceEntryKind `json:"kind"`
	Size       int64              `json:"size,string"`
	ModifiedAt string             `json:"modified_at"`
}
type WorkspaceReadResult struct {
	Roots         []WorkspaceRoot  `json:"roots,omitempty"`
	Entries       []WorkspaceEntry `json:"entries,omitempty"`
	NextPageToken string           `json:"next_page_token,omitempty"`
	Text          string           `json:"text,omitempty"`
	Size          int64            `json:"size,string"`
	Binary        bool             `json:"binary"`
	Truncated     bool             `json:"truncated"`
	Diff          *WorkspaceDiff   `json:"diff,omitempty"`
}

// Validate every observation before crossing the Worker-to-client boundary.
func (r WorkspaceReadResult) Validate(q WorkspaceReadQuery) error {
	valid := q.Validate() == nil && len(r.Text) <= WorkspacePreviewLimit && utf8.ValidString(r.Text) && r.Size >= 0 && len(r.NextPageToken) <= 256
	if q.Operation == WorkspaceGitDiff {
		valid = valid && r.Diff != nil && r.Diff.Validate(q) == nil && len(r.Roots) == 0 && len(r.Entries) == 0 && r.NextPageToken == "" && r.Text == "" && r.Size == 0 && !r.Binary && !r.Truncated
	} else if r.Diff != nil {
		valid = false
	} else if q.Operation == WorkspaceFile {
		valid = valid && len(r.Roots) == 0 && len(r.Entries) == 0 && r.NextPageToken == "" && (!r.Binary || r.Text == "") && !strings.ContainsRune(r.Text, 0) && int64(len(r.Text)) <= r.Size && (!r.Truncated || r.Size > WorkspacePreviewLimit) && (r.Binary || r.Truncated || int64(len(r.Text)) == r.Size)
	} else if q.Operation == WorkspaceDirectory {
		valid = valid && len(r.Roots) == 0 && len(r.Entries) <= WorkspacePageSize && r.Text == "" && r.Size == 0 && !r.Binary && !r.Truncated
		last := ""
		for _, entry := range r.Entries {
			valid = valid && WorkspacePath(entry.Name) && entry.Name != "." && !strings.Contains(entry.Name, "/") && entry.Name > last && entry.Size >= 0 && len(entry.ModifiedAt) <= 40
			switch entry.Kind {
			case WorkspaceEntryDirectory, WorkspaceEntryFile, WorkspaceEntryLink, WorkspaceEntryOther:
			default:
				valid = false
			}
			last = entry.Name
		}
	} else {
		// Root results are constructed from accepted server metadata, never
		// supplied by the Worker observation channel.
		valid = false
	}
	if !valid {
		return Fail(Unavailable, "The workspace observation is invalid.", "Refresh the current workspace view.")
	}
	return nil
}
