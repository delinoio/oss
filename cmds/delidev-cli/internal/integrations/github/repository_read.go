package github

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type readResult struct {
	raw     []byte
	link    string
	state   domain.IntegrationAccessState
	problem *domain.Error
}

func inaccessible() readResult {
	return readResult{state: domain.IntegrationAccessUnavailable, problem: domain.Fail(domain.Unavailable, "GitHub repository access could not be verified.", "Retry after checking network availability; unavailable data does not establish feature access or successful checks.")}
}

type repositoryRepresentation uint8

const (
	repositoryJSON repositoryRepresentation = iota
	repositoryDiff
)

func (c *Client) readRepositoryJSON(ctx context.Context, token []byte, path string) readResult {
	return c.readRepository(ctx, token, path, repositoryJSON)
}
func (c *Client) readRepository(ctx context.Context, token []byte, path string, representation repositoryRepresentation) readResult {
	userIdentity := representation == repositoryJSON && strings.HasPrefix(path, "/user/") && domain.PositiveDecimal(strings.TrimPrefix(path, "/user/"))
	// Numeric lookup is restricted to the repository itself. Renames must not
	// turn a provider-returned URL into a new transport capability.
	repositoryIdentity := representation == repositoryJSON && strings.HasPrefix(path, "/repositories/") && domain.PositiveDecimal(strings.TrimPrefix(path, "/repositories/"))
	repositoryInventory := representation == repositoryJSON && validRepositoryInventoryPath(path)
	if credentials.ValidatePAT(token) != nil || (!strings.HasPrefix(path, "/repos/") && !strings.HasPrefix(path, "/search/issues?") && !userIdentity && !repositoryIdentity && !repositoryInventory) {
		return inaccessible()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodGet, apiOrigin+path, nil)
	if err != nil {
		return inaccessible()
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	defer req.Header.Del("Authorization")
	accept := "application/vnd.github+json"
	limit := int64(1 << 20)
	switch representation {
	case repositoryJSON:
	case repositoryDiff:
		accept = "application/vnd.github.diff"
		limit = 512 << 10
	default:
		return inaccessible()
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", apiVersion)
	req.Header.Set("User-Agent", "DeliDev/0.1.0")
	reply, err := c.http.Do(req)
	if err != nil {
		return inaccessible()
	}
	defer reply.Body.Close()
	if reply.StatusCode == http.StatusNotFound {
		return readResult{state: domain.IntegrationAccessNotFound, problem: domain.Fail(domain.NotFound, "The GitHub resource is missing or inaccessible.", "Check the exact repository, selected token, organization approval and feature permissions; a 404 does not prove absence.")}
	}
	if reply.StatusCode != http.StatusOK {
		failure := identityFailure(reply)
		states := map[domain.IntegrationValidationState]domain.IntegrationAccessState{domain.IntegrationInvalidToken: domain.IntegrationAccessInvalidToken, domain.IntegrationAccessRestricted: domain.IntegrationAccessDenied, domain.IntegrationSSORequired: domain.IntegrationAccessSSORequired, domain.IntegrationRateLimited: domain.IntegrationAccessRateLimited, domain.IntegrationUnavailable: domain.IntegrationAccessUnavailable}
		problem := *failure.Problem
		problem.Message = "GitHub did not permit this repository feature read."
		return readResult{state: states[failure.State], problem: &problem}
	}
	raw, err := io.ReadAll(io.LimitReader(reply.Body, limit+1))
	if err != nil {
		return inaccessible()
	}
	if int64(len(raw)) > limit {
		if representation == repositoryDiff {
			return readResult{state: domain.IntegrationAccessUnavailable, problem: domain.Fail(domain.ResourceExhausted, "The GitHub diff exceeds its read limit.", "Inspect this comparison in GitHub; no truncated diff is returned.")}
		}
		return readResult{state: domain.IntegrationAccessUnavailable, problem: domain.Fail(domain.ResourceExhausted, "The GitHub response exceeds its read limit.", "Read a smaller result page or narrow the search; no truncated result is returned.")}
	}
	if representation == repositoryDiff {
		contentType, parameters, err := mime.ParseMediaType(reply.Header.Get("Content-Type"))
		if err != nil || (parameters["charset"] != "" && !strings.EqualFold(parameters["charset"], "utf-8")) || (contentType != "application/vnd.github.diff" && contentType != "application/vnd.github.v3.diff") || !utf8.Valid(raw) || strings.ContainsRune(string(raw), '\x00') {
			return inaccessible()
		}
	} else {
		var complete any
		if domain.Decode(raw, &complete) != nil {
			return inaccessible()
		}
	}
	return readResult{raw: raw, link: reply.Header.Get("Link"), state: domain.IntegrationAccessAvailable}
}
func exactUnsigned(raw json.RawMessage) (uint64, bool) {
	var n uint64
	err := json.Unmarshal(raw, &n)
	return n, err == nil && string(raw) != "null"
}
func stringField(fields map[string]json.RawMessage, key string) string {
	var result string
	_ = json.Unmarshal(fields[key], &result)
	return result
}
func jsonObject(raw []byte) (map[string]json.RawMessage, bool) {
	var value map[string]json.RawMessage
	err := domain.Decode(raw, &value)
	return value, err == nil && value != nil
}
func boundedArray(raw []byte, maximum int) bool {
	var rows []map[string]json.RawMessage
	if domain.Decode(raw, &rows) != nil || rows == nil || len(rows) > maximum {
		return false
	}
	for _, row := range rows {
		if row == nil {
			return false
		}
	}
	return true
}
func commitSHA(value string) bool {
	if len(value) != 40 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func parseRepository(raw []byte, owner, name string) (*domain.RemoteRepository, bool) {
	fields, ok := jsonObject(raw)
	if !ok {
		return nil, false
	}
	id, ok := exactUnsigned(fields["id"])
	if !ok || id == 0 {
		return nil, false
	}
	observedOwner, ok := jsonObject(fields["owner"])
	if !ok {
		return nil, false
	}
	ownerName, repoName := stringField(observedOwner, "login"), stringField(fields, "name")
	if domain.ValidateGitHubRepository(ownerName, repoName) != nil || !strings.EqualFold(owner, ownerName) || !strings.EqualFold(name, repoName) || !strings.EqualFold(stringField(fields, "full_name"), ownerName+"/"+repoName) {
		return nil, false
	}
	var private *bool
	if json.Unmarshal(fields["private"], &private) != nil || private == nil {
		return nil, false
	}
	nodeID, branch := stringField(fields, "node_id"), stringField(fields, "default_branch")
	if domain.Text(nodeID, "repository node identity", 256, true) != nil || domain.Text(branch, "default branch", 1024, false) != nil || strings.ContainsAny(branch, "\r\n") {
		return nil, false
	}
	return &domain.RemoteRepository{Provider: domain.GitHubCom, ID: strconv.FormatUint(id, 10), NodeID: nodeID, Owner: ownerName, Name: repoName, Private: *private, DefaultBranch: branch}, true
}
