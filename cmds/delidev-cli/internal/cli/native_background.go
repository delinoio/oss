// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionNativeBackgroundCommand(ctx context.Context, c client, o options, args []string) (any, error) {
	if len(args) == 0 {
		return nil, domain.Fail(domain.MissingInput, "Choose a native-background action.", "Use status, list, terminate or clean; controls require the original live execution.")
	}
	f := flags("session native-background " + args[0])
	id := f.String("id", "", "original session UUID")
	if args[0] == "status" {
		action := f.String("action", "", "optional retained native action UUID")
		if e := parse(f, args[1:]); e != nil {
			return nil, e
		}
		if e := domain.ID(*id).Validate(); e != nil {
			return nil, e
		}
		req := &pb.GetNativeBackgroundRequest{SessionId: *id}
		if *action != "" {
			if e := domain.ID(*action).Validate(); e != nil {
				return nil, e
			}
			req.ActionId = action
		}
		r, e := c.sessions.GetNativeBackground(ctx, request(c, req))
		if e != nil {
			return nil, rpc.ClientError(e)
		}
		return map[string]any{"session": resourceJSON(r.Msg.Session), "action": resourceJSON(r.Msg.Action)}, nil
	}
	op := map[string]pb.NativeBackgroundOperation{"list": pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_LIST, "terminate": pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE, "clean": pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_CLEAN}[args[0]]
	if op == pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_UNSPECIFIED {
		return nil, domain.Fail(domain.InvalidArgument, "Unknown native-background action.", "Use status, list, terminate or clean.")
	}
	revision := f.Uint64("revision", 0, "current original session revision")
	sequence := f.Uint64("inventory-sequence", 0, "original retained native inventory sequence")
	execution := f.String("execution", "", "original active execution UUID")
	item := f.String("item", "", "original native item ID for terminate")
	process := f.String("process", "", "original native process ID for terminate (never an OS PID)")
	wait := f.Bool("wait", true, "wait up to 30 seconds for original native observations")
	if e := parse(f, args[1:]); e != nil {
		return nil, e
	}
	if domain.ID(*id).Validate() != nil || domain.ID(*execution).Validate() != nil || *revision == 0 || *sequence == 0 {
		return nil, domain.NativeBackgroundRecovery()
	}
	if op == pb.NativeBackgroundOperation_NATIVE_BACKGROUND_OPERATION_TERMINATE {
		if domain.Text(*item, "native item", 1024, true) != nil || !domain.NativeBackgroundProcess(*process) {
			return nil, domain.NativeBackgroundRecovery()
		}
	} else if *item != "" || *process != "" {
		return nil, domain.NativeBackgroundRecovery()
	}
	r, e := c.sessions.RequestNativeBackground(ctx, request(c, &pb.RequestNativeBackgroundRequest{Mutation: &pb.Mutation{RequestId: string(o.requestID), Id: *id, ExpectedRevision: *revision}, Operation: op, ExecutionId: *execution, InventorySequence: *sequence, ItemId: *item, ProcessId: *process}))
	if e != nil {
		return nil, rpc.ClientError(e)
	}
	accepted := map[string]any{"action": resourceJSON(r.Msg.Action), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}
	if !*wait {
		return accepted, nil
	}
	if r.Msg.Action == nil {
		return accepted, domain.NativeBackgroundRecovery()
	}
	actionID := r.Msg.Action.Id
	bounded, stop := context.WithTimeout(ctx, 30*time.Second)
	defer stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		observed, e := c.sessions.GetNativeBackground(bounded, request(c, &pb.GetNativeBackgroundRequest{SessionId: *id, ActionId: &actionID}))
		if e != nil {
			return accepted, rpc.ClientError(e)
		}
		var a domain.NativeBackgroundAction
		if observed.Msg.Action == nil || domain.Decode(observed.Msg.Action.DocumentJson, &a) != nil || a.Validate() != nil || a.ActionID != domain.ID(actionID) {
			return accepted, domain.NativeBackgroundRecovery()
		}
		result := map[string]any{"session": resourceJSON(observed.Msg.Session), "action": resourceJSON(observed.Msg.Action), "request_id": r.Msg.RequestId, "replayed": r.Msg.Replayed}
		if a.State == domain.NativeBackgroundObserved {
			return result, nil
		}
		if a.State == domain.NativeBackgroundUncertain || a.State == domain.NativeBackgroundCanceled || a.State == domain.NativeBackgroundRejected {
			return result, domain.NativeBackgroundRecovery()
		}
		select {
		case <-bounded.Done():
			return result, domain.NativeBackgroundRecovery()
		case <-ticker.C:
		}
	}
}
