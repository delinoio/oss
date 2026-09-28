package domain

type ReviewSelection struct {
	Path  string           `json:"path"`
	Kind  ReviewAnchorKind `json:"kind"`
	Side  ReviewSide       `json:"side,omitempty"`
	Start uint32           `json:"start,omitempty"`
	End   uint32           `json:"end,omitempty"`
}

// ReviewAnchor retains what the author reviewed. A later observation never
// rewrites this context or silently moves it to an apparently similar line.
type ReviewAnchor struct {
	RepositoryID ID                      `json:"repository_id"`
	Comparison   WorkspaceDiffComparison `json:"comparison"`
	QueryPath    string                  `json:"query_path"`
	DiffRevision string                  `json:"diff_revision"`
	Selection    ReviewSelection         `json:"selection"`
	FileDigest   string                  `json:"file_digest"`
	Context      string                  `json:"context"`
}

func (s ReviewSelection) Validate() error {
	valid := WorkspacePath(s.Path) && s.Path != "."
	switch s.Kind {
	case ReviewFileAnchor:
		valid = valid && s.Side == "" && s.Start == 0 && s.End == 0
	case ReviewLineAnchor:
		valid = valid && (s.Side == ReviewOldSide || s.Side == ReviewNewSide) && s.Start > 0 && s.End >= s.Start && s.End-s.Start < 20
	default:
		valid = false
	}
	if !valid {
		return Fail(InvalidArgument, "Invalid local review location.", "Select a changed file or at most 20 visible lines on one side of its diff.")
	}
	return nil
}

func (v WorkspaceDiff) ReviewAnchor(selection ReviewSelection) (ReviewAnchor, error) {
	if err := selection.Validate(); err != nil {
		return ReviewAnchor{}, err
	}
	files, err := v.ReviewFiles()
	if err != nil {
		return ReviewAnchor{}, err
	}
	anchor := ReviewAnchor{RepositoryID: v.RepositoryID, Comparison: v.Comparison, QueryPath: v.Path, DiffRevision: v.Revision, Selection: selection}
	for _, file := range files {
		if file.Path != selection.Path {
			continue
		}
		anchor.FileDigest = file.Digest
		if selection.Kind == ReviewFileAnchor {
			return anchor, nil
		}
		if file.Kind != ReviewTextFile {
			break
		}
		var hunk, count uint32
		for _, line := range file.Lines {
			number := line.Old
			if selection.Side == ReviewNewSide {
				number = line.New
			}
			if number < selection.Start || number > selection.End {
				continue
			}
			if number != selection.Start+count || (hunk != 0 && hunk != line.Hunk) {
				return ReviewAnchor{}, reviewUnavailable()
			}
			hunk = line.Hunk
			count++
			anchor.Context += line.Text
			if line.Newline {
				anchor.Context += "\n"
			}
			if len(anchor.Context) > 8192 {
				return ReviewAnchor{}, Fail(ResourceExhausted, "The selected review context exceeds 8 KiB.", "Select fewer or shorter lines.")
			}
		}
		if count == selection.End-selection.Start+1 {
			return anchor, nil
		}
		break
	}
	return ReviewAnchor{}, Fail(Conflict, "The selected review location is absent or ambiguous.", "Refresh the diff and select a visible file or text line; binary and non-line changes support file comments only.")
}

func (a ReviewAnchor) Matches(v WorkspaceDiff) bool {
	if a.RepositoryID != v.RepositoryID || a.Comparison != v.Comparison || a.QueryPath != v.Path || a.DiffRevision != v.Revision {
		return false
	}
	current, err := v.ReviewAnchor(a.Selection)
	return err == nil && current == a
}
