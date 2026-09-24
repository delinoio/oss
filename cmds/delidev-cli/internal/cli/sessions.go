package cli

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionChangeJSON(change *pb.SessionChange) any {
	return map[string]any{"session": resourceJSON(change.Session), "input": resourceJSON(change.Input), "replayed": change.Replayed}
}

func sessionCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	action := args[0]
	f := flags("session " + action)
	switch action {
	case "create", "enqueue":
		input := f.String("input", "-", "")
		var id *string
		if action == "enqueue" {
			id = f.String("id", "", "")
		}
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if o.tokenStdin && *input == "-" {
			return nil, domain.Fail(domain.InvalidArgument, "Credential stdin and session input cannot share one stream.", "Pass the document using --input PATH.")
		}
		raw, err := readDocument(*input, streams.In)
		if err != nil {
			return nil, err
		}
		if action == "create" {
			var value domain.CreateSession
			if err := domain.Decode(raw, &value); err != nil {
				return nil, err
			}
			value.Source = domain.ExternalCLISession
			value.ApplyDefaults()
			if err := value.Validate(); err != nil {
				return nil, err
			}
			raw, _ = json.Marshal(value)
			response, err := c.sessions.CreateSession(ctx, request(c, &pb.CreateSessionRequest{RequestId: string(o.requestID), DocumentJson: raw}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return sessionChangeJSON(response.Msg.Change), nil
		}
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
		var value domain.SessionInput
		if err := domain.Decode(raw, &value); err != nil {
			return nil, err
		}
		value.ApplyDefaults()
		if err := value.Validate(); err != nil {
			return nil, err
		}
		raw, _ = json.Marshal(value)
		response, err := c.sessions.EnqueueInput(ctx, request(c, &pb.EnqueueInputRequest{RequestId: string(o.requestID), SessionId: *id, DocumentJson: raw}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return sessionChangeJSON(response.Msg.Change), nil
	case "list":
		project := f.String("project-id", "", "")
		archived := f.Bool("include-archived", false, "")
		limit := f.Uint64("limit", 50, "")
		page := f.String("page-token", "", "")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use --limit between 1 and 200.")
		}
		response, err := c.sessions.ListSessions(ctx, request(c, &pb.ListSessionsRequest{ProjectId: *project, IncludeArchived: *archived, PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"sessions": resourcesJSON(response.Msg.Sessions), "next_page_token": response.Msg.NextPageToken}, nil
	case "stop", "archive", "restore", "unarchive", "resume", "rename":
		id := f.String("id", "", "")
		revision := f.Uint64("revision", 0, "")
		var name *string
		if action == "rename" {
			name = f.String("name", "", "")
		}
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *id == "" || *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Session controls require an ID and current revision.", "Provide --id and --revision from session get.")
		}
		meta := &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}
		if action == "rename" {
			response, err := c.sessions.RenameSession(ctx, request(c, &pb.RenameSessionRequest{Mutation: meta, Name: *name}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			return sessionChangeJSON(response.Msg.Change), nil
		}
		controls := map[string]pb.SessionAction{"stop": pb.SessionAction_SESSION_ACTION_STOP, "archive": pb.SessionAction_SESSION_ACTION_ARCHIVE, "restore": pb.SessionAction_SESSION_ACTION_RESTORE, "unarchive": pb.SessionAction_SESSION_ACTION_RESTORE, "resume": pb.SessionAction_SESSION_ACTION_RESUME}
		response, err := c.sessions.ControlSession(ctx, request(c, &pb.ControlSessionRequest{Mutation: meta, Action: controls[action]}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return sessionChangeJSON(response.Msg.Change), nil
	default:
		return nil, usage()
	}
}

func queueCommand(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	action := args[0]
	f := flags("queue " + action)
	session := f.String("session-id", "", "")
	if action == "list" {
		limit := f.Uint64("limit", 50, "")
		page := f.String("page-token", "", "")
		if err := parse(f, args[1:]); err != nil {
			return nil, err
		}
		if *limit < 1 || *limit > 200 {
			return nil, domain.Fail(domain.InvalidArgument, "Invalid page size.", "Use --limit between 1 and 200.")
		}
		response, err := c.sessions.ListQueue(ctx, request(c, &pb.ListQueueRequest{SessionId: *session, PageSize: uint32(*limit), PageToken: *page}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"inputs": resourcesJSON(response.Msg.Inputs), "next_page_token": response.Msg.NextPageToken}, nil
	}
	id := f.String("id", "", "")
	revision := f.Uint64("revision", 0, "")
	var input *string
	if action == "edit" {
		input = f.String("input", "-", "")
	}
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	meta := &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}
	if action == "edit" {
		if o.tokenStdin && *input == "-" {
			return nil, domain.Fail(domain.InvalidArgument, "Credential stdin and input cannot share one stream.", "Pass --input PATH.")
		}
		raw, err := readDocument(*input, streams.In)
		if err != nil {
			return nil, err
		}
		var value struct {
			Prompt string `json:"prompt"`
		}
		if err := domain.Decode(raw, &value); err != nil {
			return nil, err
		}
		response, err := c.sessions.EditQueuedInput(ctx, request(c, &pb.EditQueuedInputRequest{Mutation: meta, SessionId: *session, Prompt: value.Prompt}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return sessionChangeJSON(response.Msg.Change), nil
	}
	if action == "remove" || action == "delete" {
		response, err := c.sessions.RemoveQueuedInput(ctx, request(c, &pb.RemoveQueuedInputRequest{Mutation: meta, SessionId: *session}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return sessionChangeJSON(response.Msg.Change), nil
	}
	return nil, usage()
}
