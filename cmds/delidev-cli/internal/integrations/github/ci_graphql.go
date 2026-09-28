package github

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// A fixed read-only document is the entire GraphQL capability. There is no
// arbitrary document, mutation, endpoint, credential fallback or URL following.
const ciGraphQL = `query DeliDevRequiredCI($id: ID!, $headAfter: String, $mergeAfter: String) {
  node(id: $id) { ... on PullRequest {
    id number state merged baseRefName baseRefOid headRefName headRefOid mergeable isInMergeQueue
    repository { id }
    statusCheckRollup { commit { oid } contexts(first: 100, after: $headAfter) { ...DeliDevContexts } }
    potentialMergeCommit { oid parents(first: 3) { totalCount nodes { oid } }
      statusCheckRollup { commit { oid } contexts(first: 100, after: $mergeAfter) { ...DeliDevContexts } }
    }
  } }
}
fragment DeliDevContexts on StatusCheckRollupContextConnection {
  totalCount pageInfo { hasNextPage endCursor }
  nodes {
    __typename
    ... on CheckRun { id name status conclusion isRequired(pullRequestId: $id)
      repository { id }
      checkSuite { commit { oid } app { databaseId id slug } workflowRun { event } }
    }
    ... on StatusContext { id context state isRequired(pullRequestId: $id) commit { oid } }
  }
}`

func (c *Client) readCIPage(ctx context.Context, token []byte, id string, headAfter, mergeAfter *string) (map[string]json.RawMessage, error) {
	if credentials.ValidatePAT(token) != nil || domain.Text(id, "PR node identity", 256, true) != nil {
		return nil, queryUnavailable()
	}
	body, _ := json.Marshal(map[string]any{"query": ciGraphQL, "operationName": "DeliDevRequiredCI", "variables": map[string]any{"id": id, "headAfter": headAfter, "mergeAfter": mergeAfter}})
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodPost, apiOrigin+"/graphql", bytes.NewReader(body))
	if err != nil {
		return nil, queryUnavailable()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DeliDev/0.1.0")
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("Authorization", "Bearer "+string(token))
	defer req.Header.Del("Authorization")
	reply, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, domain.SafeError(ctx.Err())
		}
		return nil, queryUnavailable()
	}
	defer reply.Body.Close()
	if reply.StatusCode != http.StatusOK {
		return nil, identityFailure(reply).Problem
	}
	raw, err := io.ReadAll(io.LimitReader(reply.Body, (1<<20)+1))
	if err != nil {
		return nil, queryUnavailable()
	}
	if len(raw) > 1<<20 {
		return nil, domain.Fail(domain.ResourceExhausted, "The CI response exceeds its complete read limit.", "Inspect checks in GitHub; no truncated CI evidence is returned.")
	}
	envelope, ok := jsonObject(raw)
	if !ok {
		return nil, queryUnavailable()
	}
	if errors, exists := envelope["errors"]; exists {
		var entries []json.RawMessage
		if domain.Decode(errors, &entries) != nil {
			return nil, queryUnavailable()
		}
		if len(entries) != 0 {
			return nil, ciGraphQLProblem(entries)
		}
	}
	data, ok := jsonObject(envelope["data"])
	if !ok {
		return nil, queryUnavailable()
	}
	node, ok := jsonObject(data["node"])
	if !ok {
		return nil, queryUnavailable()
	}
	return node, nil
}

func ciGraphQLProblem(entries []json.RawMessage) error {
	// GitHub may return a partial data object alongside these errors. Never
	// retain it, a remote diagnostic message, or an authorization inference from it.
	for _, raw := range entries {
		entry, ok := jsonObject(raw)
		if !ok {
			return queryUnavailable()
		}
		switch stringField(entry, "type") {
		case "FORBIDDEN", "INSUFFICIENT_SCOPES":
			return domain.Fail(domain.PermissionDenied, "GitHub did not permit the complete CI evidence read.", "Check this selected profile's repository and feature permissions plus organization approval; no other token is selected.")
		case "RATE_LIMITED":
			return domain.Fail(domain.ResourceExhausted, "GitHub rate-limited the CI evidence read.", "Wait for the applicable rate limit to reset, then refresh; no partial result authorizes automatic handling.")
		case "NOT_FOUND":
			return domain.Fail(domain.NotFound, "The GitHub PR is missing or inaccessible.", "Check the current repository and selected token; this response does not prove absence.")
		}
	}
	return queryUnavailable()
}

type ciPage struct {
	rollup domain.CIRollup
	total  uint64
	next   *string
}

func parseCIContext(raw []byte, repository domain.RemoteRepository, sha string) (domain.CIContext, error) {
	f, ok := jsonObject(raw)
	required, requiredOK := nullableBool(f, "isRequired")
	v := domain.CIContext{NodeID: stringField(f, "id")}
	if !ok || !requiredOK || required == nil {
		return v, queryUnavailable()
	}
	v.Required = *required
	switch stringField(f, "__typename") {
	case "CheckRun":
		v.Kind, v.Name, v.NativeStatus = domain.CICheckRun, stringField(f, "name"), stringField(f, "status")
		v.NativeConclusion, ok = nullableString(f, "conclusion")
		if !ok {
			return v, queryUnavailable()
		}
		repo, repoOK := jsonObject(f["repository"])
		suite, suiteOK := jsonObject(f["checkSuite"])
		commit, commitOK := jsonObject(suite["commit"])
		if !repoOK || stringField(repo, "id") != repository.NodeID || !suiteOK || !commitOK {
			return v, queryUnavailable()
		}
		v.CommitSHA = stringField(commit, "oid")
		appRaw, exists := suite["app"]
		if !exists {
			return v, queryUnavailable()
		}
		if string(appRaw) != "null" {
			app, ok := jsonObject(appRaw)
			id, idOK := exactUnsigned(app["databaseId"])
			if !ok || !idOK {
				return v, queryUnavailable()
			}
			v.Application = &domain.CheckApplication{ID: strconv.FormatUint(id, 10), NodeID: stringField(app, "id"), Slug: stringField(app, "slug")}
		}
		workflowRaw, exists := suite["workflowRun"]
		if !exists {
			return v, queryUnavailable()
		}
		if string(workflowRaw) != "null" {
			workflow, ok := jsonObject(workflowRaw)
			if !ok {
				return v, queryUnavailable()
			}
			event := stringField(workflow, "event")
			v.WorkflowEvent = &event
		}
	case "StatusContext":
		v.Kind, v.Name, v.NativeStatus = domain.CICommitStatus, stringField(f, "context"), stringField(f, "state")
		commit, ok := jsonObject(f["commit"])
		if !ok {
			return v, queryUnavailable()
		}
		v.CommitSHA = stringField(commit, "oid")
	default:
		return v, queryUnavailable()
	}
	if v.Validate(sha) != nil {
		return v, queryUnavailable()
	}
	return v, nil
}
func parseCIRollup(raw []byte, repository domain.RemoteRepository, sha string) (ciPage, error) {
	result := ciPage{rollup: domain.CIRollup{CommitSHA: sha, TotalCount: "0", Contexts: []domain.CIContext{}}}
	if string(raw) == "null" {
		return result, nil
	}
	f, ok := jsonObject(raw)
	commit, commitOK := jsonObject(f["commit"])
	contexts, contextsOK := jsonObject(f["contexts"])
	total, totalOK := exactUnsigned(contexts["totalCount"])
	page, pageOK := jsonObject(contexts["pageInfo"])
	hasNext, nextOK := nullableBool(page, "hasNextPage")
	cursor, cursorOK := nullableString(page, "endCursor")
	var rows []json.RawMessage
	if !ok || !commitOK || stringField(commit, "oid") != sha || !contextsOK || !totalOK || !pageOK || !nextOK || hasNext == nil || !cursorOK || domain.Decode(contexts["nodes"], &rows) != nil || rows == nil || len(rows) > 100 || uint64(len(rows)) > total {
		return result, queryUnavailable()
	}
	if total > domain.MaxCIContexts {
		return result, domain.Fail(domain.ResourceExhausted, "The CI inventory exceeds its complete read limit.", "Inspect checks in GitHub; no partial CI result is returned.")
	}
	if *hasNext {
		if cursor == nil || domain.Text(*cursor, "CI page cursor", 1024, true) != nil || len(rows) == 0 {
			return result, queryUnavailable()
		}
		result.next = cursor
	}
	result.total = total
	result.rollup.TotalCount = strconv.FormatUint(total, 10)
	for _, raw := range rows {
		value, err := parseCIContext(raw, repository, sha)
		if err != nil {
			return result, err
		}
		result.rollup.Contexts = append(result.rollup.Contexts, value)
	}
	return result, nil
}
