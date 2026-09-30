package github

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ciBinding struct {
	mergeability string
	inMergeQueue bool
	mergeSHA     string
}

func parseCINode(node map[string]json.RawMessage, repository domain.RemoteRepository, item domain.RepositoryItem) (ciBinding, ciPage, *ciPage, error) {
	var binding ciBinding
	empty := ciPage{}
	repo, repoOK := jsonObject(node["repository"])
	number, numberOK := exactUnsigned(node["number"])
	merged, mergedOK := nullableBool(node, "merged")
	queued, queueOK := nullableBool(node, "isInMergeQueue")
	state := "OPEN"
	if item.State == domain.RepositoryItemClosed {
		state = "CLOSED"
	}
	if item.Merged != nil && *item.Merged {
		state = "MERGED"
	}
	if !repoOK || stringField(repo, "id") != repository.NodeID || !numberOK || strconv.FormatUint(number, 10) != item.Number || stringField(node, "id") != item.NodeID || stringField(node, "baseRefName") != item.BaseRef || stringField(node, "baseRefOid") != item.BaseSHA || stringField(node, "headRefName") != item.HeadRef || stringField(node, "headRefOid") != item.HeadSHA || stringField(node, "state") != state || !mergedOK || merged == nil || item.Merged == nil || *merged != *item.Merged || !queueOK || queued == nil {
		return binding, empty, nil, domain.Fail(domain.Conflict, "The PR changed while reading CI evidence.", "Refresh the current PR; the earlier observation was not published.")
	}
	binding.mergeability, binding.inMergeQueue = stringField(node, "mergeable"), *queued
	switch binding.mergeability {
	case "MERGEABLE", "CONFLICTING", "UNKNOWN":
	default:
		return binding, empty, nil, queryUnavailable()
	}
	if item.Mergeable != nil && (binding.mergeability == "UNKNOWN" || *item.Mergeable != (binding.mergeability == "MERGEABLE")) {
		return binding, empty, nil, queryUnavailable()
	}
	head, err := parseCIRollup(node["statusCheckRollup"], repository, item.HeadSHA)
	if err != nil {
		return binding, empty, nil, err
	}
	raw, exists := node["potentialMergeCommit"]
	if !exists {
		return binding, empty, nil, queryUnavailable()
	}
	if string(raw) == "null" {
		return binding, head, nil, nil
	}
	merge, ok := jsonObject(raw)
	binding.mergeSHA = stringField(merge, "oid")
	parents, parentsOK := jsonObject(merge["parents"])
	count, countOK := exactUnsigned(parents["totalCount"])
	var rows []map[string]json.RawMessage
	if !ok || !commitSHA(binding.mergeSHA) || binding.mergeSHA == item.HeadSHA || !parentsOK || !countOK || count != 2 || domain.Decode(parents["nodes"], &rows) != nil || len(rows) != 2 || stringField(rows[0], "oid") != item.BaseSHA || stringField(rows[1], "oid") != item.HeadSHA {
		return binding, empty, nil, domain.Fail(domain.Unavailable, "The PR test merge commit could not be bound to its current base and head.", "Refresh after GitHub recomputes mergeability; no head fallback authorizes CI remediation.")
	}
	page, err := parseCIRollup(merge["statusCheckRollup"], repository, binding.mergeSHA)
	return binding, head, &page, err
}

type ciAccumulator struct {
	rollup      domain.CIRollup
	total       uint64
	initialized bool
	done        bool
	after       *string
	seen        map[string]bool
	cursors     map[string]bool
}

func (a *ciAccumulator) append(page ciPage) error {
	if !a.initialized {
		a.initialized = true
		a.total = page.total
		a.rollup = domain.CIRollup{CommitSHA: page.rollup.CommitSHA, TotalCount: page.rollup.TotalCount, Contexts: []domain.CIContext{}}
		a.seen = map[string]bool{}
		a.cursors = map[string]bool{}
	}
	if a.total != page.total || a.rollup.CommitSHA != page.rollup.CommitSHA {
		return domain.Fail(domain.Conflict, "The CI inventory changed during pagination.", "Refresh the complete PR CI evidence.")
	}
	if a.done {
		return nil
	}
	for _, row := range page.rollup.Contexts {
		if a.seen[row.NodeID] {
			return queryUnavailable()
		}
		a.seen[row.NodeID] = true
		a.rollup.Contexts = append(a.rollup.Contexts, row)
	}
	if len(a.rollup.Contexts) > domain.MaxCIContexts || uint64(len(a.rollup.Contexts)) > a.total {
		return queryUnavailable()
	}
	if page.next == nil {
		if uint64(len(a.rollup.Contexts)) != a.total {
			return queryUnavailable()
		}
		a.done = true
		sort.Slice(a.rollup.Contexts, func(i, j int) bool { return a.rollup.Contexts[i].NodeID < a.rollup.Contexts[j].NodeID })
		return nil
	}
	if uint64(len(a.rollup.Contexts)) >= a.total || a.cursors[*page.next] {
		return queryUnavailable()
	}
	a.cursors[*page.next] = true
	a.after = page.next
	return nil
}
func (c *Client) readCIInventory(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem) (domain.PullRequestCI, error) {
	var result domain.PullRequestCI
	var binding ciBinding
	head, merge := ciAccumulator{}, ciAccumulator{}
	queue := ciQueueAccumulator{}
	for page := 0; page < 5; page++ {
		node, err := c.readCIPage(ctx, token, item.NodeID, head.after, merge.after, queue.after, queue.checks.after)
		if err != nil {
			return result, err
		}
		current, headPage, mergePage, err := parseCINode(node, repository, item)
		if err != nil {
			return result, err
		}
		queuePage, err := parseCIQueue(node, repository, item, current.inMergeQueue)
		if err != nil {
			return result, err
		}
		if err := queue.append(queuePage, item); err != nil {
			return result, err
		}
		if page == 0 {
			binding = current
		} else if binding != current {
			return result, domain.Fail(domain.Conflict, "The PR CI evaluation commit changed during pagination.", "Refresh the complete PR evidence.")
		}
		if err := head.append(headPage); err != nil {
			return result, err
		}
		if mergePage != nil {
			if err := merge.append(*mergePage); err != nil {
				return result, err
			}
		}
		if head.done && (mergePage == nil || merge.done) && queue.complete() {
			result.Head = head.rollup
			if mergePage != nil {
				result.TestMerge = &merge.rollup
			}
			result.NativeMergeability, result.InMergeQueue = binding.mergeability, binding.inMergeQueue
			result.MergeQueue = queue.queue
			return result, nil
		}
	}
	return result, domain.Fail(domain.ResourceExhausted, "The CI inventory exceeds its complete page limit.", "Inspect checks in GitHub; no partial CI result is returned.")
}
