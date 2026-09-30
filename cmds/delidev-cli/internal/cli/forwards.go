package cli

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/forwarding"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionForward(ctx context.Context, c client, o options, args []string, streams IO) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Select a forwarding operation.", "Use session forward start, status, stop or reconcile.")
	}
	f := flags("session forward " + args[0])
	session := f.String("session-id", "", "owning session")
	id := f.String("id", "", "accepted forward ID")
	revision := f.Uint64("revision", 0, "exact session revision for start, forward revision for stop")
	machine := f.String("machine-id", "", "explicit owning Worker")
	port := f.Uint("worker-port", 0, "explicit Worker-loopback development port")
	local := f.Uint("local-port", 0, "OS-assigned loopback port by default; explicit conflicts fail")
	if err := parse(f, args[1:]); err != nil {
		return nil, err
	}
	if err := domain.ID(*session).Validate(); err != nil {
		return nil, err
	}
	if args[0] != "start" {
		if err := domain.ID(*id).Validate(); err != nil {
			return nil, err
		}
	}
	switch args[0] {
	case "start":
		if domain.ID(*machine).Validate() != nil || *revision == 0 || *port == 0 || *port > 65535 || *local > 65535 {
			return nil, domain.Fail(domain.InvalidArgument, "Select the owning Worker, session revision and development port.", "Use --machine-id, --revision and --worker-port; --local-port defaults to zero.")
		}
		response, err := c.forwards.StartForward(ctx, request(c, &pb.StartForwardRequest{RequestId: string(o.requestID), SessionId: *session, ExpectedSessionRevision: *revision, MachineId: *machine, WorkerPort: uint32(*port), LocalPort: uint32(*local)}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		result := map[string]any{"forward": resourceJSON(response.Msg.Forward), "replayed": response.Msg.Replayed}
		// A repeated command returns observation of the original foreground owner.
		// It never starts another listener, including after Stop or process loss.
		if response.Msg.Replayed {
			return result, nil
		}
		var value domain.Forward
		if err := domain.Decode(response.Msg.Forward.DocumentJson, &value); err != nil {
			return result, err
		}
		config := forwarding.Config{Client: c.forwards, Token: c.token, Root: o.dataDir, Endpoint: c.endpoint, Forward: value, Peer: &pb.ForwardPeer{ForwardId: response.Msg.Forward.Id, SessionId: *session, RuntimeId: string(value.ClientRuntimeID)}, Logger: slog.New(slog.NewJSONHandler(streams.Err, nil)), Ready: func(endpoint string) error {
			// Foreground start publishes one readiness envelope before waiting. It
			// returns an independent final envelope when its original lifetime ends.
			return json.NewEncoder(streams.Out).Encode(envelope{Version: 1, RequestID: o.requestID, Result: map[string]any{"forward_id": response.Msg.Forward.Id, "session_id": *session, "local_endpoint": endpoint, "ready": true}})
		}}
		err = forwarding.Run(ctx, config)
		observe, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		current, readErr := c.forwards.GetForward(observe, request(c, &pb.GetForwardRequest{ForwardId: response.Msg.Forward.Id, SessionId: *session}))
		if readErr == nil {
			result["forward"] = resourceJSON(current.Msg.Forward)
		}
		return result, err
	case "status":
		response, err := c.forwards.GetForward(ctx, request(c, &pb.GetForwardRequest{ForwardId: *id, SessionId: *session}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"forward": resourceJSON(response.Msg.Forward)}, nil
	case "stop":
		if *revision == 0 {
			return nil, domain.Fail(domain.MissingInput, "Stopping requires the current forward revision.", "Read its status and pass --revision.")
		}
		response, err := c.forwards.StopForward(ctx, request(c, &pb.StopForwardRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, SessionId: *session}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		return map[string]any{"forward": resourceJSON(response.Msg.Forward), "replayed": response.Msg.Replayed}, nil
	case "reconcile":
		response, err := c.forwards.GetForward(ctx, request(c, &pb.GetForwardRequest{ForwardId: *id, SessionId: *session}))
		if err != nil {
			return nil, rpc.ClientError(err)
		}
		var value domain.Forward
		if err := domain.Decode(response.Msg.Forward.DocumentJson, &value); err != nil {
			return nil, err
		}
		config := forwarding.Config{Client: c.forwards, Token: c.token, Root: o.dataDir, Endpoint: c.endpoint, Peer: &pb.ForwardPeer{ForwardId: *id, SessionId: *session, RuntimeId: string(value.ClientRuntimeID)}}
		if err := forwarding.Reconcile(ctx, config); err != nil {
			return nil, err
		}
		return map[string]any{"forward_id": *id, "cleanup_receipt_confirmed": true}, nil
	default:
		return nil, domain.Fail(domain.InvalidArgument, "Unknown forwarding operation.", "Use start, status, stop or reconcile.")
	}
}
