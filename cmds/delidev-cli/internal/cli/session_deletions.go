package cli

import (
	"context"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionDeletionJSON(v *pb.SessionDeletionJob) any {
	return map[string]any{"id": v.Id, "session_id": v.SessionId, "revision": strconv.FormatUint(v.Revision, 10), "state": v.State.String(), "accepted_at": v.AcceptedAt, "finished_at": v.FinishedAt, "workers_pending": v.WorkersPending, "database_removed": v.DatabaseRemoved, "backups_removed": v.BackupsRemoved, "reclaimed_bytes_known": v.ReclaimedBytesKnown}
}
func sessionDeletionCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	f := flags("session " + args[0])
	id := f.String("id", "", "session UUID")
	revision := new(uint64)
	confirm := new(bool)
	wait := new(bool)
	if args[0] == "delete" {
		revision = f.Uint64("revision", 0, "exact current session revision")
		confirm = f.Bool("confirm", false, "confirm irreversible deletion of all DeliDev-managed copies")
		wait = f.Bool("wait", false, "observe confirmed cleanup within the command deadline")
	}
	if e := parse(f, args[1:]); e != nil {
		return nil, e
	}
	if e := domain.ID(*id).Validate(); e != nil {
		return nil, e
	}
	var job *pb.SessionDeletionJob
	if args[0] == "delete" {
		if !*confirm || *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Permanent deletion requires confirmation and the current revision.", "Use --id ID --revision REV --confirm; deletion cannot be canceled.")
		}
		r, e := c.sessions.DeleteSession(ctx, request(c, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}}))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		job = r.Msg.Job
		if !*wait {
			return map[string]any{"job": sessionDeletionJSON(job), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}, nil
		}
	}
	for {
		r, e := c.sessions.GetSessionDeletion(ctx, request(c, &pb.GetSessionDeletionRequest{SessionId: *id}))
		if e != nil {
			if job != nil {
				return sessionDeletionJSON(job), rpc.ClientError(e)
			}
			return nil, rpc.ClientError(e)
		}
		job = r.Msg.Job
		if !*wait || job.State == pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED {
			return sessionDeletionJSON(job), nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return sessionDeletionJSON(job), domain.SafeError(ctx.Err())
		case <-timer.C:
		}
	}
}
