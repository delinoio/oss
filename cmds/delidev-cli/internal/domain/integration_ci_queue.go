package domain

import "strconv"

const MaxCIMergeQueueEntries = 500

type CIMergeQueueStrategy string

const (
	CIQueueAllGreen        CIMergeQueueStrategy = "ALLGREEN"
	CIQueueHeadGreen       CIMergeQueueStrategy = "HEADGREEN"
	CIQueueUnknownStrategy CIMergeQueueStrategy = "unknown"
)

// Queue identity and ordered membership are retained separately from check
// results. A queue state or timeout never supplies a failed native check.
type CIMergeQueueEntry struct {
	NodeID            string `json:"node_id"`
	PullRequestNodeID string `json:"pull_request_node_id"`
	PullRequestNumber string `json:"pull_request_number"`
	Position          string `json:"position"`
	BaseSHA           string `json:"base_sha,omitempty"`
	HeadSHA           string `json:"head_sha,omitempty"`
	State             string `json:"state"`
}

type CIMergeQueue struct {
	NodeID           string               `json:"node_id"`
	RepositoryNodeID string               `json:"repository_node_id"`
	Strategy         CIMergeQueueStrategy `json:"strategy"`
	Entry            CIMergeQueueEntry    `json:"entry"`
	TotalCount       string               `json:"total_count"`
	Entries          []CIMergeQueueEntry  `json:"entries"`
	Rollup           *CIRollup            `json:"rollup,omitempty"`
}

func (v CIMergeQueueEntry) Validate() error {
	_, positionErr := strconv.ParseUint(v.Position, 10, 31)
	if Text(v.NodeID, "queue entry identity", 256, true) != nil || Text(v.PullRequestNodeID, "queue PR identity", 256, true) != nil || !PositiveDecimal(v.PullRequestNumber) || !unsignedDecimal(v.Position) || positionErr != nil || Text(v.State, "queue entry state", 64, true) != nil || v.BaseSHA != "" && !repositorySHA(v.BaseSHA) || v.HeadSHA != "" && !repositorySHA(v.HeadSHA) {
		return invalidPRObservation()
	}
	return nil
}

func (v CIMergeQueue) Validate(item RepositoryItem) error {
	if Text(v.NodeID, "queue identity", 256, true) != nil || Text(v.RepositoryNodeID, "queue repository", 256, true) != nil || v.Entry.Validate() != nil || v.Entry.PullRequestNodeID != item.NodeID || v.Entry.PullRequestNumber != item.Number || v.Entries == nil || len(v.Entries) > MaxCIMergeQueueEntries || v.TotalCount != strconv.Itoa(len(v.Entries)) {
		return invalidPRObservation()
	}
	switch v.Strategy {
	case CIQueueAllGreen, CIQueueHeadGreen, CIQueueUnknownStrategy:
	default:
		return invalidPRObservation()
	}
	seen, prs := map[string]bool{}, map[string]bool{}
	found := false
	var previous uint64
	for i, entry := range v.Entries {
		position, _ := strconv.ParseUint(entry.Position, 10, 31)
		if entry.Validate() != nil || i > 0 && position <= previous || seen[entry.NodeID] || prs[entry.PullRequestNodeID] {
			return invalidPRObservation()
		}
		previous = position
		seen[entry.NodeID], prs[entry.PullRequestNodeID] = true, true
		if entry.NodeID == v.Entry.NodeID {
			if entry != v.Entry {
				return invalidPRObservation()
			}
			found = true
		}
	}
	if !found || v.Rollup != nil && v.Rollup.Validate(v.Entry.HeadSHA) != nil {
		return invalidPRObservation()
	}
	return nil
}

func (v CIMergeQueue) evaluable(item RepositoryItem) bool {
	return v.Validate(item) == nil && v.Strategy == CIQueueAllGreen && v.Entry.BaseSHA != "" && v.Entry.HeadSHA != "" && v.Entry.HeadSHA != item.HeadSHA && v.Entry.HeadSHA != item.BaseSHA && v.Entry.HeadSHA != v.Entry.BaseSHA && v.Rollup != nil
}
