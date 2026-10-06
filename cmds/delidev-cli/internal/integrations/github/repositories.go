// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type RepositoryListObservation struct {
	Repositories []domain.GitHubRepositoryListEntry
	NextPage     uint32
}

func repositoryInventoryPath(page, size uint32) string {
	return "/user/repos?direction=asc&sort=full_name&per_page=" + strconv.FormatUint(uint64(size), 10) + "&page=" + strconv.FormatUint(uint64(page), 10)
}
func validRepositoryInventoryPath(path string) bool {
	parsed, err := url.Parse(path)
	if err != nil || parsed.Path != "/user/repos" || parsed.Scheme != "" || parsed.Host != "" || parsed.Fragment != "" {
		return false
	}
	values, err := url.ParseQuery(parsed.RawQuery)
	page, e1 := strconv.ParseUint(values.Get("page"), 10, 32)
	size, e2 := strconv.ParseUint(values.Get("per_page"), 10, 32)
	return err == nil && e1 == nil && e2 == nil && domain.ValidateGitHubRepositoryPageInput(uint32(page), uint32(size)) == nil && path == repositoryInventoryPath(uint32(page), uint32(size))
}

// ListRepositories reads Metadata only. Provider-returned URLs, tokens and
// temporary clone tokens are never projected or used as transport authority.
func (c *Client) ListRepositories(ctx context.Context, token []byte, page, size uint32) (RepositoryListObservation, error) {
	result := RepositoryListObservation{Repositories: []domain.GitHubRepositoryListEntry{}}
	if err := domain.ValidateGitHubRepositoryPageInput(page, size); err != nil {
		return result, err
	}
	path := repositoryInventoryPath(page, size)
	read := c.readRepositoryJSON(ctx, token, path)
	if ctx.Err() != nil {
		return result, domain.SafeError(ctx.Err())
	}
	if read.state != domain.IntegrationAccessAvailable {
		return result, readProblem(read)
	}
	var rows []json.RawMessage
	if domain.Decode(read.raw, &rows) != nil || rows == nil || len(rows) > int(size) {
		return result, queryUnavailable()
	}
	ids, nodes, names := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, raw := range rows {
		fields, ok := jsonObject(raw)
		owner, ownerOK := jsonObject(fields["owner"])
		r, valid := parseRepository(raw, stringField(owner, "login"), stringField(fields, "name"))
		var archived *bool
		if !ok || !ownerOK || !valid || json.Unmarshal(fields["archived"], &archived) != nil || archived == nil {
			return RepositoryListObservation{}, queryUnavailable()
		}
		entry := domain.GitHubRepositoryListEntry{Repository: *r, Archived: *archived, HTTPSURL: "https://github.com/" + r.Owner + "/" + r.Name + ".git", SSHURL: "git@github.com:" + r.Owner + "/" + r.Name + ".git"}
		name := strings.ToLower(r.Owner + "/" + r.Name)
		if entry.Validate() != nil || ids[r.ID] || nodes[r.NodeID] || names[name] {
			return RepositoryListObservation{}, queryUnavailable()
		}
		ids[r.ID], nodes[r.NodeID], names[name] = true, true, true
		result.Repositories = append(result.Repositories, entry)
	}
	next, err := queryNextPage(read.link, path, page, domain.RemoteRepository{})
	if err != nil || next > 1000000 {
		return RepositoryListObservation{}, queryUnavailable()
	}
	result.NextPage = next
	return result, nil
}
