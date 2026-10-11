// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type WorkspaceObservation struct {
	Identity   domain.GitHubIdentity
	Repository domain.RemoteRepository
	Nodes      []domain.PRWorkspaceNode
	Edges      []domain.PRWorkspaceEdge
	State      domain.PRWorkspaceState
	Reasons    []string
	Avatars    map[string]string
}

func parseCounts(fields map[string]json.RawMessage) *domain.PRChangeCounts {
	a, aok := exactUnsigned(fields["additions"])
	d, dok := exactUnsigned(fields["deletions"])
	if !aok || !dok {
		return nil
	}
	return &domain.PRChangeCounts{Additions: strconv.FormatUint(a, 10), Deletions: strconv.FormatUint(d, 10)}
}

// Provider locators stay private. Only exact GitHub numeric user avatar paths are
// admitted; a URL, login, image or avatar never establishes actor authority.
func AvatarLocator(value, actorID string) bool {
	u, e := url.Parse(value)
	if e != nil || !domain.PositiveDecimal(actorID) || u.Scheme != "https" || u.Host != "avatars.githubusercontent.com" || u.User != nil || u.Fragment != "" || u.EscapedPath() != "/u/"+actorID {
		return false
	}
	q, e := url.ParseQuery(u.RawQuery)
	if e != nil {
		return false
	}
	for k, v := range q {
		if (k != "v" && k != "s") || len(v) != 1 || !domain.PositiveDecimal(v[0]) {
			return false
		}
	}
	return true
}
func retainAvatar(raw []byte, actor *domain.RepositoryActor, avatars map[string]string) {
	if actor == nil {
		return
	}
	fields, ok := jsonObject(raw)
	if !ok {
		return
	}
	value := stringField(fields, "avatar_url")
	if AvatarLocator(value, actor.ID) {
		avatars[actor.ID] = value
	}
}
func (c *Client) Workspace(ctx context.Context, token []byte, owner, name string, seeds []string) (WorkspaceObservation, error) {
	result := WorkspaceObservation{Nodes: []domain.PRWorkspaceNode{}, Edges: []domain.PRWorkspaceEdge{}, Reasons: []string{}, State: domain.PRWorkspaceComplete, Avatars: map[string]string{}}
	if len(seeds) == 0 || len(seeds) > 20 {
		return result, queryUnavailable()
	}
	first, err := c.QueryRepository(ctx, token, owner, name, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: seeds[0]})
	if err != nil {
		return result, err
	}
	result.Identity, result.Repository = first.Identity, first.Repository
	reads := 3
	seenSeeds := map[string]bool{}
	for _, n := range seeds {
		if !domain.PositiveDecimal(n) || seenSeeds[n] {
			return result, queryUnavailable()
		}
		seenSeeds[n] = true
	}
	reason := func(value string) {
		for _, v := range result.Reasons {
			if v == value {
				return
			}
		}
		result.Reasons = append(result.Reasons, value)
		if result.State != domain.PRWorkspaceAmbiguous {
			result.State = domain.PRWorkspaceIncomplete
		}
	}
	read := func(path string) (readResult, bool) {
		if reads >= 40 || ctx.Err() != nil {
			reason("read_limit")
			return readResult{}, false
		}
		reads++
		v := c.readRepositoryJSON(ctx, token, path)
		if v.state != domain.IntegrationAccessAvailable {
			reason("access_unavailable")
			return v, false
		}
		return v, true
	}
	nodes := map[string]domain.PRWorkspaceNode{}
	queue := append([]string{}, seeds...)
	depth := map[string]int{}
	visited := map[string]bool{}
	detail := func(number string) (domain.PRWorkspaceNode, bool) {
		v, ok := read(repositoryPath(result.Repository) + "/pulls/" + number)
		if !ok {
			return domain.PRWorkspaceNode{}, false
		}
		item, skip, e := parseItem(v.raw, result.Repository, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: number})
		if e != nil || skip {
			reason("identity_unavailable")
			return domain.PRWorkspaceNode{}, false
		}
		fields, _ := jsonObject(v.raw)
		retainAvatar(fields["user"], item.Author, result.Avatars)
		return domain.PRWorkspaceNode{Item: item, Counts: parseCounts(fields), Seed: seenSeeds[number]}, true
	}
	for len(queue) > 0 {
		number := queue[0]
		queue = queue[1:]
		if visited[number] {
			continue
		}
		visited[number] = true
		if len(nodes) >= 100 {
			reason("node_limit")
			break
		}
		if depth[number] > 20 {
			reason("depth_limit")
			continue
		}
		node, ok := detail(number)
		if !ok {
			continue
		}
		nodes[number] = node
		if node.Item.HeadRepository == nil || node.Item.HeadRepository.Repository == nil {
			reason("head_repository_unavailable")
			continue
		}
		// Foreign forks do not acquire same-repository graph edges.
		if node.Item.HeadRepository.Repository.ID != result.Repository.ID {
			continue
		}
		for _, filter := range []url.Values{{"head": {result.Repository.Owner + ":" + node.Item.BaseRef}}, {"base": {node.Item.HeadRef}}} {
			filter.Set("state", "all")
			filter.Set("per_page", "20")
			filter.Set("page", "1")
			for page := uint32(1); ; page++ {
				filter.Set("page", strconv.FormatUint(uint64(page), 10))
				path := repositoryPath(result.Repository) + "/pulls?" + filter.Encode()
				v, ok := read(path)
				if !ok {
					break
				}
				var rows []json.RawMessage
				if domain.Decode(v.raw, &rows) != nil || rows == nil || len(rows) > 20 {
					reason("relation_unavailable")
					break
				}
				for _, raw := range rows {
					fields, ok := jsonObject(raw)
					n, nok := exactUnsigned(fields["number"])
					if !ok || !nok || n == 0 {
						reason("identity_unavailable")
						continue
					}
					other := strconv.FormatUint(n, 10)
					if !visited[other] {
						if old, exists := depth[other]; !exists || depth[number]+1 < old {
							depth[other] = depth[number] + 1
						}
						queue = append(queue, other)
					}
				}
				next, e := queryNextPage(v.link, path, page, result.Repository)
				if e != nil {
					reason("relation_unavailable")
					break
				}
				if next == 0 {
					break
				}
			}
		}
	}
	// Reobserve every retained identity/ref before declaring the graph complete.
	for n, node := range nodes {
		again, ok := detail(n)
		if !ok {
			continue
		}
		if !sameWorkspaceItem(node.Item, again.Item) {
			reason("relationship_drift")
		}
		nodes[n] = again
	}
	numbers := make([]string, 0, len(nodes))
	for n := range nodes {
		numbers = append(numbers, n)
	}
	sort.Slice(numbers, func(i, j int) bool {
		a, _ := strconv.ParseUint(numbers[i], 10, 64)
		b, _ := strconv.ParseUint(numbers[j], 10, 64)
		return a < b
	})
	parents := map[string]int{}
	children := map[string][]string{}
	for _, a := range numbers {
		for _, b := range numbers {
			parent, child := nodes[a].Item, nodes[b].Item
			if a != b && parent.HeadRef == child.BaseRef && parent.HeadRepository != nil && parent.HeadRepository.Repository != nil && parent.HeadRepository.Repository.ID == result.Repository.ID && child.HeadRepository != nil && child.HeadRepository.Repository != nil && child.HeadRepository.Repository.ID == result.Repository.ID {
				result.Edges = append(result.Edges, domain.PRWorkspaceEdge{Parent: a, Child: b})
				parents[b]++
				children[a] = append(children[a], b)
			}
		}
	}
	state := map[string]uint8{}
	var cycle func(string) bool
	cycle = func(n string) bool {
		if state[n] == 1 {
			return true
		}
		if state[n] == 2 {
			return false
		}
		state[n] = 1
		for _, child := range children[n] {
			if cycle(child) {
				return true
			}
		}
		state[n] = 2
		return false
	}
	for _, n := range numbers {
		if parents[n] > 1 {
			reason("competing_parents")
			result.State = domain.PRWorkspaceAmbiguous
		}
		if cycle(n) {
			reason("cycle")
			result.State = domain.PRWorkspaceAmbiguous
		}
		result.Nodes = append(result.Nodes, nodes[n])
	}
	if ctx.Err() != nil {
		return result, domain.SafeError(ctx.Err())
	}
	for _, n := range seeds {
		if _, ok := nodes[n]; !ok {
			reason("seed_unavailable")
		}
	}
	return result, nil
}
func sameWorkspaceItem(a, b domain.RepositoryItem) bool {
	return a.ID == b.ID && a.NodeID == b.NodeID && a.Number == b.Number && a.BaseSHA == b.BaseSHA && a.HeadSHA == b.HeadSHA && a.BaseRef == b.BaseRef && a.HeadRef == b.HeadRef && samePRHeadRepository(a.HeadRepository, b.HeadRepository)
}

const prCommitsGraphQL = `query DeliDevPRCommits($id: ID!, $after: String) { node(id: $id) { ... on PullRequest { id databaseId number baseRefOid headRefOid repository { id databaseId } commits(first: 20, after: $after) { pageInfo { hasNextPage endCursor } nodes { commit { oid message additions deletions author { name date user { id databaseId login __typename avatarUrl } } committer { name date user { id databaseId login __typename avatarUrl } } parents(first:100) { pageInfo { hasNextPage } nodes { oid } } } } } } } }`

type CommitsObservation struct {
	Identity   domain.GitHubIdentity
	Repository domain.RemoteRepository
	Value      domain.PRCommits
	Avatars    map[string]string
}

func (c *Client) Commits(ctx context.Context, token []byte, owner, name, number, id, remoteID, base, head, cursor string) (CommitsObservation, error) {
	r := CommitsObservation{Avatars: map[string]string{}}
	initial, e := c.QueryRepository(ctx, token, owner, name, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: number})
	if e != nil {
		return r, e
	}
	item := initial.Items[0]
	r.Identity, r.Repository = initial.Identity, initial.Repository
	if item.ID != id || initial.Repository.ID != remoteID || item.BaseSHA != base || item.HeadSHA != head {
		return r, domain.Fail(domain.Conflict, "The pull request commit scope changed.", "Reload this pull request explicitly; continuation cannot mix base or head generations.")
	}
	var after any
	if cursor != "" {
		after = cursor
	}
	node, e := c.readGraphQLNode(ctx, token, graphQLCommits, map[string]any{"id": item.NodeID, "after": after})
	if e != nil {
		return r, e
	}
	databaseID, dok := exactUnsigned(node["databaseId"])
	num, nok := exactUnsigned(node["number"])
	repo, rok := jsonObject(node["repository"])
	repoID, ridok := exactUnsigned(repo["databaseId"])
	if !dok || !nok || !rok || !ridok || strconv.FormatUint(databaseID, 10) != id || strconv.FormatUint(num, 10) != number || stringField(node, "id") != item.NodeID || strconv.FormatUint(repoID, 10) != remoteID || stringField(repo, "id") != initial.Repository.NodeID || stringField(node, "baseRefOid") != base || stringField(node, "headRefOid") != head {
		return r, queryUnavailable()
	}
	connection, ok := jsonObject(node["commits"])
	page, pok := jsonObject(connection["pageInfo"])
	more, mok := nullableBool(page, "hasNextPage")
	var rows []json.RawMessage
	if !ok || !pok || !mok || more == nil || domain.Decode(connection["nodes"], &rows) != nil || rows == nil || len(rows) > 20 {
		return r, queryUnavailable()
	}
	r.Value = domain.PRCommits{PullRequestID: id, Number: number, BaseSHA: base, HeadSHA: head, PageToken: cursor, Commits: []domain.PRCommit{}}
	if *more {
		r.Value.NextPageToken = stringField(page, "endCursor")
		if r.Value.NextPageToken == "" || r.Value.NextPageToken == cursor || domain.Text(r.Value.NextPageToken, "cursor", 2048, true) != nil {
			return r, queryUnavailable()
		}
	}
	for _, raw := range rows {
		wrapper, ok := jsonObject(raw)
		f, fok := jsonObject(wrapper["commit"])
		if !ok || !fok {
			return r, queryUnavailable()
		}
		commit := domain.PRCommit{SHA: stringField(f, "oid"), Message: stringField(f, "message"), Counts: parseCounts(f), Parents: []string{}}
		person := func(raw []byte) (domain.PRCommitPerson, bool) {
			f, ok := jsonObject(raw)
			if !ok {
				return domain.PRCommitPerson{}, false
			}
			v := domain.PRCommitPerson{Name: stringField(f, "name")}
			v.Date, e = time.Parse(time.RFC3339Nano, stringField(f, "date"))
			if e != nil {
				return v, false
			}
			if string(f["user"]) != "null" {
				a, aok := jsonObject(f["user"])
				aid, idok := exactUnsigned(a["databaseId"])
				if !aok || !idok {
					return v, false
				}
				kind := domain.RepositoryUnknownActor
				if stringField(a, "__typename") == "User" {
					kind = domain.RepositoryUser
				}
				if stringField(a, "__typename") == "Bot" {
					kind = domain.RepositoryBot
				}
				v.Actor = &domain.RepositoryActor{ProviderType: stringField(a, "__typename"), ID: strconv.FormatUint(aid, 10), NodeID: stringField(a, "id"), Login: stringField(a, "login"), Kind: kind}
				if v.Actor.Validate() != nil {
					return v, false
				}
				locator := stringField(a, "avatarUrl")
				if AvatarLocator(locator, v.Actor.ID) {
					r.Avatars[v.Actor.ID] = locator
				}
			}
			return v, true
		}
		commit.Author, ok = person(f["author"])
		if !ok {
			return r, queryUnavailable()
		}
		commit.Committer, ok = person(f["committer"])
		if !ok {
			return r, queryUnavailable()
		}
		parents, pok := jsonObject(f["parents"])
		pi, piok := jsonObject(parents["pageInfo"])
		pm, pmok := nullableBool(pi, "hasNextPage")
		var ps []json.RawMessage
		if !pok || !piok || !pmok || pm == nil || *pm || domain.Decode(parents["nodes"], &ps) != nil || ps == nil || len(ps) > 100 {
			return r, queryUnavailable()
		}
		for _, p := range ps {
			f, ok := jsonObject(p)
			if !ok {
				return r, queryUnavailable()
			}
			commit.Parents = append(commit.Parents, stringField(f, "oid"))
		}
		r.Value.Commits = append(r.Value.Commits, commit)
	}
	final, e := c.QueryRepository(ctx, token, owner, name, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: number})
	if e != nil {
		return r, e
	}
	if final.Repository.ID != remoteID || !sameWorkspaceItem(item, final.Items[0]) {
		return r, domain.Fail(domain.Conflict, "The pull request changed during commit collection.", "Reload the pull request explicitly.")
	}
	return r, nil
}
