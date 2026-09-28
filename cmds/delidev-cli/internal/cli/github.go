package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/presentation"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func githubCommand(ctx context.Context, c client, args []string) (any, error) {
	if len(args) < 2 {
		return nil, usage()
	}
	query := domain.RepositoryQuery{}
	switch args[0] {
	case "pr":
		query.Kind = domain.RepositoryPullRequest
	case "issue":
		query.Kind = domain.RepositoryIssue
	default:
		return nil, usage()
	}
	switch args[1] {
	case "list":
		query.Operation = domain.RepositoryList
	case "search":
		query.Operation = domain.RepositorySearch
	case "get", "open":
		query.Operation = domain.RepositoryDetail
	case "diff":
		query.Operation = domain.RepositoryDiff
	case "checks":
		query.Operation = domain.RepositoryChecks
	case "ci":
		query.Operation = domain.RepositoryCI
	case "rules":
		query.Operation = domain.RepositoryRules
	case "statuses":
		query.Operation = domain.RepositoryStatuses
	default:
		return nil, usage()
	}
	f := flags("github " + args[0] + " " + args[1])
	repository := f.String("repository-id", "", "")
	var state, text, number string
	var page, size uint
	if query.Operation == domain.RepositoryDetail || query.IsPRObservation() {
		f.StringVar(&number, "number", "", "")
	}
	if query.Operation != domain.RepositoryDetail && query.Operation != domain.RepositoryDiff && query.Operation != domain.RepositoryRules && query.Operation != domain.RepositoryCI {
		f.UintVar(&page, "page", 1, "")
		f.UintVar(&size, "page-size", 20, "")
	}
	if query.Operation == domain.RepositoryList || query.Operation == domain.RepositorySearch {
		f.StringVar(&state, "state", "open", "")
		if query.Operation == domain.RepositorySearch {
			f.StringVar(&text, "text", "", "")
		}
	}

	if err := parse(f, args[2:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*repository).Validate(); err != nil {
		return nil, err
	}
	if page > 10000 || size > 20 {
		return nil, domain.Fail(domain.InvalidArgument, "The query page is out of bounds.", "Use page 1–10000 and page size 1–20.")
	}
	query.State, query.Search, query.Number, query.Page, query.PageSize = domain.RepositoryItemState(state), text, number, uint32(page), uint32(size)
	if err := query.Validate(); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(query)
	reply, err := c.integrations.QueryRepositoryIntegration(ctx, request(c, &pb.QueryRepositoryIntegrationRequest{RepositoryId: *repository, SchemaVersion: 1, QueryJson: raw}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	if reply.Msg.SchemaVersion != 1 {
		return nil, domain.Fail(domain.Unsupported, "Unsupported repository query response.", "Use a compatible client and server.")
	}
	var value domain.RepositoryQueryResult
	if err := domain.Decode(reply.Msg.DocumentJson, &value); err != nil {
		return nil, err
	}
	if err := value.Validate(); err != nil {
		return nil, err
	}
	if value.RepositoryID != domain.ID(*repository) || value.Query != query {
		return nil, domain.Fail(domain.RecoveryRequired, "The result belongs to another repository query.", "Repeat the selected query explicitly.")
	}
	if args[1] == "open" {
		if err := presentation.OpenGitHub(ctx, value.Items[0].URL); err != nil {
			return nil, err
		}
		return map[string]any{"observation": value, "dispatched": true}, nil
	}
	return value, nil
}
