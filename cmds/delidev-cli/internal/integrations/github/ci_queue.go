package github

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ciQueuePage struct {
	queue   domain.CIMergeQueue
	entries []domain.CIMergeQueueEntry
	total   uint64
	next    *string
	checks  *ciPage
}

func parseCIQueueEntry(raw []byte) (domain.CIMergeQueueEntry, error) {
	f, ok := jsonObject(raw)
	pr, prOK := jsonObject(f["pullRequest"])
	number, numberOK := exactUnsigned(pr["number"])
	position, positionOK := exactUnsigned(f["position"])
	v := domain.CIMergeQueueEntry{NodeID: stringField(f, "id"), PullRequestNodeID: stringField(pr, "id"), PullRequestNumber: strconv.FormatUint(number, 10), Position: strconv.FormatUint(position, 10), State: stringField(f, "state")}
	for key, dest := range map[string]*string{"baseCommit": &v.BaseSHA, "headCommit": &v.HeadSHA} {
		value, exists := f[key]
		if !exists {
			return v, queryUnavailable()
		}
		if string(value) != "null" {
			commit, valid := jsonObject(value)
			*dest = stringField(commit, "oid")
			if !valid || !commitSHA(*dest) {
				return v, queryUnavailable()
			}
		}
	}
	if !ok || !prOK || !numberOK || !positionOK || v.Validate() != nil {
		return v, queryUnavailable()
	}
	return v, nil
}

func parseCIQueue(node map[string]json.RawMessage, repository domain.RemoteRepository, item domain.RepositoryItem, queued bool) (*ciQueuePage, error) {
	raw, exists := node["mergeQueueEntry"]
	if !exists {
		return nil, queryUnavailable()
	}
	if string(raw) == "null" {
		return nil, nil
	}
	if !queued {
		return nil, queryUnavailable()
	}
	f, ok := jsonObject(raw)
	entry, err := parseCIQueueEntry(raw)
	if err != nil || !ok || entry.PullRequestNodeID != item.NodeID || entry.PullRequestNumber != item.Number {
		return nil, queryUnavailable()
	}
	queueRaw, exists := f["mergeQueue"]
	if !exists {
		return nil, queryUnavailable()
	}
	// A provider-visible entry without its queue/configuration cannot authorize
	// commit selection. Preserve Unknown rather than borrowing the PR head.
	if string(queueRaw) == "null" {
		return nil, nil
	}
	q, ok := jsonObject(queueRaw)
	repo, repoOK := jsonObject(q["repository"])
	if !ok || !repoOK || stringField(repo, "id") != repository.NodeID {
		return nil, queryUnavailable()
	}
	v := &ciQueuePage{queue: domain.CIMergeQueue{NodeID: stringField(q, "id"), RepositoryNodeID: repository.NodeID, Strategy: domain.CIQueueUnknownStrategy, Entry: entry}}
	configRaw, exists := q["configuration"]
	if !exists {
		return nil, queryUnavailable()
	}
	if string(configRaw) != "null" {
		config, valid := jsonObject(configRaw)
		if !valid {
			return nil, queryUnavailable()
		}
		switch strategy := domain.CIMergeQueueStrategy(stringField(config, "mergingStrategy")); strategy {
		case domain.CIQueueAllGreen, domain.CIQueueHeadGreen:
			v.queue.Strategy = strategy
		}
	}
	connection, connOK := jsonObject(q["entries"])
	total, totalOK := exactUnsigned(connection["totalCount"])
	page, pageOK := jsonObject(connection["pageInfo"])
	hasNext, nextOK := nullableBool(page, "hasNextPage")
	cursor, cursorOK := nullableString(page, "endCursor")
	var rows []json.RawMessage
	if !connOK || !totalOK || !pageOK || !nextOK || hasNext == nil || !cursorOK || domain.Decode(connection["nodes"], &rows) != nil || rows == nil || len(rows) > 100 || uint64(len(rows)) > total {
		return nil, queryUnavailable()
	}
	if total > domain.MaxCIMergeQueueEntries {
		return nil, domain.Fail(domain.ResourceExhausted, "The merge queue exceeds its complete read limit.", "Inspect the queue in GitHub; no partial CI result is returned.")
	}
	v.total, v.entries = total, []domain.CIMergeQueueEntry{}
	if *hasNext {
		if cursor == nil || domain.Text(*cursor, "queue cursor", 1024, true) != nil || len(rows) == 0 {
			return nil, queryUnavailable()
		}
		v.next = cursor
	}
	for _, raw := range rows {
		entry, err := parseCIQueueEntry(raw)
		if err != nil {
			return nil, err
		}
		v.entries = append(v.entries, entry)
	}
	if entry.HeadSHA != "" {
		head, _ := jsonObject(f["headCommit"])
		repo, valid := jsonObject(head["repository"])
		if !valid || stringField(repo, "id") != repository.NodeID {
			return nil, queryUnavailable()
		}
		checks, err := parseCIRollup(head["statusCheckRollup"], repository, entry.HeadSHA)
		if err != nil {
			return nil, err
		}
		v.checks = &checks
	}
	return v, nil
}

type ciQueueAccumulator struct {
	queue   *domain.CIMergeQueue
	after   *string
	cursors map[string]bool
	done    bool
	checks  ciAccumulator
}

func (a *ciQueueAccumulator) append(page *ciQueuePage, item domain.RepositoryItem) error {
	if page == nil {
		if a.queue != nil {
			return queryUnavailable()
		}
		a.done = true
		return nil
	}
	if a.queue == nil {
		if a.done {
			return queryUnavailable()
		}
		copy := page.queue
		copy.TotalCount, copy.Entries = strconv.FormatUint(page.total, 10), []domain.CIMergeQueueEntry{}
		a.queue, a.cursors = &copy, map[string]bool{}
	} else {
		binding := *a.queue
		binding.TotalCount, binding.Entries, binding.Rollup = "", nil, nil
		if !reflect.DeepEqual(binding, page.queue) || a.queue.TotalCount != strconv.FormatUint(page.total, 10) {
			return domain.Fail(domain.Conflict, "The merge queue entry changed during the read.", "Refresh the complete PR CI evidence.")
		}
	}
	if !a.done {
		a.queue.Entries = append(a.queue.Entries, page.entries...)
		if len(a.queue.Entries) > domain.MaxCIMergeQueueEntries || uint64(len(a.queue.Entries)) > page.total {
			return queryUnavailable()
		}
		if page.next == nil {
			if uint64(len(a.queue.Entries)) != page.total {
				return queryUnavailable()
			}
			a.done = true
		} else {
			if uint64(len(a.queue.Entries)) >= page.total || a.cursors[*page.next] {
				return queryUnavailable()
			}
			a.cursors[*page.next], a.after = true, page.next
		}
	}
	if page.checks != nil {
		if err := a.checks.append(*page.checks); err != nil {
			return err
		}
		if a.checks.done {
			a.queue.Rollup = &a.checks.rollup
		}
	}
	if a.done && (page.checks == nil || a.checks.done) && a.queue.Validate(item) != nil {
		return queryUnavailable()
	}
	return nil
}

func (a ciQueueAccumulator) complete() bool {
	return a.done && (a.queue == nil || a.queue.Entry.HeadSHA == "" || a.checks.done)
}
