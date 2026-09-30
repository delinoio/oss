package github

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func nullableString(fields map[string]json.RawMessage, key string) (*string, bool) {
	raw, exists := fields[key]
	if !exists {
		return nil, false
	}
	var value *string
	err := json.Unmarshal(raw, &value)
	return value, err == nil
}
func nullableTime(fields map[string]json.RawMessage, key string) (*time.Time, bool) {
	raw, ok := nullableString(fields, key)
	if !ok || raw == nil {
		return nil, ok
	}
	value, err := time.Parse(time.RFC3339Nano, *raw)
	return &value, err == nil
}
func repositoryPath(repository domain.RemoteRepository) string {
	return "/repos/" + url.PathEscape(repository.Owner) + "/" + url.PathEscape(repository.Name)
}
func parsePRChecks(raw []byte, repository domain.RemoteRepository, item domain.RepositoryItem, pageSize uint32) (*domain.PullRequestChecks, error) {
	fields, ok := jsonObject(raw)
	total, totalOK := exactUnsigned(fields["total_count"])
	var rows []json.RawMessage
	if !ok || !totalOK || domain.Decode(fields["check_runs"], &rows) != nil || rows == nil || len(rows) > int(pageSize) || uint64(len(rows)) > total {
		return nil, queryUnavailable()
	}
	result := &domain.PullRequestChecks{HeadSHA: item.HeadSHA, Filter: domain.LatestCheckRuns, TotalCount: strconv.FormatUint(total, 10), Runs: []domain.PullRequestCheck{}}
	for _, raw := range rows {
		fields, ok := jsonObject(raw)
		id, idOK := exactUnsigned(fields["id"])
		if !ok || !idOK {
			return nil, queryUnavailable()
		}
		value := domain.PullRequestCheck{ID: strconv.FormatUint(id, 10), NodeID: stringField(fields, "node_id"), Name: stringField(fields, "name"), HeadSHA: stringField(fields, "head_sha"), NativeStatus: stringField(fields, "status")}
		if !strings.EqualFold(stringField(fields, "url"), apiOrigin+repositoryPath(repository)+"/check-runs/"+value.ID) {
			return nil, queryUnavailable()
		}
		value.Status = domain.ClassifyCheckStatus(value.NativeStatus)
		value.NativeConclusion, ok = nullableString(fields, "conclusion")
		if !ok {
			return nil, queryUnavailable()
		}
		if value.NativeConclusion != nil {
			conclusion := domain.ClassifyCheckConclusion(*value.NativeConclusion)
			value.Conclusion = &conclusion
		}
		value.StartedAt, ok = nullableTime(fields, "started_at")
		if !ok {
			return nil, queryUnavailable()
		}
		value.CompletedAt, ok = nullableTime(fields, "completed_at")
		if !ok {
			return nil, queryUnavailable()
		}
		application, exists := fields["app"]
		if !exists {
			return nil, queryUnavailable()
		}
		if string(application) != "null" {
			app, ok := jsonObject(application)
			appID, idOK := exactUnsigned(app["id"])
			if !ok || !idOK {
				return nil, queryUnavailable()
			}
			value.Application = &domain.CheckApplication{ID: strconv.FormatUint(appID, 10), NodeID: stringField(app, "node_id"), Slug: stringField(app, "slug")}
		}
		result.Runs = append(result.Runs, value)
	}
	if result.Validate(item, pageSize) != nil {
		return nil, queryUnavailable()
	}
	return result, nil
}
func parsePRStatuses(raw []byte, repository domain.RemoteRepository, item domain.RepositoryItem, pageSize uint32) (*domain.PullRequestCommitStatuses, error) {
	fields, ok := jsonObject(raw)
	total, totalOK := exactUnsigned(fields["total_count"])
	var rows []json.RawMessage
	remote, remoteOK := jsonObject(fields["repository"])
	remoteID, remoteIDOK := exactUnsigned(remote["id"])
	if !ok || !totalOK || !remoteOK || !remoteIDOK || strconv.FormatUint(remoteID, 10) != repository.ID || stringField(remote, "node_id") != repository.NodeID || stringField(fields, "sha") != item.HeadSHA || domain.Decode(fields["statuses"], &rows) != nil || rows == nil || len(rows) > int(pageSize) || uint64(len(rows)) > total {
		return nil, queryUnavailable()
	}
	result := &domain.PullRequestCommitStatuses{HeadSHA: item.HeadSHA, NativeState: stringField(fields, "state"), TotalCount: strconv.FormatUint(total, 10), Contexts: []domain.PullRequestCommitStatus{}}
	result.State = domain.ClassifyCommitStatus(result.NativeState)
	for _, raw := range rows {
		fields, ok := jsonObject(raw)
		id, idOK := exactUnsigned(fields["id"])
		if !ok || !idOK {
			return nil, queryUnavailable()
		}
		value := domain.PullRequestCommitStatus{ID: strconv.FormatUint(id, 10), NodeID: stringField(fields, "node_id"), Context: stringField(fields, "context"), NativeState: stringField(fields, "state")}
		if !strings.EqualFold(stringField(fields, "url"), apiOrigin+repositoryPath(repository)+"/statuses/"+item.HeadSHA) {
			return nil, queryUnavailable()
		}
		value.State = domain.ClassifyCommitStatus(value.NativeState)
		value.Description, ok = nullableString(fields, "description")
		if !ok {
			return nil, queryUnavailable()
		}
		value.Creator, ok = parseActor(fields["creator"])
		if !ok {
			return nil, queryUnavailable()
		}
		var err error
		value.CreatedAt, err = time.Parse(time.RFC3339Nano, stringField(fields, "created_at"))
		if err != nil {
			return nil, queryUnavailable()
		}
		value.UpdatedAt, err = time.Parse(time.RFC3339Nano, stringField(fields, "updated_at"))
		if err != nil {
			return nil, queryUnavailable()
		}
		result.Contexts = append(result.Contexts, value)
	}
	if result.Validate(item, pageSize) != nil {
		return nil, queryUnavailable()
	}
	return result, nil
}
func (c *Client) queryPRObservation(ctx context.Context, token []byte, repository domain.RemoteRepository, q domain.RepositoryQuery, result RepositoryQueryObservation) (RepositoryQueryObservation, error) {
	detailQuery := domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: q.Number}
	detailPath, _ := queryPath(repository, detailQuery)
	readDetail := func() (domain.RepositoryItem, error) {
		read := c.readRepositoryJSON(ctx, token, detailPath)
		if read.state != domain.IntegrationAccessAvailable {
			return domain.RepositoryItem{}, readProblem(read)
		}
		value, skip, err := parseItem(read.raw, repository, detailQuery)
		if skip {
			return domain.RepositoryItem{}, queryUnavailable()
		}
		return value, err
	}
	item, err := readDetail()
	if err != nil {
		return result, err
	}
	result.Items = []domain.RepositoryItem{item}
	if q.Operation == domain.RepositoryFeedback || q.Operation == domain.RepositoryReviewers {
		result.Feedback, err = c.readFeedbackInventory(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		if q.Operation == domain.RepositoryReviewers {
			result.Reviewers, err = c.readReviewerEvidence(ctx, token, repository, item, *result.Feedback)
			if err != nil {
				return result, err
			}
		}
		repeated, err := c.readFeedbackInventory(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		if !reflect.DeepEqual(result.Feedback, repeated) {
			return result, domain.Fail(domain.Conflict, "Published feedback changed during the read.", "Refresh the complete PR feedback; no mixed content versions were published.")
		}
		if result.Reviewers != nil {
			result.Feedback = nil
		}
	} else if q.Operation == domain.RepositoryRules {
		result.Rules, err = c.readActiveRules(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		repeated, err := c.readActiveRules(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		if repeated.Digest != result.Rules.Digest {
			return result, domain.Fail(domain.Conflict, "The active rules changed during the read.", "Refresh the PR rules explicitly; no earlier requirements were published.")
		}
	} else if q.Operation == domain.RepositoryCI {
		rules, err := c.readActiveRules(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		observed, err := c.readCIInventory(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		if err := c.readRequiredWorkflows(ctx, token, repository, item, &observed, *rules); err != nil {
			return result, err
		}
		repeated, err := c.readCIInventory(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		if err := c.readRequiredWorkflows(ctx, token, repository, item, &repeated, *rules); err != nil {
			return result, err
		}
		// Both optional inventories must be complete to retain workflow proof.
		// A local enrichment budget or admission failure makes that family
		// unavailable without discarding independently repeated ordinary checks.
		if observed.WorkflowRuns == nil || repeated.WorkflowRuns == nil {
			observed.WorkflowRuns, repeated.WorkflowRuns = nil, nil
		}
		if !reflect.DeepEqual(observed, repeated) {
			return result, domain.Fail(domain.Conflict, "CI results changed during the read.", "Refresh the complete PR CI evidence; no mixed result was published.")
		}
		currentRules, err := c.readActiveRules(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		if rules.Digest != currentRules.Digest {
			return result, domain.Fail(domain.Conflict, "Active CI rules changed during the read.", "Refresh the PR CI evaluation explicitly.")
		}
		// Recheck every inventory after the final rule read. REST PR head/base
		// equality cannot detect entry into, removal from or reordering in a queue.
		final, err := c.readCIInventory(ctx, token, repository, item)
		if err != nil {
			return result, err
		}
		// Retained workflow proof must also survive the final bracket. If this
		// optional read is unavailable, drop that entire family as in the first
		// two reads; ordinary queue/check evidence still has to agree exactly.
		if observed.WorkflowRuns != nil {
			if err := c.readRequiredWorkflows(ctx, token, repository, item, &final, *currentRules); err != nil {
				return result, err
			}
			if final.WorkflowRuns == nil {
				observed.WorkflowRuns = nil
			}
		}
		if !reflect.DeepEqual(observed, final) {
			return result, domain.Fail(domain.Conflict, "CI evidence changed during the final evaluation read.", "Refresh the complete current PR evidence.")
		}
		observed.Rules = *rules
		observed.Result = observed.Evaluate(item)
		if observed.Validate(item) != nil {
			return result, queryUnavailable()
		}
		result.CI = &observed
	} else {
		base := repositoryPath(repository)
		values := url.Values{"page": {strconv.FormatUint(uint64(q.Page), 10)}, "per_page": {strconv.FormatUint(uint64(q.PageSize), 10)}}
		var path string
		var read readResult
		switch q.Operation {
		case domain.RepositoryDiff:
			// Immutable commit operands prevent an A -> B -> A PR-head race from
			// mislabeling a diff fetched by mutable PR number. No fallback is safe.
			path = base + "/compare/" + item.BaseSHA + "..." + item.HeadSHA
			read = c.readRepository(ctx, token, path, repositoryDiff)
		case domain.RepositoryChecks:
			values.Set("filter", "latest")
			path = base + "/commits/" + item.HeadSHA + "/check-runs?" + values.Encode()
			read = c.readRepositoryJSON(ctx, token, path)
		case domain.RepositoryStatuses:
			path = base + "/commits/" + item.HeadSHA + "/status?" + values.Encode()
			read = c.readRepositoryJSON(ctx, token, path)
		default:
			return result, queryUnavailable()
		}
		if read.state != domain.IntegrationAccessAvailable {
			return result, readProblem(read)
		}
		switch q.Operation {
		case domain.RepositoryDiff:
			if read.link != "" {
				return result, queryUnavailable()
			}
			sum := sha256.Sum256(read.raw)
			result.Diff = &domain.PullRequestDiff{Patch: string(read.raw), Digest: hex.EncodeToString(sum[:]), BaseSHA: item.BaseSHA, HeadSHA: item.HeadSHA}
			if result.Diff.Validate(item) != nil {
				return result, queryUnavailable()
			}
		case domain.RepositoryChecks:
			result.Checks, err = parsePRChecks(read.raw, repository, item, q.PageSize)
		case domain.RepositoryStatuses:
			result.Statuses, err = parsePRStatuses(read.raw, repository, item, q.PageSize)
		}
		if err != nil {
			return result, err
		}
		if q.Operation != domain.RepositoryDiff {
			result.NextPage, err = queryNextPage(read.link, path, q.Page, repository)
			if err != nil {
				return result, err
			}
			if result.NextPage > 10000 {
				return result, domain.Fail(domain.ResourceExhausted, "The PR observation page limit is reached.", "Inspect the selected PR in GitHub.")
			}
		}
	}
	current, err := readDetail()
	if err != nil {
		return result, err
	}
	if current.ID != item.ID || current.NodeID != item.NodeID || current.BaseRef != item.BaseRef || current.BaseSHA != item.BaseSHA || current.HeadRef != item.HeadRef || current.HeadSHA != item.HeadSHA || !samePRHeadRepository(current.HeadRepository, item.HeadRepository) {
		return result, domain.Fail(domain.Conflict, "The PR base or head changed while reading its observation.", "Refresh the current PR explicitly; the earlier data was not published.")
	}
	if result.CI != nil && (current.State != item.State || current.Merged == nil || item.Merged == nil || *current.Merged != *item.Merged || result.CI.Validate(current) != nil) {
		return result, domain.Fail(domain.Conflict, "The PR CI evaluation changed before publication.", "Refresh current CI evidence.")
	}
	if ctx.Err() != nil {
		return result, domain.SafeError(ctx.Err())
	}
	return result, nil
}
