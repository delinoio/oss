package github

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func (c *Client) workspaceRepository(ctx context.Context, token []byte, owner, name string) (domain.RemoteRepository, error) {
	if domain.ValidateGitHubRepository(owner, name) != nil {
		return domain.RemoteRepository{}, queryUnavailable()
	}
	identity, err := c.Identity(ctx, token)
	if err != nil {
		return domain.RemoteRepository{}, err
	}
	if identity.Identity == nil {
		return domain.RemoteRepository{}, identity.Problem
	}
	read := c.readRepositoryJSON(ctx, token, "/repos/"+url.PathEscape(owner)+"/"+url.PathEscape(name))
	if read.problem != nil {
		return domain.RemoteRepository{}, readProblem(read)
	}
	repo, ok := parseRepository(read.raw, owner, name)
	if !ok {
		return domain.RemoteRepository{}, queryUnavailable()
	}
	return *repo, nil
}
func parseCounts(fields map[string]json.RawMessage) (*domain.PRChangeCounts, error) {
	a, hasA := fields["additions"]
	d, hasD := fields["deletions"]
	if !hasA && !hasD {
		return nil, nil
	}
	additions, okA := exactUnsigned(a)
	deletions, okD := exactUnsigned(d)
	if !okA || !okD || additions > 1<<53-1 || deletions > 1<<53-1 {
		return nil, queryUnavailable()
	}
	return &domain.PRChangeCounts{Additions: additions, Deletions: deletions}, nil
}
func (c *Client) workspaceRow(ctx context.Context, token []byte, repo domain.RemoteRepository, number string) (domain.PRWorkspaceRow, error) {
	read := c.readRepositoryJSON(ctx, token, repositoryPath(repo)+"/pulls/"+number)
	if read.problem != nil {
		return domain.PRWorkspaceRow{}, readProblem(read)
	}
	item, _, err := parseItem(read.raw, repo, domain.RepositoryQuery{Kind: domain.RepositoryPullRequest, Operation: domain.RepositoryDetail, Number: number})
	if err != nil {
		return domain.PRWorkspaceRow{}, err
	}
	fields, _ := jsonObject(read.raw)
	counts, err := parseCounts(fields)
	if err != nil {
		return domain.PRWorkspaceRow{}, err
	}
	locator := ""
	if item.Author != nil && item.Author.Kind != domain.RepositoryUnknownActor {
		actor, _ := jsonObject(fields["user"])
		candidate := stringField(actor, "avatar_url")
		if validAvatarLocator(candidate, item.Author.ID) {
			locator = candidate
		}
	}
	return domain.PRWorkspaceRow{Item: item, Counts: counts, AvatarLocator: locator}, nil
}

// The graph is authoritative only after all matching branches and exact PR
// operands have been rechecked. Incomplete reads retain selectable seed rows.
func (c *Client) PullRequestWorkspace(ctx context.Context, token []byte, owner, name string, seeds []uint32) (domain.PRWorkspace, error) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repo, err := c.workspaceRepository(bounded, token, owner, name)
	if err != nil {
		return domain.PRWorkspace{}, err
	}
	result := domain.PRWorkspace{Repository: repo, State: domain.PRWorkspaceComplete, Rows: []domain.PRWorkspaceRow{}, Edges: []domain.PRWorkspaceEdge{}}
	rows := map[string]domain.PRWorkspaceRow{}
	order := []string{}
	depth := map[string]int{}
	seedSet := map[string]bool{}
	for _, seed := range seeds {
		number := strconv.FormatUint(uint64(seed), 10)
		seedSet[number] = true
		row, e := c.workspaceRow(bounded, token, repo, number)
		if e != nil {
			result.State = domain.PRWorkspaceIncomplete
			continue
		}
		row.Seed = true
		rows[number] = row
		order = append(order, number)
	}
	pages := 0
	type pageSnapshot struct {
		path string
		raw  []byte
		link string
	}
	snapshots := []pageSnapshot{}
	readBranches := map[string]bool{}
	for index := 0; index < len(order); index++ {
		number := order[index]
		row := rows[number]
		if row.Item.HeadRepository == nil || row.Item.HeadRepository.State != domain.PRHeadRepositoryAvailable {
			result.State = domain.PRWorkspaceIncomplete
			continue
		}
		// Cross-fork heads cannot establish same-repository branch ancestry.
		branches := [][2]string{{"head", repo.Owner + ":" + row.Item.BaseRef}}
		if row.Item.HeadRepository.Repository != nil && row.Item.HeadRepository.Repository.ID == repo.ID {
			branches = append(branches, [2]string{"base", row.Item.HeadRef})
		}
		for _, branch := range branches {
			key := branch[0] + ":" + branch[1]
			if readBranches[key] {
				continue
			}
			readBranches[key] = true
			page := uint32(1)
			for page != 0 {
				if pages >= 40 {
					result.State = domain.PRWorkspaceIncomplete
					break
				}
				pages++
				values := url.Values{"state": {"all"}, "per_page": {"20"}, "page": {strconv.Itoa(int(page))}, branch[0]: {branch[1]}}
				path := repositoryPath(repo) + "/pulls?" + values.Encode()
				read := c.readRepositoryJSON(bounded, token, path)
				var candidates []json.RawMessage
				if read.problem != nil || domain.Decode(read.raw, &candidates) != nil || candidates == nil || len(candidates) > 20 {
					result.State = domain.PRWorkspaceIncomplete
					break
				}
				snapshots = append(snapshots, pageSnapshot{path: path, raw: append([]byte(nil), read.raw...), link: read.link})
				for _, raw := range candidates {
					fields, ok := jsonObject(raw)
					n, numberOK := exactUnsigned(fields["number"])
					if !ok || !numberOK || n == 0 {
						result.State = domain.PRWorkspaceIncomplete
						continue
					}
					candidate := strconv.FormatUint(n, 10)
					if _, exists := rows[candidate]; exists {
						continue
					}
					if len(rows) >= 100 || depth[number] >= 20 {
						result.State = domain.PRWorkspaceIncomplete
						continue
					}
					related, e := c.workspaceRow(bounded, token, repo, candidate)
					if e != nil {
						result.State = domain.PRWorkspaceIncomplete
						continue
					}
					// Independently read refs must still satisfy the original discovery filter.
					matches := related.Item.BaseRef == row.Item.HeadRef && branch[0] == "base"
					if branch[0] == "head" {
						matches = related.Item.HeadRef == row.Item.BaseRef && sameHeadRepository(related.Item, repo)
					}
					if !matches {
						result.State = domain.PRWorkspaceIncomplete
						continue
					}
					related.Seed = seedSet[candidate]
					rows[candidate] = related
					order = append(order, candidate)
					depth[candidate] = depth[number] + 1
				}
				next, e := queryNextPage(read.link, path, page, repo)
				if e != nil {
					result.State = domain.PRWorkspaceIncomplete
					break
				}
				page = next
			}
		}
	}

	if result.State == domain.PRWorkspaceComplete {
		for _, snapshot := range snapshots {
			if pages >= 40 {
				result.State = domain.PRWorkspaceIncomplete
				break
			}
			pages++
			again := c.readRepositoryJSON(bounded, token, snapshot.path)
			if again.problem != nil || !bytes.Equal(again.raw, snapshot.raw) || again.link != snapshot.link {
				result.State = domain.PRWorkspaceIncomplete
				break
			}
		}
	}
	for _, number := range order {
		row := rows[number]
		again, e := c.workspaceRow(bounded, token, repo, number)
		if e != nil || !reflect.DeepEqual(row.Item, again.Item) || !reflect.DeepEqual(row.Counts, again.Counts) {
			result.State = domain.PRWorkspaceIncomplete
		}
		result.Rows = append(result.Rows, row)
	}
	// A same-name branch read with different exact operands is drift, not a
	// complete stack observation. Keep its rows without claiming a tree.
	for _, parent := range result.Rows {
		for _, child := range result.Rows {
			if parent.Item.Number != child.Item.Number && sameHeadRepository(parent.Item, repo) && parent.Item.HeadRef == child.Item.BaseRef && parent.Item.HeadSHA != child.Item.BaseSHA {
				result.State = domain.PRWorkspaceIncomplete
			}
		}
	}
	edges, ambiguous := workspaceEdges(result.Rows, repo)
	result.Edges = edges
	if ambiguous {
		result.State = domain.PRWorkspaceAmbiguous
	}
	return result, nil
}
func sameHeadRepository(item domain.RepositoryItem, repo domain.RemoteRepository) bool {
	return item.HeadRepository != nil && item.HeadRepository.Repository != nil && item.HeadRepository.Repository.ID == repo.ID
}
func workspaceEdges(rows []domain.PRWorkspaceRow, repo domain.RemoteRepository) ([]domain.PRWorkspaceEdge, bool) {
	edges := []domain.PRWorkspaceEdge{}
	parents := map[string]int{}
	children := map[string][]string{}
	for _, parent := range rows {
		if !sameHeadRepository(parent.Item, repo) {
			continue
		}
		for _, child := range rows {
			if parent.Item.Number != child.Item.Number && parent.Item.HeadRef == child.Item.BaseRef {
				edges = append(edges, domain.PRWorkspaceEdge{Parent: parent.Item.Number, Child: child.Item.Number})
				parents[child.Item.Number]++
				children[parent.Item.Number] = append(children[parent.Item.Number], child.Item.Number)
			}
		}
	}
	ambiguous := false
	for _, count := range parents {
		if count > 1 {
			ambiguous = true
		}
	}
	visited := map[string]int{}
	var visit func(string)
	visit = func(n string) {
		if visited[n] == 1 {
			ambiguous = true
			return
		}
		if visited[n] == 2 {
			return
		}
		visited[n] = 1
		for _, child := range children[n] {
			visit(child)
		}
		visited[n] = 2
	}
	for _, row := range rows {
		visit(row.Item.Number)
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Parent == edges[j].Parent {
			return edges[i].Child < edges[j].Child
		}
		return edges[i].Parent < edges[j].Parent
	})
	return edges, ambiguous
}
