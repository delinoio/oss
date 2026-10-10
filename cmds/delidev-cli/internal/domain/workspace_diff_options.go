package domain

import "encoding/json"

// SameReference compares semantic reference identity without pointer identity.
func SameReference(a, b *Reference) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

type DiffBaseChoice struct {
	Reference  Reference `json:"reference"`
	Configured bool      `json:"configured"`
}
type WorkspaceDiffOptions struct {
	Version          int              `json:"version"`
	RepositoryID     ID               `json:"repository_id"`
	Path             string           `json:"path"`
	Choices          []DiffBaseChoice `json:"choices"`
	Default          *Reference       `json:"default,omitempty"`
	DefaultAvailable bool             `json:"default_available"`
}

func (v WorkspaceDiffOptions) Validate(q WorkspaceReadQuery) error {
	valid := q.Operation == WorkspaceGitDiffOptions && q.Validate() == nil && v.Version == 1 && v.RepositoryID == q.RepositoryID && v.Path == q.Path && v.Choices != nil && len(v.Choices) <= 1001
	seen := map[Reference]bool{}
	size, configured := 0, 0
	for _, choice := range v.Choices {
		ref := choice.Reference
		size += len(ref.Name) + len(ref.Remote) + len(ref.Type)
		valid = valid && ref.Validate(false) == nil && !seen[ref]
		seen[ref] = true
		if choice.Configured {
			configured++
		} else {
			valid = valid && (ref.Type == LocalBranch || ref.Type == RemoteBranch)
		}
	}
	valid = valid && size <= 128<<10 && configured <= 1 && len(v.Choices)-configured <= 1000
	valid = valid && (!v.DefaultAvailable || v.Default != nil) && (v.Default == nil || v.Default.Validate(false) == nil && seen[*v.Default])
	encoded, _ := json.Marshal(v)
	valid = valid && len(encoded) <= 128<<10
	if !valid {
		return Fail(Unavailable, "The Git comparison options are invalid.", "Refresh the selected repository options.")
	}
	return nil
}
