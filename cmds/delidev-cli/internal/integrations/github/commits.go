package github

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const commitsGraphQL = `query DeliDevPRCommits($id: ID!, $after: String) {
 node(id:$id) { ... on PullRequest { id number state merged baseRefName baseRefOid headRefName headRefOid repository { id }
 commits(first:20,after:$after) { pageInfo { hasNextPage endCursor } nodes { commit {
 oid message additions deletions authoredDate committedDate
 author { name user { id databaseId login avatarUrl } } committer { name }
 parents(first:100) { pageInfo { hasNextPage } nodes { oid } }
 } } } } }
}`

func (c *Client) PullRequestCommits(ctx context.Context, token []byte, owner, name string, number uint32, base, head, after string) (domain.PRCommitPage, string, error) {
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repo, err := c.workspaceRepository(bounded, token, owner, name)
	if err != nil {
		return domain.PRCommitPage{}, "", err
	}
	row, err := c.workspaceRow(bounded, token, repo, strconv.FormatUint(uint64(number), 10))
	if err != nil {
		return domain.PRCommitPage{}, "", err
	}
	if row.Item.BaseSHA != base || row.Item.HeadSHA != head {
		return domain.PRCommitPage{}, "", domain.Fail(domain.CursorExpired, "The PR operands changed.", "Reload commits for the current PR.")
	}
	variables := map[string]any{"id": row.Item.NodeID, "after": nil}
	if after != "" {
		variables["after"] = after
	}
	node, err := c.readGraphQLNode(bounded, token, graphQLCommits, variables)
	if err != nil {
		return domain.PRCommitPage{}, "", err
	}
	if validateFeedbackPR(node, repo, row.Item) != nil {
		return domain.PRCommitPage{}, "", queryUnavailable()
	}
	connection, ok := jsonObject(node["commits"])
	info, infoOK := jsonObject(connection["pageInfo"])
	hasNext, validNext := nullableBool(info, "hasNextPage")
	var nodes []json.RawMessage
	if !ok || !infoOK || !validNext || hasNext == nil || domain.Decode(connection["nodes"], &nodes) != nil || nodes == nil || len(nodes) > 20 {
		return domain.PRCommitPage{}, "", queryUnavailable()
	}
	result := domain.PRCommitPage{Repository: repo, Item: row.Item, Commits: []domain.PRCommit{}}
	seen := map[string]bool{}
	for _, raw := range nodes {
		outer, ok := jsonObject(raw)
		fields, fieldsOK := jsonObject(outer["commit"])
		if !ok || !fieldsOK {
			return result, "", queryUnavailable()
		}
		commit := domain.PRCommit{SHA: stringField(fields, "oid"), Message: stringField(fields, "message"), Parents: []string{}}
		if !domain.PRWorkspaceSHA(commit.SHA) || seen[commit.SHA] || domain.Text(commit.Message, "commit message", 1<<20, false) != nil {
			return result, "", queryUnavailable()
		}
		seen[commit.SHA] = true
		counts, e := parseCounts(fields)
		if e != nil {
			return result, "", e
		}
		commit.Counts = counts
		commit.AuthoredAt, e = time.Parse(time.RFC3339Nano, stringField(fields, "authoredDate"))
		if e != nil {
			return result, "", queryUnavailable()
		}
		commit.CommittedAt, e = time.Parse(time.RFC3339Nano, stringField(fields, "committedDate"))
		if e != nil {
			return result, "", queryUnavailable()
		}
		author, aok := jsonObject(fields["author"])
		committer, cok := jsonObject(fields["committer"])
		if !aok || !cok {
			return result, "", queryUnavailable()
		}
		commit.Author = stringField(author, "name")
		commit.Committer = stringField(committer, "name")
		user, uok := jsonObject(author["user"])
		if uok {
			id, valid := exactUnsigned(user["databaseId"])
			if valid && id > 0 {
				commit.AvatarActor = strconv.FormatUint(id, 10)
				locator := stringField(user, "avatarUrl")
				if validAvatarLocator(locator, commit.AvatarActor) {
					commit.AvatarLocator = locator
				}
			}
		}
		parents, pok := jsonObject(fields["parents"])
		pinfo, piok := jsonObject(parents["pageInfo"])
		more, moreOK := nullableBool(pinfo, "hasNextPage")
		var entries []json.RawMessage
		if !pok || !piok || !moreOK || more == nil || *more || domain.Decode(parents["nodes"], &entries) != nil || len(entries) > 100 {
			return result, "", queryUnavailable()
		}
		for _, entry := range entries {
			parent, ok := jsonObject(entry)
			sha := stringField(parent, "oid")
			if !ok || !domain.PRWorkspaceSHA(sha) {
				return result, "", queryUnavailable()
			}
			commit.Parents = append(commit.Parents, sha)
		}
		result.Commits = append(result.Commits, commit)
	}
	next := ""
	if *hasNext {
		next = stringField(info, "endCursor")
		if next == "" || next == after || len(next) > 2048 || len(nodes) == 0 {
			return result, "", queryUnavailable()
		}
	}
	again, e := c.workspaceRow(bounded, token, repo, row.Item.Number)
	if e != nil || again.Item.BaseSHA != base || again.Item.HeadSHA != head || again.Item.ID != row.Item.ID {
		return result, "", domain.Fail(domain.CursorExpired, "The PR changed during the commit read.", "Reload commits for the current PR.")
	}
	return result, next, nil
}
