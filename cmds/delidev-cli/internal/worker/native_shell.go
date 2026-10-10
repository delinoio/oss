// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"path/filepath"
	"time"
)

// The command is sent once after the caller durably retains the original claim.
// Publications authenticate the immutable assignment even as live revisions grow.
func runNativeShellAction(ctx context.Context, config Config, owner domain.ID, native *codex.Client, input domain.SessionCompactionInput, thread domain.ID) (domain.NativeShellObservation, error) {
	result := domain.NativeShellObservation{Version: 1, ActionID: input.ActionID, NativeThreadID: thread, Processes: []domain.NativeShellProcess{}, Sequence: 1}
	observed, err := native.ShellCommand(ctx, input.ActionID, thread, input.Shell.Command, input.Shell.TimeoutMS)
	result.Delivery = domain.NativeShellDelivery(observed.Delivery)
	if result.Delivery == domain.NativeShellDelivery(codex.ShellSendIntent) || result.Delivery == "" {
		result.Delivery = domain.NativeShellUncertainDelivery
	}
	publish := func() error {
		if result.Validate() != nil {
			return domain.NativeShellUncertain()
		}
		raw, e := json.Marshal(result)
		if e != nil {
			return e
		}
		c := config.execution
		bounded, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		reply, e := c.Client.PublishNativeShell(bounded, authenticated(c.Credential, &pb.PublishNativeShellRequest{Mutation: &pb.Mutation{Id: string(owner), ExpectedRevision: c.Assignment.Revision, RequestId: string(domain.NewID())}, MachineId: string(input.Assignment.MachineID), InstanceId: string(c.Instance), ObservationJson: raw}))
		if e != nil {
			return rpc.ClientError(e)
		}
		if reply == nil || reply.Msg == nil || reply.Msg.Job == nil || reply.Msg.Job.Id != string(owner) {
			return domain.NativeShellUncertain()
		}
		return nil
	}
	if e := publish(); e != nil {
		return result, e
	}
	if err != nil {
		return result, err
	}
	indices := map[string]int{}
	eventsCtx := ctx
	var cancel context.CancelFunc
	defer func() {
		if cancel != nil {
			cancel()
		}
	}()
	interrupted := false
	for count := 0; count < domain.MaxExecutionEvents-2; count++ {
		event, e := native.NextEvent(eventsCtx)
		if e != nil {
			if ctx.Err() != nil && !interrupted {
				interrupted = true
				eventsCtx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
				if e := native.InterruptShell(eventsCtx, input.ActionID); e != nil {
					return result, e
				}
				continue
			}
			return result, e
		}
		if event.Kind != codex.NativeShellEvent {
			continue
		}
		if event.Shell == nil || event.Shell.RequestID != input.ActionID || event.Shell.ThreadID != thread {
			return result, domain.NativeShellUncertain()
		}
		result.Delivery, result.NativeTurnID, result.Terminal = domain.NativeShellDelivery(event.Shell.Delivery), event.Shell.TurnID, event.Shell.Terminal
		if event.Tool != nil {
			t := event.Tool
			if t.Command == nil {
				return result, domain.NativeShellUncertain()
			}
			n, exists := indices[t.ID]
			if !exists {
				n = len(result.Processes)
				indices[t.ID] = n
				result.Processes = append(result.Processes, domain.NativeShellProcess{ItemID: t.ID, Command: t.Command.Command, Cwd: t.Command.Cwd, Output: ""})
			}
			p := &result.Processes[n]
			if p.Command != t.Command.Command || p.Cwd != t.Command.Cwd || p.ProcessID != nil && (t.Command.ProcessID == nil || *p.ProcessID != *t.Command.ProcessID) {
				return result, domain.NativeShellUncertain()
			}
			p.ProcessID, p.Status, p.ExitCode, p.AggregatedOutput = t.Command.ProcessID, domain.NativeShellStatus(t.Status), t.Command.ExitCode, t.Command.AggregatedOutput
		}
		if event.TextDelta != "" {
			n, exists := indices[event.ItemID]
			if !exists {
				return result, domain.NativeShellUncertain()
			}
			result.Processes[n].Output += event.TextDelta
		}
		result.Sequence++
		if e := publish(); e != nil {
			return result, e
		}
		if result.Terminal {
			if len(result.Processes) == 0 {
				return result, domain.NativeShellUncertain()
			}
			return result, nil
		}
	}
	return result, domain.NativeShellUncertain()
}

type nativeShellSendClaimRecord struct {
	ActionID         domain.ID                 `json:"action_id"`
	JobID            domain.ID                 `json:"job_id"`
	ThreadID         domain.ID                 `json:"thread_id"`
	AssignmentDigest string                    `json:"assignment_digest"`
	Command          domain.NativeShellCommand `json:"command"`
}

func nativeShellSendClaimFor(config Config, owner domain.ID, input domain.SessionCompactionInput, thread domain.ID) nativeShellSendClaimRecord {
	return nativeShellSendClaimRecord{input.ActionID, owner, thread, executionInputDigest(config.execution.Assignment.DocumentJson), *input.Shell}
}
func verifyNativeShellSendClaim(config Config, owner domain.ID, input domain.SessionCompactionInput, thread domain.ID) error {
	expected, err := json.Marshal(nativeShellSendClaimFor(config, owner, input, thread))
	if err != nil {
		return domain.NativeShellUncertain()
	}
	raw, err := security.ReadPrivate(filepath.Join(config.Root, "jobs", string(owner), string(nativeShellSendClaim)), 128<<10)
	if err != nil || !bytes.Equal(raw, expected) {
		return domain.NativeShellUncertain()
	}
	return nil
}
