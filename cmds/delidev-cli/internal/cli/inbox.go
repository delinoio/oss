package cli

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func inboxViewJSON(view *pb.InboxView) any {
	if view == nil {
		return nil
	}
	result := map[string]any{"entry": resourceJSON(view.Entry), "session": resourceJSON(view.Session)}
	if view.Interaction != nil {
		result["interaction"] = resourceJSON(view.Interaction)
	}
	return result
}

func inboxCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, usage()
	}
	f := flags("inbox " + args[0])
	if args[0] == "list" {
		session := f.String("session-id", "", "session filter")
		project := f.String("project-id", "", "project filter")
		state := f.String("read-state", "all", "all, read or unread")
		source := f.String("source", "all", "all, interaction or execution-terminal")
		limit := f.Uint("limit", 50, "page size")
		cursor := f.String("page-token", "", "inbox page token")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit == 0 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid inbox page size.", "Use --limit between 1 and 200.")
		}
		for _, id := range []string{*session, *project} {
			if id != "" {
				if err := domain.ID(id).Validate(); err != nil {
					return nil, err
				}
			}
		}
		readState := pb.InboxReadState_INBOX_READ_STATE_UNSPECIFIED
		switch *state {
		case "all":
		case string(domain.InboxRead):
			readState = pb.InboxReadState_INBOX_READ_STATE_READ
		case string(domain.InboxUnread):
			readState = pb.InboxReadState_INBOX_READ_STATE_UNREAD
		default:
			return nil, domain.Fail(domain.InvalidArgument, "Unknown inbox read-state filter.", "Use --read-state all, read or unread.")
		}
		sourceKind := pb.InboxSource_INBOX_SOURCE_UNSPECIFIED
		switch *source {
		case "all":
		case string(domain.InteractionInbox):
			sourceKind = pb.InboxSource_INBOX_SOURCE_INTERACTION
		case string(domain.ExecutionTerminalInbox):
			sourceKind = pb.InboxSource_INBOX_SOURCE_EXECUTION_TERMINAL
		default:
			return nil, domain.Fail(domain.InvalidArgument, "Unknown inbox source filter.", "Use --source all, interaction or execution-terminal.")
		}
		response, err := c.inbox.ListInbox(ctx, request(c, &pb.ListInboxRequest{SessionId: *session, ProjectId: *project, ReadState: readState, Source: sourceKind, PageSize: uint32(*limit), PageToken: *cursor}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		entries := make([]any, 0, len(response.Msg.Entries))
		for _, view := range response.Msg.Entries {
			entries = append(entries, inboxViewJSON(view))
		}
		return map[string]any{"entries": entries, "next_page_token": response.Msg.NextPageToken}, nil
	}
	if args[0] != "get" && args[0] != "inspect" && args[0] != "mark-read" && args[0] != "mark-unread" {
		return nil, usage()
	}
	id := f.String("id", "", "inbox entry identity")
	mark := args[0] == "mark-read" || args[0] == "mark-unread"
	var revision uint64
	if mark {
		f.Uint64Var(&revision, "revision", 0, "current inbox entry revision")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if *id == "" {
		return nil, domain.Fail(domain.MissingInput, "An inbox entry ID is required.", "Use an entry ID returned by inbox list.")
	}
	if mark && revision == 0 {
		return nil, domain.Fail(domain.MissingInput, "The inbox mutation requires its current revision.", "Inspect the inbox entry, then use its own revision for mark-read or mark-unread.")
	}
	if err := domain.ID(*id).Validate(); err != nil {
		return nil, err
	}
	if !mark {
		response, err := c.inbox.GetInboxEntry(ctx, request(c, &pb.GetInboxEntryRequest{Id: *id}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"view": inboxViewJSON(response.Msg.View)}, nil
	}
	state := pb.InboxReadState_INBOX_READ_STATE_READ
	if args[0] == "mark-unread" {
		state = pb.InboxReadState_INBOX_READ_STATE_UNREAD
	}
	response, err := c.inbox.SetInboxReadState(ctx, request(c, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{Id: *id, ExpectedRevision: revision, RequestId: string(o.requestID)}, ReadState: state}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	return map[string]any{"view": inboxViewJSON(response.Msg.View), "replayed": response.Msg.Replayed}, nil
}
