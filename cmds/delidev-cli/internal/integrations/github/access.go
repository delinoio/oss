package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type RepositoryAccessObservation struct {
	Identity   *domain.GitHubIdentity
	Repository *domain.RemoteRepository
	Features   []domain.IntegrationFeatureAccess
}
type readResult struct {
	raw     []byte
	state   domain.IntegrationAccessState
	problem *domain.Error
}

func inaccessible() readResult {
	return readResult{state: domain.IntegrationAccessUnavailable, problem: domain.Fail(domain.Unavailable, "GitHub repository access could not be verified.", "Retry after checking network availability; unavailable data does not establish feature access or successful checks.")}
}
func (c *Client) readRepositoryJSON(ctx context.Context, token []byte, path string) readResult {
	if credentials.ValidatePAT(token) != nil || !strings.HasPrefix(path, "/repos/") {
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
	req.Header.Set("Accept", "application/vnd.github+json")
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
	raw, err := io.ReadAll(io.LimitReader(reply.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return inaccessible()
	}
	var complete any
	if domain.Decode(raw, &complete) != nil {
		return inaccessible()
	}
	return readResult{raw: raw, state: domain.IntegrationAccessAvailable}
}
func accessFeature(feature domain.IntegrationFeature, read readResult) domain.IntegrationFeatureAccess {
	return domain.IntegrationFeatureAccess{Feature: feature, State: read.state, Problem: read.problem}
}
func unknownFeature(feature domain.IntegrationFeature) domain.IntegrationFeatureAccess {
	return domain.IntegrationFeatureAccess{Feature: feature, State: domain.IntegrationAccessNotEvaluated, Problem: domain.Fail(domain.Unavailable, "This repository feature was not evaluated.", "Verify repository metadata and its default branch before checking this feature.")}
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

// InspectRepository observes the selected repository's read endpoints. It never
// grants future access, infers CI success from availability, or lists unrelated
// repositories. All probes share the original token and a bounded lifetime.
func (c *Client) InspectRepository(ctx context.Context, token []byte, owner, name string) (RepositoryAccessObservation, error) {
	if err := domain.ValidateGitHubRepository(owner, name); err != nil {
		return RepositoryAccessObservation{}, err
	}
	if err := credentials.ValidatePAT(token); err != nil {
		return RepositoryAccessObservation{}, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	result := RepositoryAccessObservation{}
	for _, feature := range domain.IntegrationFeatures() {
		result.Features = append(result.Features, unknownFeature(feature))
	}
	identity, err := c.Identity(bounded, token)
	if err != nil {
		return result, err
	}
	if identity.Identity == nil {
		states := map[domain.IntegrationValidationState]domain.IntegrationAccessState{domain.IntegrationInvalidToken: domain.IntegrationAccessInvalidToken, domain.IntegrationAccessRestricted: domain.IntegrationAccessDenied, domain.IntegrationSSORequired: domain.IntegrationAccessSSORequired, domain.IntegrationRateLimited: domain.IntegrationAccessRateLimited, domain.IntegrationUnavailable: domain.IntegrationAccessUnavailable}
		for i, feature := range domain.IntegrationFeatures() {
			result.Features[i] = domain.IntegrationFeatureAccess{Feature: feature, State: states[identity.State], Problem: identity.Problem}
		}
		return result, nil
	}
	result.Identity = identity.Identity
	base := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
	metadata := c.readRepositoryJSON(bounded, token, base)
	if metadata.state == domain.IntegrationAccessAvailable {
		var ok bool
		result.Repository, ok = parseRepository(metadata.raw, owner, name)
		if !ok {
			metadata = inaccessible()
		}
	}
	result.Features[0] = accessFeature(domain.RepositoryMetadataFeature, metadata)
	if result.Repository == nil {
		if bounded.Err() != nil {
			return result, domain.SafeError(bounded.Err())
		}
		return result, nil
	}
	branch := result.Repository.DefaultBranch
	if branch != "" {
		read := c.readRepositoryJSON(bounded, token, base+"/branches/"+url.PathEscape(branch))
		if read.state == domain.IntegrationAccessAvailable {
			fields, ok := jsonObject(read.raw)
			commit, commitOK := jsonObject(fields["commit"])
			sha := stringField(commit, "sha")
			if !ok || !commitOK || stringField(fields, "name") != branch || !commitSHA(sha) {
				read = inaccessible()
			} else {
				result.Repository.HeadCommit = sha
			}
		}
		result.Features[1] = accessFeature(domain.RepositoryContentsFeature, read)
	}
	type probe struct {
		index    int
		path     string
		validate func([]byte) bool
	}
	probes := []probe{
		{2, base + "/pulls?state=all&per_page=1", func(raw []byte) bool { return boundedArray(raw, 1) }},
		{3, base + "/issues?state=all&per_page=1", func(raw []byte) bool { return boundedArray(raw, 1) }},
		{7, base + "/collaborators/" + url.PathEscape(identity.Identity.Login) + "/permission", func(raw []byte) bool {
			fields, ok := jsonObject(raw)
			user, userOK := jsonObject(fields["user"])
			id, idOK := exactUnsigned(user["id"])
			permission := stringField(fields, "permission")
			return ok && userOK && idOK && strconv.FormatUint(id, 10) == identity.Identity.ID && stringField(user, "node_id") == identity.Identity.NodeID && strings.EqualFold(stringField(user, "login"), identity.Identity.Login) && (permission == "admin" || permission == "write" || permission == "read" || permission == "none")
		}},
	}
	if branch != "" {
		probes = append(probes, probe{6, base + "/rules/branches/" + url.PathEscape(branch) + "?per_page=1", func(raw []byte) bool { return boundedArray(raw, 1) }})
	}
	if sha := result.Repository.HeadCommit; sha != "" {
		probes = append(probes, probe{4, base + "/commits/" + sha + "/check-runs?per_page=1", func(raw []byte) bool {
			fields, ok := jsonObject(raw)
			total, totalOK := exactUnsigned(fields["total_count"])
			var rows []map[string]json.RawMessage
			return ok && totalOK && domain.Decode(fields["check_runs"], &rows) == nil && boundedArray(fields["check_runs"], 1) && uint64(len(rows)) <= total
		}}, probe{5, base + "/commits/" + sha + "/status?per_page=1", func(raw []byte) bool {
			fields, ok := jsonObject(raw)
			state := stringField(fields, "state")
			return ok && stringField(fields, "sha") == sha && boundedArray(fields["statuses"], 1) && (state == "pending" || state == "success" || state == "failure")
		}})
	}
	jobs := make(chan probe, len(probes))
	for _, probe := range probes {
		jobs <- probe
	}
	close(jobs)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			for probe := range jobs {
				if bounded.Err() != nil {
					return
				}
				read := c.readRepositoryJSON(bounded, token, probe.path)
				if read.state == domain.IntegrationAccessAvailable && !probe.validate(read.raw) {
					read = inaccessible()
				}
				result.Features[probe.index] = accessFeature(domain.IntegrationFeatures()[probe.index], read)
			}
		}()
	}
	group.Wait()
	if bounded.Err() != nil {
		return result, domain.SafeError(bounded.Err())
	}
	return result, nil
}
