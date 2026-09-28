package cli

import (
	"context"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func searchCommand(ctx context.Context, c client, args []string) (any, error) {
	f := flags("search")
	query := f.String("query", "", "literal conversation text")
	session := f.String("session-id", "", "session filter")
	project := f.String("project-id", "", "project filter")
	agent := f.String("agent-id", "", "Agent Worker filter")
	account := f.String("account-id", "", "original execution account filter")
	outcome := f.String("outcome", "all", "all, not-started, running, succeeded, failed or stopped")
	archive := f.String("archive", "all", "all, active, archiving or archived")
	limit := f.Uint("limit", 50, "page size")
	cursor := f.String("page-token", "", "search page token")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	if *query == "" {
		return nil, domain.Fail(domain.MissingInput, "Conversation text is required.", "Use search --query TEXT.")
	}
	if *limit == 0 || *limit > 200 {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid search page size.", "Use --limit between 1 and 200.")
	}
	selection := domain.SearchSelection{SessionID: domain.ID(*session), ProjectID: domain.ID(*project), AgentID: domain.ID(*agent), AccountID: domain.ID(*account)}
	if *outcome != "all" {
		selection.Outcome = domain.ExecutionOutcome(*outcome)
	}
	if *archive != "all" {
		selection.Archive = domain.ArchiveState(*archive)
	}
	if err := selection.Validate(); err != nil {
		return nil, err
	}
	outcomeWire := pb.SearchExecutionOutcome_SEARCH_EXECUTION_OUTCOME_UNSPECIFIED
	archiveWire := pb.SearchArchiveState_SEARCH_ARCHIVE_STATE_UNSPECIFIED
	if selection.Outcome != "" {
		outcomeWire = pb.SearchExecutionOutcome(pb.SearchExecutionOutcome_value["SEARCH_EXECUTION_OUTCOME_"+strings.ToUpper(strings.ReplaceAll(*outcome, "-", "_"))])
	}
	if selection.Archive != "" {
		archiveWire = pb.SearchArchiveState(pb.SearchArchiveState_value["SEARCH_ARCHIVE_STATE_"+strings.ToUpper(*archive)])
	}
	response, err := c.search.SearchConversations(ctx, request(c, &pb.SearchConversationsRequest{Query: *query, SessionId: *session, ProjectId: *project, AgentId: *agent, AccountId: *account, Outcome: outcomeWire, Archive: archiveWire, PageSize: uint32(*limit), PageToken: *cursor}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	hits := make([]any, 0, len(response.Msg.Hits))
	for _, hit := range response.Msg.Hits {
		hits = append(hits, map[string]any{"message": resourceJSON(hit.Message), "session_name": hit.SessionName, "agent_id": hit.AgentId, "outcome": strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(hit.Outcome.String(), "SEARCH_EXECUTION_OUTCOME_")), "_", "-"), "archive": strings.ToLower(strings.TrimPrefix(hit.Archive.String(), "SEARCH_ARCHIVE_STATE_"))})
	}
	return map[string]any{"hits": hits, "next_page_token": response.Msg.NextPageToken}, nil
}
