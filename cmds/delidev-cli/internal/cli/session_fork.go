package cli

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func forkJSON(value *pb.ForkSessionResponse) any {
	return map[string]any{"job": resourceJSON(value.Job), "session": resourceJSON(value.Session), "request_id": value.RequestId, "replayed": value.Replayed}
}

func sessionForkCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	f := flags("session fork")
	id := f.String("id", "", "completed source session")
	revision := f.Uint64("revision", 0, "exact source revision")
	turn := f.String("turn-id", "", "verified completed native turn")
	name := f.String("name", "", "new independent session name")
	workspace := f.String("workspace", "", "worktree, general-chat or explicit local")
	localRoot := f.String("local-worker-dir", "", "private paired Worker scope for Local sharing")
	jobID := f.String("job-id", "", "observe the original accepted fork without replaying creation")
	wait := f.Bool("wait", false, "wait for the original fork job within the command deadline")
	if err := parse(f, args); err != nil {
		return nil, err
	}
	var result *pb.ForkSessionResponse
	if *jobID != "" {
		if *id != "" || *revision != 0 || *turn != "" || *name != "" || *workspace != "" || *localRoot != "" {
			return nil, domain.Fail(domain.InvalidArgument, "Fork observation accepts only --job-id and --wait.", "Retain the accepted job rather than submitting a replacement mutation.")
		}
		response, err := c.sessions.GetSessionFork(ctx, request(c, &pb.GetSessionForkRequest{JobId: *jobID}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		result = &pb.ForkSessionResponse{Job: response.Msg.Job, Session: response.Msg.Session}
	} else {
		kind := pb.ForkWorkspace_FORK_WORKSPACE_UNSPECIFIED
		switch domain.WorkspaceType(*workspace) {
		case "":
		case domain.Worktree:
			kind = pb.ForkWorkspace_FORK_WORKSPACE_WORKTREE
		case domain.GeneralChat:
			kind = pb.ForkWorkspace_FORK_WORKSPACE_GENERAL_CHAT
		case domain.Local:
			kind = pb.ForkWorkspace_FORK_WORKSPACE_LOCAL
		default:
			return nil, domain.Fail(domain.InvalidArgument, "Unknown fork workspace.", "Select worktree, general-chat or local.")
		}
		token := ""
		if kind == pb.ForkWorkspace_FORK_WORKSPACE_LOCAL {
			source, err := c.resources.GetResource(ctx, request(c, &pb.GetResourceRequest{Kind: pb.EntityKind_ENTITY_KIND_SESSION, Id: *id}))
			if err != nil {
				return nil, rpc.ClientError(err)
			}
			var session domain.Session
			if domain.Decode(source.Msg.Resource.DocumentJson, &session) != nil {
				return nil, domain.Fail(domain.RecoveryRequired, "The original session is unavailable.", "Read the source before forking.")
			}
			token, err = localCreationCredential(ctx, c, domain.CreateSession{Workspace: domain.Local, MachineID: session.MachineID}, *localRoot)
			if err != nil {
				return nil, err
			}
		} else if *localRoot != "" {
			return nil, domain.Fail(domain.InvalidArgument, "Local authority requires explicit Local sharing.", "Omit --local-worker-dir for independent workspace forks.")
		}
		response, err := c.sessions.ForkSession(ctx, request(c, &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, ExpectedTurnId: *turn, Name: *name, Workspace: kind, LocalWorkerToken: token}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		result = response.Msg
	}
	if *wait {
		job, err := awaitJobWithin(ctx, c, result.Job, 135*time.Second)
		result.Job = job
		if err != nil {
			return forkJSON(result), err
		}
		response, err := c.sessions.GetSessionFork(ctx, request(c, &pb.GetSessionForkRequest{JobId: job.Id}))
		if err != nil {
			return forkJSON(result), rpc.ClientError(err)
		}
		result.Session = response.Msg.Session
		var state domain.Job
		if domain.Decode(job.DocumentJson, &state) != nil {
			return forkJSON(result), domain.Fail(domain.RecoveryRequired, "The accepted fork result is invalid.", "Retain the original job identity.")
		}
		if state.Problem != nil {
			return forkJSON(result), state.Problem
		}
	}
	return forkJSON(result), nil
}
