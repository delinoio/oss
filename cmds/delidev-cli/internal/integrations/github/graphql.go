package github

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"net/http"
	"time"
)

type graphQLRead uint8

const (
	graphQLCI graphQLRead = iota + 1
	graphQLReviews
	graphQLConversation
	graphQLThreads
	graphQLThreadComments
)

func graphQLDocument(operation graphQLRead) (string, string) {
	switch operation {
	case graphQLCI:
		return ciGraphQL, "DeliDevRequiredCI"
	case graphQLReviews:
		return feedbackReviewsGraphQL, "DeliDevPRReviews"
	case graphQLConversation:
		return feedbackConversationGraphQL, "DeliDevPRConversation"
	case graphQLThreads:
		return feedbackThreadsGraphQL, "DeliDevPRThreads"
	case graphQLThreadComments:
		return feedbackThreadCommentsGraphQL, "DeliDevPRThreadComments"
	default:
		return "", ""
	}
}

func (c *Client) readGraphQLNode(ctx context.Context, token []byte, operation graphQLRead, variables map[string]any) (map[string]json.RawMessage, error) {
	document, name := graphQLDocument(operation)
	id, _ := variables["id"].(string)
	if document == "" || credentials.ValidatePAT(token) != nil || domain.Text(id, "node identity", 256, true) != nil {
		return nil, queryUnavailable()
	}
	body, _ := json.Marshal(map[string]any{"query": document, "operationName": name, "variables": variables})
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
		return nil, domain.Fail(domain.ResourceExhausted, "The GitHub response exceeds its complete read limit.", "Inspect the complete result in GitHub; no truncated evidence is returned.")
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
			return nil, graphQLReadProblem(entries)
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

func graphQLReadProblem(entries []json.RawMessage) error {
	// GitHub may return a partial data object alongside these errors. Never
	// retain it, a remote diagnostic message, or an authorization inference from it.
	for _, raw := range entries {
		entry, ok := jsonObject(raw)
		if !ok {
			return queryUnavailable()
		}
		switch stringField(entry, "type") {
		case "FORBIDDEN", "INSUFFICIENT_SCOPES":
			return domain.Fail(domain.PermissionDenied, "GitHub did not permit the complete evidence read.", "Check this selected profile's repository and feature permissions plus organization approval; no other token is selected.")
		case "RATE_LIMITED":
			return domain.Fail(domain.ResourceExhausted, "GitHub rate-limited the evidence read.", "Wait for the applicable rate limit to reset, then refresh; no partial result authorizes automatic handling.")
		case "NOT_FOUND":
			return domain.Fail(domain.NotFound, "The GitHub PR is missing or inaccessible.", "Check the current repository and selected token; this response does not prove absence.")
		}
	}
	return queryUnavailable()
}
