package github

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func feedbackLimit() error {
	return domain.Fail(domain.ResourceExhausted, "The complete PR feedback exceeds its read limit.", "Inspect feedback in GitHub; no partial inventory is returned.")
}

func validateFeedbackPR(node map[string]json.RawMessage, repository domain.RemoteRepository, item domain.RepositoryItem) error {
	repo, ok := jsonObject(node["repository"])
	number, numberOK := exactUnsigned(node["number"])
	merged, mergedOK := nullableBool(node, "merged")
	state := "OPEN"
	if item.State == domain.RepositoryItemClosed {
		state = "CLOSED"
	}
	if item.Merged != nil && *item.Merged {
		state = "MERGED"
	}
	if !ok || stringField(repo, "id") != repository.NodeID || !numberOK || strconv.FormatUint(number, 10) != item.Number || stringField(node, "id") != item.NodeID || stringField(node, "state") != state || !mergedOK || merged == nil || item.Merged == nil || *merged != *item.Merged || stringField(node, "baseRefName") != item.BaseRef || stringField(node, "baseRefOid") != item.BaseSHA || stringField(node, "headRefName") != item.HeadRef || stringField(node, "headRefOid") != item.HeadSHA {
		return domain.Fail(domain.Conflict, "The PR changed while reading feedback.", "Refresh the complete feedback for the selected PR.")
	}
	return nil
}

type feedbackPages struct {
	total   uint64
	count   uint64
	pages   uint32
	after   *string
	seen    map[string]bool
	cursors map[string]bool
}

func (p *feedbackPages) append(raw []byte) ([]map[string]json.RawMessage, bool, error) {
	f, ok := jsonObject(raw)
	total, totalOK := exactUnsigned(f["totalCount"])
	page, pageOK := jsonObject(f["pageInfo"])
	next, nextOK := nullableBool(page, "hasNextPage")
	cursor, cursorOK := nullableString(page, "endCursor")
	var rows []map[string]json.RawMessage
	if !ok || !totalOK || !pageOK || !nextOK || next == nil || !cursorOK || domain.Decode(f["nodes"], &rows) != nil || rows == nil || len(rows) > 100 {
		return nil, false, queryUnavailable()
	}
	if total > domain.MaxPRFeedback {
		return nil, false, feedbackLimit()
	}
	if p.pages == 0 {
		p.total = total
		p.seen = map[string]bool{}
		p.cursors = map[string]bool{}
	}
	p.pages++
	if total != p.total {
		return nil, false, domain.Fail(domain.Conflict, "Feedback changed during pagination.", "Refresh the complete PR feedback.")
	}
	for _, row := range rows {
		id := stringField(row, "id")
		if domain.Text(id, "feedback node", 256, true) != nil || p.seen[id] {
			return nil, false, queryUnavailable()
		}
		p.seen[id] = true
	}
	p.count += uint64(len(rows))
	if p.count > total || !*next && p.count != total {
		return nil, false, queryUnavailable()
	}
	if *next {
		if p.pages >= 5 {
			return nil, false, feedbackLimit()
		}
		if len(rows) == 0 || p.count >= total || cursor == nil || domain.Text(*cursor, "feedback cursor", 1024, true) != nil || p.cursors[*cursor] {
			return nil, false, queryUnavailable()
		}
		p.cursors[*cursor], p.after = true, cursor
	}
	return rows, !*next, nil
}

func parseFeedbackAuthor(raw []byte) (*domain.FeedbackAuthor, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	f, ok := jsonObject(raw)
	if !ok {
		return nil, queryUnavailable()
	}
	a := &domain.FeedbackAuthor{NativeType: stringField(f, "__typename"), NodeID: stringField(f, "id"), Login: stringField(f, "login"), Kind: domain.RepositoryUnknownActor}
	switch a.NativeType {
	case "User":
		a.Kind = domain.RepositoryUser
	case "Bot":
		a.Kind = domain.RepositoryBot
	case "Organization":
		a.Kind = domain.RepositoryOrganization
	}
	if a.Kind != domain.RepositoryUnknownActor {
		id, ok := exactUnsigned(f["databaseId"])
		if !ok {
			return nil, queryUnavailable()
		}
		a.ID = strconv.FormatUint(id, 10)
	}
	if a.Validate() != nil {
		return nil, queryUnavailable()
	}
	return a, nil
}

func feedbackLine(f map[string]json.RawMessage, key string) (*uint32, error) {
	raw, exists := f[key]
	if !exists {
		return nil, queryUnavailable()
	}
	if string(raw) == "null" {
		return nil, nil
	}
	n, ok := exactUnsigned(raw)
	if !ok || n == 0 || n > 2147483647 {
		return nil, queryUnavailable()
	}
	value := uint32(n)
	return &value, nil
}

func parseFeedback(f map[string]json.RawMessage, kind domain.PRFeedbackKind, thread string, repository domain.RemoteRepository, item domain.RepositoryItem) (domain.PRFeedback, bool, error) {
	v := domain.PRFeedback{Kind: kind, NodeID: stringField(f, "id"), ID: stringField(f, "fullDatabaseId"), NativeState: stringField(f, "state"), ThreadNodeID: thread}
	pr, prOK := jsonObject(f["pullRequest"])
	repo, repoOK := jsonObject(f["repository"])
	if !prOK || stringField(pr, "id") != item.NodeID || !repoOK || stringField(repo, "id") != repository.NodeID || !domain.PositiveDecimal(v.ID) {
		return v, false, queryUnavailable()
	}
	if kind == domain.PRReviewBody {
		submitted, ok := nullableTime(f, "submittedAt")
		if !ok || v.NativeState == "PENDING" && submitted != nil {
			return v, false, queryUnavailable()
		}
		if v.NativeState == "PENDING" {
			return v, true, nil
		}
		if submitted == nil || submitted.IsZero() || !domain.PublishedReviewState(v.NativeState) {
			return v, false, queryUnavailable()
		}
		v.ReviewSubmittedAt = submitted
	}
	if kind == domain.PRReviewComment {
		review, ok := jsonObject(f["pullRequestReview"])
		submitted, timeOK := nullableTime(review, "submittedAt")
		state := stringField(review, "state")
		if !ok || !timeOK {
			return v, false, queryUnavailable()
		}
		if v.NativeState == "PENDING" && state == "PENDING" && submitted == nil {
			return v, true, nil
		}
		if !domain.PublishedReviewState(state) || submitted == nil || submitted.IsZero() {
			return v, false, queryUnavailable()
		}
		v.ReviewNodeID = stringField(review, "id")
		v.ReviewState, v.ReviewSubmittedAt = state, submitted
		code := &domain.FeedbackCode{Path: stringField(f, "path"), DiffHunk: stringField(f, "diffHunk")}
		var err error
		code.Line, err = feedbackLine(f, "line")
		if err != nil {
			return v, false, err
		}
		code.OriginalLine, err = feedbackLine(f, "originalLine")
		if err != nil {
			return v, false, err
		}
		v.Code = code
	}
	v.URL = stringField(f, "url")
	if json.Unmarshal(f["body"], &v.Body) != nil || string(f["body"]) == "null" {
		return v, false, queryUnavailable()
	}
	var err error
	v.PublishedAt, err = time.Parse(time.RFC3339Nano, stringField(f, "publishedAt"))
	if err != nil {
		return v, false, queryUnavailable()
	}
	var ok bool
	v.LastEditedAt, ok = nullableTime(f, "lastEditedAt")
	if !ok {
		return v, false, queryUnavailable()
	}
	v.Author, err = parseFeedbackAuthor(f["author"])
	if err != nil {
		return v, false, err
	}
	v.ContentVersion = v.Version()
	if v.Validate(item) != nil {
		return v, false, queryUnavailable()
	}
	return v, false, nil
}

func parseFeedbackThread(node map[string]json.RawMessage, repository domain.RemoteRepository, item domain.RepositoryItem) (domain.PRFeedbackThread, error) {
	v := domain.PRFeedbackThread{NodeID: stringField(node, "id"), CommentNodes: []string{}}
	pr, ok := jsonObject(node["pullRequest"])
	repo, repoOK := jsonObject(node["repository"])
	resolved, resolvedOK := nullableBool(node, "isResolved")
	outdated, outdatedOK := nullableBool(node, "isOutdated")
	if !ok || !repoOK || stringField(repo, "id") != repository.NodeID || validateFeedbackPR(pr, repository, item) != nil || domain.Text(v.NodeID, "thread node", 256, true) != nil || !resolvedOK || resolved == nil || !outdatedOK || outdated == nil {
		return v, queryUnavailable()
	}
	v.Resolved, v.Outdated = *resolved, *outdated
	return v, nil
}

func (c *Client) readFeedbackInventory(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem) (*domain.PullRequestFeedback, error) {
	result := &domain.PullRequestFeedback{BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA, Entries: []domain.PRFeedback{}, Threads: []domain.PRFeedbackThread{}}
	count, bodyBytes := 0, 0
	seen := map[string]bool{}
	appendEntry := func(v domain.PRFeedback, draft bool) error {
		if seen[v.NodeID] {
			return queryUnavailable()
		}
		seen[v.NodeID] = true
		count++
		if count > domain.MaxPRFeedback {
			return feedbackLimit()
		}
		if draft {
			return nil
		}
		bodyBytes += len(v.Body)
		if v.Code != nil {
			bodyBytes += len(v.Code.Path) + len(v.Code.DiffHunk)
		}
		if bodyBytes > domain.MaxPRFeedbackBytes {
			return feedbackLimit()
		}
		result.Entries = append(result.Entries, v)
		return nil
	}
	for _, group := range []struct {
		operation graphQLRead
		field     string
		kind      domain.PRFeedbackKind
	}{{graphQLReviews, "reviews", domain.PRReviewBody}, {graphQLConversation, "comments", domain.PRConversationComment}, {graphQLThreads, "reviewThreads", ""}} {
		pages := feedbackPages{}
		for {
			node, err := c.readGraphQLNode(ctx, token, group.operation, map[string]any{"id": item.NodeID, "after": pages.after})
			if err != nil {
				return nil, err
			}
			if err := validateFeedbackPR(node, repository, item); err != nil {
				return nil, err
			}
			rows, done, err := pages.append(node[group.field])
			if err != nil {
				return nil, err
			}
			for _, row := range rows {
				if group.operation != graphQLThreads {
					entry, draft, err := parseFeedback(row, group.kind, "", repository, item)
					if err != nil {
						return nil, err
					}
					if err := appendEntry(entry, draft); err != nil {
						return nil, err
					}
					if draft {
						result.ExcludedDraftReviews++
					}
					continue
				}
				thread, err := parseFeedbackThread(row, repository, item)
				if err != nil {
					return nil, err
				}
				if seen[thread.NodeID] {
					return nil, queryUnavailable()
				}
				seen[thread.NodeID] = true
				comments := feedbackPages{}
				for {
					entries, done, err := comments.append(row["comments"])
					if err != nil {
						return nil, err
					}
					for _, raw := range entries {
						entry, draft, err := parseFeedback(raw, domain.PRReviewComment, thread.NodeID, repository, item)
						if err != nil {
							return nil, err
						}
						if err := appendEntry(entry, draft); err != nil {
							return nil, err
						}
						if draft {
							thread.ExcludedDrafts++
						} else {
							thread.CommentNodes = append(thread.CommentNodes, entry.NodeID)
						}
					}
					if done {
						break
					}
					row, err = c.readGraphQLNode(ctx, token, graphQLThreadComments, map[string]any{"id": thread.NodeID, "after": comments.after})
					if err != nil {
						return nil, err
					}
					current, err := parseFeedbackThread(row, repository, item)
					if err != nil || current.NodeID != thread.NodeID || current.Resolved != thread.Resolved || current.Outdated != thread.Outdated {
						return nil, queryUnavailable()
					}
				}
				sort.Strings(thread.CommentNodes)
				result.Threads = append(result.Threads, thread)
			}
			if done {
				break
			}
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].NodeID < result.Entries[j].NodeID })
	sort.Slice(result.Threads, func(i, j int) bool { return result.Threads[i].NodeID < result.Threads[j].NodeID })
	if result.Validate(item) != nil {
		return nil, queryUnavailable()
	}
	return result, nil
}
