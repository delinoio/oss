package github

import (
	"context"
	"encoding/json"
	"strconv"

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
    ... on CheckRun { id name status conclusion startedAt completedAt title summary text isRequired(pullRequestId: $id)
      repository { id }
      checkSuite { id commit { oid } app { databaseId id slug } workflowRun { id event runNumber runAttempt createdAt updatedAt } }
    }
    ... on StatusContext { id context state createdAt updatedAt description isRequired(pullRequestId: $id) commit { oid } }
  }
}`

func (c *Client) readCIPage(ctx context.Context, token []byte, id string, headAfter, mergeAfter *string) (map[string]json.RawMessage, error) {
	return c.readGraphQLNode(ctx, token, graphQLCI, map[string]any{"id": id, "headAfter": headAfter, "mergeAfter": mergeAfter})
}

type ciPage struct {
	rollup domain.CIRollup
	total  uint64
	next   *string
}

func parseCIContext(raw []byte, repository domain.RemoteRepository, sha string) (domain.CIContext, error) {
	f, ok := jsonObject(raw)
	required, requiredOK := nullableBool(f, "isRequired")
	v := domain.CIContext{NodeID: stringField(f, "id"), Evidence: &domain.CIContextEvidence{}}
	if !ok || !requiredOK || required == nil {
		return v, queryUnavailable()
	}
	v.Required = *required
	e := v.Evidence
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
		e.SuiteNodeID = stringField(suite, "id")
		e.StartedAt, ok = nullableTime(f, "startedAt")
		if !ok {
			return v, queryUnavailable()
		}
		e.CompletedAt, ok = nullableTime(f, "completedAt")
		if !ok {
			return v, queryUnavailable()
		}
		for key, dest := range map[string]**string{"title": &e.Title, "summary": &e.Summary, "text": &e.Text} {
			*dest, ok = nullableString(f, key)
			if !ok {
				return v, queryUnavailable()
			}
		}
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
			number, numberOK := exactUnsigned(workflow["runNumber"])
			attempt, attemptOK := exactUnsigned(workflow["runAttempt"])
			created, createdOK := nullableTime(workflow, "createdAt")
			updated, updatedOK := nullableTime(workflow, "updatedAt")
			if !numberOK || !attemptOK || number == 0 || attempt == 0 || number > 2147483647 || attempt > 2147483647 || !createdOK || !updatedOK || created == nil || updated == nil {
				return v, queryUnavailable()
			}
			e.Workflow = &domain.CIWorkflowEvidence{NodeID: stringField(workflow, "id"), RunNumber: strconv.FormatUint(number, 10), ObservedAttempt: strconv.FormatUint(attempt, 10), CreatedAt: *created, UpdatedAt: *updated}
		}
	case "StatusContext":
		e.CreatedAt, ok = nullableTime(f, "createdAt")
		if !ok || e.CreatedAt == nil {
			return v, queryUnavailable()
		}
		e.UpdatedAt, ok = nullableTime(f, "updatedAt")
		if !ok || e.UpdatedAt == nil {
			return v, queryUnavailable()
		}
		e.Description, ok = nullableString(f, "description")
		if !ok {
			return v, queryUnavailable()
		}
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
