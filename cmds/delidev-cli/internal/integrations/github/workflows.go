// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const workflowSuitesGraphQL = `query DeliDevWorkflowSuites($id: ID!, $after: String) {
  node(id: $id) { ... on PullRequest {
    id number state merged baseRefName baseRefOid headRefName headRefOid mergeable isInMergeQueue repository { id }
    potentialMergeCommit { oid parents(first: 3) { totalCount nodes { oid } }
      checkSuites(first: 100, after: $after) { totalCount pageInfo { hasNextPage endCursor }
        nodes { id commit { oid } app { databaseId }
          workflowRun { id databaseId event runAttempt
            file { id path repositoryName repositoryFileUrl viewerCanReadRepository run { id } }
          }
        }
      }
    }
  } }
}`

type workflowSuite struct {
	run  domain.CIRequiredWorkflowRun
	file map[string]json.RawMessage
}

// Enumerate all suites on the independently parent-verified current test merge,
// including competing runs that the latest status rollup might omit.
func (c *Client) workflowSuites(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem, ci domain.PullRequestCI) ([]workflowSuite, error) {
	result := []workflowSuite{}
	seen, cursors := map[string]bool{}, map[string]bool{}
	var after *string
	var total uint64
	count := 0
	for page := 0; page < 5; page++ {
		node, err := c.readGraphQLNode(ctx, token, graphQLWorkflowSuites, map[string]any{"id": item.NodeID, "after": after})
		if err != nil {
			return nil, err
		}
		merge, ok := jsonObject(node["potentialMergeCommit"])
		connection, connOK := jsonObject(merge["checkSuites"])
		// Reuse the existing original PR/ordered-parent validator, with no rollup
		// projection in this separate inventory document.
		node["statusCheckRollup"] = json.RawMessage("null")
		merge["statusCheckRollup"] = json.RawMessage("null")
		node["potentialMergeCommit"], _ = json.Marshal(merge)
		binding, _, _, err := parseCINode(node, repository, item)
		if !ok || !connOK || err != nil || ci.TestMerge == nil || binding.mergeSHA != ci.TestMerge.CommitSHA || binding.mergeability != ci.NativeMergeability || binding.inMergeQueue != ci.InMergeQueue {
			return nil, queryUnavailable()
		}
		n, nOK := exactUnsigned(connection["totalCount"])
		info, infoOK := jsonObject(connection["pageInfo"])
		next, nextOK := nullableBool(info, "hasNextPage")
		cursor, cursorOK := nullableString(info, "endCursor")
		var rows []json.RawMessage
		if !nOK || n > domain.MaxCIContexts || page > 0 && n != total || !infoOK || !nextOK || next == nil || !cursorOK || domain.Decode(connection["nodes"], &rows) != nil || rows == nil || len(rows) > 100 {
			return nil, queryUnavailable()
		}
		total = n
		for _, raw := range rows {
			f, ok := jsonObject(raw)
			commit, commitOK := jsonObject(f["commit"])
			id := stringField(f, "id")
			if !ok || !commitOK || stringField(commit, "oid") != binding.mergeSHA || domain.Text(id, "suite identity", 256, true) != nil || seen[id] {
				return nil, queryUnavailable()
			}
			seen[id] = true
			wRaw, exists := f["workflowRun"]
			if !exists {
				return nil, queryUnavailable()
			}
			if string(wRaw) == "null" {
				app, appOK := jsonObject(f["app"])
				appID, appIDOK := exactUnsigned(app["databaseId"])
				// Only a positively identified non-Actions App proves that a
				// suite is unrelated. Missing App identity cannot hide a competing
				// required workflow whose original run is inaccessible.
				if !appOK || !appIDOK || appID == 0 || appID == 15368 {
					return nil, queryUnavailable()
				}
				continue
			}
			w, wOK := jsonObject(wRaw)
			runID, idOK := exactUnsigned(w["databaseId"])
			attempt, attemptOK := exactUnsigned(w["runAttempt"])
			app, _ := jsonObject(f["app"])
			appID, _ := exactUnsigned(app["databaseId"])
			if !wOK || !idOK || runID == 0 || !attemptOK || attempt == 0 || attempt > 2147483647 || appID != 15368 {
				return nil, queryUnavailable()
			}
			file, _ := jsonObject(w["file"])
			result = append(result, workflowSuite{run: domain.CIRequiredWorkflowRun{NodeID: stringField(w, "id"), RunID: strconv.FormatUint(runID, 10), SuiteNodeID: id, CommitSHA: binding.mergeSHA, Event: stringField(w, "event"), Attempt: strconv.FormatUint(attempt, 10), Jobs: []domain.CIWorkflowJob{}}, file: file})
		}
		count += len(rows)
		if uint64(count) > total {
			return nil, queryUnavailable()
		}
		if !*next {
			if uint64(count) != total {
				return nil, queryUnavailable()
			}
			sort.Slice(result, func(i, j int) bool { return result[i].run.NodeID < result[j].run.NodeID })
			return result, nil
		}
		if cursor == nil || domain.Text(*cursor, "suite cursor", 1024, true) != nil || cursors[*cursor] || len(rows) == 0 || uint64(count) >= total {
			return nil, queryUnavailable()
		}
		cursors[*cursor] = true
		after = cursor
	}
	return nil, queryUnavailable()
}

func (c *Client) workflowRepository(ctx context.Context, token []byte, id string) (*domain.RemoteRepository, error) {
	read := c.readRepositoryJSON(ctx, token, "/repositories/"+id)
	if read.state != domain.IntegrationAccessAvailable {
		return nil, readProblem(read)
	}
	f, ok := jsonObject(read.raw)
	owner, _ := jsonObject(f["owner"])
	repo, valid := parseRepository(read.raw, stringField(owner, "login"), stringField(f, "name"))
	if read.state != domain.IntegrationAccessAvailable || !ok || !valid || repo.ID != id {
		return nil, queryUnavailable()
	}
	return repo, nil
}

func (c *Client) workflowSource(ctx context.Context, token []byte, suite workflowSuite, repositories map[string]*domain.RemoteRepository, run *domain.CIRequiredWorkflowRun) *domain.RequiredWorkflowReference {
	f := suite.file
	parent, _ := jsonObject(f["run"])
	readable, ok := nullableBool(f, "viewerCanReadRepository")
	name, path := stringField(f, "repositoryName"), stringField(f, "path")
	if !ok || readable == nil || !*readable || stringField(parent, "id") != suite.run.NodeID || domain.Text(stringField(f, "id"), "workflow file identity", 256, true) != nil {
		return nil
	}
	var repo *domain.RemoteRepository
	for _, r := range repositories {
		if strings.EqualFold(r.Owner+"/"+r.Name, name) {
			repo = r
			break
		}
	}
	if repo == nil {
		parts := strings.Split(name, "/")
		if len(parts) != 2 || domain.ValidateGitHubRepository(parts[0], parts[1]) != nil {
			return nil
		}
		read := c.readRepositoryJSON(ctx, token, "/repos/"+name)
		var valid bool
		repo, valid = parseRepository(read.raw, parts[0], parts[1])
		if read.state != domain.IntegrationAccessAvailable || !valid {
			return nil
		}
	}
	u, err := url.Parse(stringField(f, "repositoryFileUrl"))
	prefix := "/" + name + "/blob/"
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, prefix) {
		return nil
	}
	sha, remaining, found := strings.Cut(strings.TrimPrefix(u.Path, prefix), "/")
	ref := &domain.RequiredWorkflowReference{RepositoryID: repo.ID, Path: path, SHA: &sha}
	if !found || remaining != path || ref.Validate() != nil || u.EscapedPath() != prefix+sha+"/"+escapedWorkflowPath(path) {
		return nil
	}
	// Independently verify immutable source accessibility. No file content, URL,
	// mutable-ref resolution or user-supplied source authority is retained.
	read := c.readRepositoryJSON(ctx, token, repositoryPath(*repo)+"/contents/"+escapedWorkflowPath(path)+"?"+url.Values{"ref": {sha}}.Encode())
	content, ok := jsonObject(read.raw)
	if read.state != domain.IntegrationAccessAvailable || !ok || stringField(content, "type") != "file" || stringField(content, "path") != path || !commitSHA(stringField(content, "sha")) {
		return nil
	}
	run.SourceFileNodeID, run.SourceBlobSHA = stringField(f, "id"), stringField(content, "sha")
	return ref
}

func escapedWorkflowPath(path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func (c *Client) workflowRun(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem, run domain.CIRequiredWorkflowRun) (map[string]json.RawMessage, error) {
	read := c.readRepositoryJSON(ctx, token, repositoryPath(repository)+"/actions/runs/"+run.RunID)
	f, ok := jsonObject(read.raw)
	id, idOK := exactUnsigned(f["id"])
	attempt, attemptOK := exactUnsigned(f["run_attempt"])
	var pulls []map[string]json.RawMessage
	if read.state != domain.IntegrationAccessAvailable || !ok || !idOK || strconv.FormatUint(id, 10) != run.RunID || stringField(f, "node_id") != run.NodeID || stringField(f, "check_suite_node_id") != run.SuiteNodeID || stringField(f, "head_sha") != item.HeadSHA || !attemptOK || strconv.FormatUint(attempt, 10) != run.Attempt || stringField(f, "event") != run.Event || domain.Decode(f["pull_requests"], &pulls) != nil || len(pulls) != 1 {
		return nil, queryUnavailable()
	}
	number, numberOK := exactUnsigned(pulls[0]["number"])
	base, _ := jsonObject(pulls[0]["base"])
	baseRepo, _ := jsonObject(base["repo"])
	repoID, repoOK := exactUnsigned(baseRepo["id"])
	if !numberOK || strconv.FormatUint(number, 10) != item.Number || !repoOK || strconv.FormatUint(repoID, 10) != repository.ID {
		return nil, queryUnavailable()
	}
	return f, nil
}

func (c *Client) workflowJobs(ctx context.Context, token []byte, repository domain.RemoteRepository, run domain.CIRequiredWorkflowRun) ([]domain.CIWorkflowJob, error) {
	jobs := []domain.CIWorkflowJob{}
	seen := map[string]bool{}
	var total uint64
	for page := uint32(1); page <= 5; page++ {
		path := repositoryPath(repository) + "/actions/runs/" + run.RunID + "/attempts/" + run.Attempt + "/jobs?" + url.Values{"page": {strconv.Itoa(int(page))}, "per_page": {"100"}}.Encode()
		read := c.readRepositoryJSON(ctx, token, path)
		f, ok := jsonObject(read.raw)
		n, nOK := exactUnsigned(f["total_count"])
		var rows []map[string]json.RawMessage
		if read.state != domain.IntegrationAccessAvailable || !ok || !nOK || n > domain.MaxCIContexts || page > 1 && total != n || domain.Decode(f["jobs"], &rows) != nil || rows == nil || len(rows) > 100 {
			return nil, queryUnavailable()
		}
		total = n
		for _, f := range rows {
			id, idOK := exactUnsigned(f["run_id"])
			attempt, attemptOK := exactUnsigned(f["run_attempt"])
			conclusion, conclusionOK := nullableString(f, "conclusion")
			node := stringField(f, "node_id")
			if !idOK || strconv.FormatUint(id, 10) != run.RunID || !attemptOK || strconv.FormatUint(attempt, 10) != run.Attempt || !conclusionOK || seen[node] || domain.Text(node, "job identity", 256, true) != nil {
				return nil, queryUnavailable()
			}
			seen[node] = true
			jobs = append(jobs, domain.CIWorkflowJob{NodeID: node, NativeStatus: strings.ToUpper(stringField(f, "status")), NativeConclusion: upperOptional(conclusion)})
		}
		next, err := queryNextPage(read.link, path, page, repository)
		if err != nil || uint64(len(jobs)) > total || next != 0 && (len(rows) == 0 || uint64(len(jobs)) >= total) {
			return nil, queryUnavailable()
		}
		if next == 0 {
			if uint64(len(jobs)) != total {
				return nil, queryUnavailable()
			}
			sort.Slice(jobs, func(i, j int) bool { return jobs[i].NodeID < jobs[j].NodeID })
			return jobs, nil
		}
	}
	return nil, queryUnavailable()
}

func upperOptional(value *string) *string {
	if value == nil {
		return nil
	}
	text := strings.ToUpper(*value)
	return &text
}

func (c *Client) readRequiredWorkflows(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem, ci *domain.PullRequestCI, rules domain.PullRequestRules) error {
	wanted := false
	for _, rule := range rules.Rules {
		if rule.RequiredWorkflows != nil && len(rule.RequiredWorkflows.Workflows) > 0 {
			wanted = true
		}
	}
	if !wanted || ci.TestMerge == nil || ci.InMergeQueue {
		return nil
	}
	// Optional enrichment must leave time for the repeated ordinary inventory,
	// rules and PR publication brackets. Cap serial work instead of consuming
	// the owning query's deadline; only a complete inventory can become proof.
	budget := 3 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline) / 4; remaining < budget {
			budget = remaining
		}
	}
	proofContext, cancel := context.WithTimeout(ctx, budget)
	runs, err := c.requiredWorkflowInventory(proofContext, token, repository, item, *ci, rules)
	if proofContext.Err() != nil {
		err = proofContext.Err()
	}
	cancel()
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	if err != nil {
		// A failed optional proof cannot erase ordinary status-check evidence or
		// publish a partial workflow inventory. Evaluation remains Unknown.
		slog.WarnContext(ctx, "GitHub required workflow evidence unavailable", "operation", "required-workflows", "code", domain.SafeError(err).Code)
		return nil
	}
	if !retainRequiredWorkflowEvidence(ci, rules, item, runs) {
		slog.WarnContext(ctx, "GitHub required workflow evidence unavailable", "operation", "required-workflows", "code", domain.ResourceExhausted)
		return nil
	}
	unverified := 0
	for _, run := range runs {
		if run.Source == nil {
			unverified++
		}
	}
	slog.DebugContext(ctx, "GitHub required workflow inventory inspected", "operation", "required-workflows", "runs", len(runs), "unverified_sources", unverified)
	return nil
}

func retainRequiredWorkflowEvidence(ci *domain.PullRequestCI, rules domain.PullRequestRules, item domain.RepositoryItem, runs []domain.CIRequiredWorkflowRun) bool {
	candidate := *ci
	candidate.WorkflowRuns, candidate.Rules = runs, rules
	candidate.Result = candidate.Evaluate(item)
	// Size the final rules/result envelope, including attributed result IDs,
	// before admitting optional proof. Never publish a truncated inventory or
	// let enrichment erase the independently collected ordinary check results.
	raw, err := json.Marshal(candidate)
	if err != nil || len(raw) > domain.MaxCIEvidenceBytes {
		return false
	}
	ci.WorkflowRuns = runs
	return true
}

func (c *Client) requiredWorkflowInventory(ctx context.Context, token []byte, repository domain.RemoteRepository, item domain.RepositoryItem, ci domain.PullRequestCI, rules domain.PullRequestRules) ([]domain.CIRequiredWorkflowRun, error) {
	repositories := map[string]*domain.RemoteRepository{}
	for _, rule := range rules.Rules {
		if rule.RequiredWorkflows == nil {
			continue
		}
		for _, ref := range rule.RequiredWorkflows.Workflows {
			if _, exists := repositories[ref.RepositoryID]; exists {
				continue
			}
			if len(repositories) >= 32 {
				return nil, queryUnavailable()
			}
			r, err := c.workflowRepository(ctx, token, ref.RepositoryID)
			if err != nil {
				return nil, err
			}
			repositories[ref.RepositoryID] = r
		}
	}
	suites, err := c.workflowSuites(ctx, token, repository, item, ci)
	if err != nil || len(suites) > 32 {
		return nil, queryUnavailable()
	}
	result := []domain.CIRequiredWorkflowRun{}
	for _, suite := range suites {
		run := suite.run
		run.Source = c.workflowSource(ctx, token, suite, repositories, &run)
		original, err := c.workflowRun(ctx, token, repository, item, run)
		if err != nil {
			return nil, err
		}
		run.NativeStatus = strings.ToUpper(stringField(original, "status"))
		conclusion, ok := nullableString(original, "conclusion")
		if !ok {
			return nil, queryUnavailable()
		}
		run.NativeConclusion = upperOptional(conclusion)
		run.Jobs, err = c.workflowJobs(ctx, token, repository, run)
		if err != nil {
			return nil, err
		}
		current, err := c.workflowRun(ctx, token, repository, item, run)
		if err != nil || !reflect.DeepEqual(original, current) || run.Validate(ci.TestMerge.CommitSHA) != nil {
			return nil, queryUnavailable()
		}
		result = append(result, run)
	}
	for id, original := range repositories {
		current, err := c.workflowRepository(ctx, token, id)
		if err != nil || !reflect.DeepEqual(original, current) {
			return nil, queryUnavailable()
		}
	}
	return result, nil
}
