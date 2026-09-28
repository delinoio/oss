package worker

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// The auxiliary reader is joined with its primary stream but its errors cannot
// interrupt an agent. It never runs or claims a durable execution job.
func watchWorkspaceReads(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := receiveWorkspaceReads(ctx, config, client, credential, instance)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > 30*time.Second {
			backoff = time.Second
		}
		if config.Logger != nil {
			config.Logger.WarnContext(ctx, "workspace_reader_interrupted", "machine_id", credential.MachineID, "code", rpc.ClientError(err).Code)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(15*time.Second, backoff*2)
	}
}

func receiveWorkspaceReads(ctx context.Context, config Config, client delidevv1connect.WorkerServiceClient, credential Credential, instance domain.ID) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	deadline := time.AfterFunc(domain.WorkerConnectionTimeout, cancel)
	defer deadline.Stop()
	stream, err := client.WatchWorkspaceReads(ctx, authenticated(credential, &pb.WatchWorkspaceReadsRequest{MachineId: string(credential.MachineID), InstanceId: string(instance)}))
	if err != nil {
		return err
	}
	defer stream.Close()
	manager := workspace.Manager{Root: config.Root, Logger: config.Logger}
	for stream.Receive() {
		deadline.Reset(domain.WorkerConnectionTimeout)
		message := stream.Msg()
		if message.Heartbeat {
			if len(message.RequestJson) != 0 {
				return workspace.ResultUncertain()
			}
			continue
		}
		var request workspace.ReadRequest
		if len(message.RequestJson) > 2<<20 || domain.Decode(message.RequestJson, &request) != nil || request.ID.Validate() != nil || request.Preparation.MachineID != credential.MachineID {
			return workspace.ResultUncertain()
		}
		report := &pb.ReportWorkspaceReadRequest{MachineId: string(credential.MachineID), InstanceId: string(instance), ReadId: string(request.ID)}
		var problem error
		if request.PRCandidate != nil {
			var result workspace.PRWorkspaceMatch
			result, problem = manager.MatchPRWorkspace(ctx, request)
			if problem == nil {
				report.DocumentJson, _ = json.Marshal(result)
			}
		} else {
			var result domain.WorkspaceReadResult
			result, problem = manager.ReadWorkspace(ctx, request)
			if problem == nil {
				report.DocumentJson, _ = json.Marshal(result)
			}
		}
		if problem != nil {
			report.ProblemCode = string(domain.SafeError(problem).Code)
		}
		attempt, stop := context.WithTimeout(ctx, 5*time.Second)
		_, err = client.ReportWorkspaceRead(attempt, authenticated(credential, report))
		stop()
		// Never replay an observation after an uncertain report. A new client
		// query takes a fresh observation under independently current authority.
		if err != nil {
			// A view can be canceled while this bounded observation finishes.
			// Its late report is rejected, but must not disconnect the reader
			// and invalidate the next independently queued navigation request.
			code := connect.CodeOf(err)
			if ctx.Err() == nil && (code == connect.CodeUnavailable || code == connect.CodeCanceled || code == connect.CodeDeadlineExceeded) {
				if config.Logger != nil {
					config.Logger.InfoContext(ctx, "workspace_observation_discarded", "read_id", request.ID, "code", rpc.ClientError(err).Code)
				}
				continue
			}
			return err
		}
	}
	if err := stream.Err(); err != nil {
		return err
	}
	return domain.Fail(domain.Unavailable, "The workspace reader disconnected.", "Refresh after the Worker reconnects.")
}
