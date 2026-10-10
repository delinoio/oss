package domain

import "encoding/json"

const WorkspaceDiffReferenceLimit = 1000
const WorkspaceDiffOptionsLimit = 128 << 10

type WorkspaceDiffChoice struct {
	Reference  Reference `json:"reference"`
	Available  bool      `json:"available"`
	Configured bool      `json:"configured"`
}

type WorkspaceDiffOptions struct {
	Version      uint32                `json:"version"`
	RepositoryID ID                    `json:"repository_id"`
	Choices      []WorkspaceDiffChoice `json:"choices"`
	Default      Reference             `json:"default,omitzero"`
}

func (v WorkspaceDiffOptions) Validate(q WorkspaceReadQuery) error {
	raw, err := json.Marshal(v)
	valid := err == nil && q.Operation == WorkspaceGitDiffOptions && q.Validate() == nil && v.Version == 1 && v.RepositoryID == q.RepositoryID && v.Choices != nil && len(v.Choices) <= WorkspaceDiffReferenceLimit+1 && len(raw) <= WorkspaceDiffOptionsLimit
	seen := map[Reference]bool{}
	native, configured, found := 0, 0, v.Default == (Reference{})
	for _, choice := range v.Choices {
		ref := choice.Reference
		valid = valid && ref.Validate(false) == nil && !seen[ref] && (choice.Configured || ref.Type != CommitReference)
		seen[ref] = true
		if choice.Configured {
			configured++
		} else {
			native++
		}
		if ref == v.Default && choice.Available {
			found = true
		}
	}
	valid = valid && native <= WorkspaceDiffReferenceLimit && configured <= 1 && found
	if !valid {
		return Fail(Unavailable, "The local Git comparison options are invalid.", "Refresh the prepared repository; no partial inventory is available.")
	}
	return nil
}
