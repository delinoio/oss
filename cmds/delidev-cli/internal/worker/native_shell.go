// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"os"
	"path/filepath"
	"time"
)

type nativeShellSend struct {
	Version        uint32                    `json:"version"`
	JobID          domain.ID                 `json:"job_id"`
	ActionID       domain.ID                 `json:"action_id"`
	SessionID      domain.ID                 `json:"session_id"`
	ExecutionID    domain.ID                 `json:"execution_id"`
	NativeThreadID domain.NativeIdentity     `json:"native_thread_id"`
	Command        domain.NativeShellCommand `json:"command"`
	ClaimRequestID domain.ID                 `json:"claim_request_id"`
	InputDigest    string                    `json:"input_digest"`
}
type nativeShellCheckpoint struct {
	Version            uint32                        `json:"version"`
	ServerID           domain.ID                     `json:"server_id"`
	DeviceID           domain.ID                     `json:"device_id"`
	JobID              domain.ID                     `json:"job_id"`
	InstanceID         domain.ID                     `json:"instance_id"`
	AssignmentRevision uint64                        `json:"assignment_revision"`
	AssignmentDigest   string                        `json:"assignment_digest"`
	Input              domain.SessionCompactionInput `json:"input"`
	Send               nativeShellSend               `json:"send"`
	Native             codex.NativeShellCheckpoint   `json:"native"`
}

func nativeShellCheckpointPath(root string, action domain.ID) (string, error) {
	if action.Validate() != nil {
		return "", domain.NativeShellRecovery()
	}
	return filepath.Join(root, "native-shell-checkpoints", string(action)+".json"), nil
}
func readNativeShellCheckpoint(ctx context.Context, root string, credential Credential, input domain.ExecutionJobInput, ref domain.NativeShellRef) (codex.NativeShellCheckpoint, error) {
	path, err := nativeShellCheckpointPath(root, ref.ActionID)
	if err != nil || ref.Validate() != nil || input.Continuation == nil || ref.ExecutionID != input.Continuation.Previous.ExecutionID {
		return codex.NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	account, connection := input.AccountID, input.ConnectionID
	if input.Continuation.PreviousAccountID != "" {
		account, connection = input.Continuation.PreviousAccountID, input.Continuation.PreviousConnectionID
	}
	raw, err := security.ReadPrivate(path, maxCompactionCheckpoint)
	var p nativeShellCheckpoint
	if err != nil || executionInputDigest(raw) != ref.CheckpointDigest || domain.DecodeWithLimit(raw, &p, maxCompactionCheckpoint) != nil || p.Version != 1 || p.ServerID != credential.ServerID || p.DeviceID != credential.DeviceID || p.InstanceID.Validate() != nil || p.AssignmentRevision == 0 || p.JobID != ref.JobID || p.Input.Validate() != nil || p.Input.NativeShell == nil || p.Input.ActionID != ref.ActionID || p.Input.Assignment.ExecutionID != ref.ExecutionID || p.Input.SourceJobID != input.Continuation.Previous.JobID || p.Native.Source.ThreadID != domain.ID(p.Input.Completion.NativeThreadID) || p.Native.Source.TurnID != domain.ID(p.Input.Completion.NativeTurnID) || p.Input.Assignment.SessionID != input.SessionID || p.Input.Assignment.MachineID != input.MachineID || p.Input.Assignment.AccountID != account || p.Input.Assignment.ConnectionID != connection || p.Input.Assignment.ConfigurationDigest != input.ConfigurationDigest {
		return codex.NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	nativeRaw, _ := json.Marshal(p.Native)
	if executionInputDigest(nativeRaw) != ref.NativeDigest {
		return codex.NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	sendRaw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(p.JobID), string(nativeShellSendClaim)), 128<<10)
	expected, _ := json.Marshal(p.Send)
	if err != nil || !bytes.Equal(sendRaw, expected) || p.Send.Version != 1 || p.Send.JobID != p.JobID || p.Send.SessionID != p.Input.Assignment.SessionID || p.Send.ExecutionID != ref.ExecutionID || p.Send.NativeThreadID != p.Input.Completion.NativeThreadID || p.Send.ClaimRequestID.Validate() != nil || !bytes.Equal(mustNativeShellCommand(p.Send.Command), mustNativeShellCommand(*p.Input.NativeShell)) || p.Send.ActionID != ref.ActionID || p.Send.InputDigest != executionInputDigest(mustNativeShellInput(p.Input)) {
		return codex.NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	journalRaw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(p.JobID)+".json"), 1<<20)
	var original journal
	var result domain.NativeShellResult
	if err != nil || domain.Decode(journalRaw, &original) != nil || original.InstanceID != p.InstanceID || original.State != journalFinished && original.State != journalReported || original.Revision != p.AssignmentRevision || original.Digest != p.AssignmentDigest || original.Problem != nil || domain.Decode(original.Output, &result) != nil || result.Validate() != nil || result.History == nil || *result.History != ref {
		return codex.NativeShellCheckpoint{}, domain.NativeShellRecovery()
	}
	return p.Native, nil
}
func mustNativeShellCommand(command domain.NativeShellCommand) []byte {
	raw, _ := json.Marshal(command)
	return raw
}
func mustNativeShellInput(input domain.SessionCompactionInput) []byte {
	raw, _ := json.Marshal(input)
	return raw
}

func runNativeShellAction(ctx context.Context, config Config, owner domain.ID, job domain.Job, i domain.SessionCompactionInput, native *codex.Client, source codex.ContinuationCheckpoint) (domain.NativeShellObservation, codex.NativeShellCheckpoint, nativeShellSend, error) {
	var empty domain.NativeShellObservation
	var history codex.NativeShellCheckpoint
	var send nativeShellSend
	c := config.execution
	if c == nil || i.NativeShell == nil || i.Validate() != nil {
		return empty, history, send, domain.NativeShellRecovery()
	}
	claimID := domain.NewID()
	reply, err := c.Client.ClaimNativeShell(ctx, authenticated(c.Credential, &pb.ClaimNativeShellRequest{Mutation: &pb.Mutation{RequestId: string(claimID), Id: string(i.ActionID), ExpectedRevision: 1}, MachineId: string(i.Assignment.MachineID), InstanceId: string(c.Instance)}))
	if err != nil || reply == nil || reply.Msg == nil || reply.Msg.Action == nil || reply.Msg.Replayed {
		return empty, history, send, domain.NativeShellRecovery()
	}
	var action domain.NativeShellAction
	if domain.Decode(reply.Msg.Action.DocumentJson, &action) != nil || action.Validate() != nil || action.Claim == nil || action.Claim.RequestID != claimID || action.JobID != owner || action.ActionID != i.ActionID || action.SessionID != i.Assignment.SessionID || action.ExecutionID != i.Assignment.ExecutionID || action.State != domain.NativeShellClaimed || action.Claim.MachineID != i.Assignment.MachineID || action.Claim.InstanceID != c.Instance || action.Claim.DeviceID != c.Credential.DeviceID || !bytes.Equal(mustNativeShellCommand(action.Command), mustNativeShellCommand(*i.NativeShell)) || action.Observation == nil || action.Observation.NativeThreadID != i.Completion.NativeThreadID {
		return empty, history, send, domain.NativeShellRecovery()
	}
	send = nativeShellSend{Version: 1, JobID: owner, ActionID: i.ActionID, SessionID: i.Assignment.SessionID, ExecutionID: i.Assignment.ExecutionID, NativeThreadID: i.Completion.NativeThreadID, Command: *i.NativeShell, ClaimRequestID: claimID, InputDigest: executionInputDigest(job.Input)}
	if err := writeCompactionClaim(config.Root, owner, nativeShellSendClaim, send); err != nil {
		return empty, history, send, err
	}
	if err := native.StartNativeShell(ctx, i.ActionID, *i.NativeShell, source); err != nil {
		return empty, history, send, err
	}
	revision := reply.Msg.Action.Revision
	observation := *action.Observation
	readCtx := ctx
	var cancelWait context.CancelFunc
	cancelRequested := false
	defer func() {
		if cancelWait != nil {
			cancelWait()
		}
	}()
	for count := 0; count < domain.MaxExecutionEvents; count++ {
		event, err := native.NextEvent(readCtx)
		if err != nil && ctx.Err() != nil && !cancelRequested {
			cancelRequested = true
			readCtx, cancelWait = context.WithTimeout(context.Background(), 15*time.Second)
			if err := native.CancelNativeShell(readCtx, domain.NewID()); err != nil {
				return observation, history, send, err
			}
			continue
		}
		if err != nil {
			return observation, history, send, err
		}
		if event.Kind == codex.NativeShellEvent {
			if event.NativeShell == nil {
				return observation, history, send, domain.NativeShellRecovery()
			}
			next := *event.NativeShell
			next.SessionID = i.Assignment.SessionID
			next.ExecutionID = i.Assignment.ExecutionID
			// Native lifecycle sequence is exact and independent of receipt revisions.
			if domain.CheckNativeShellTransition(observation, next) != nil {
				return observation, history, send, domain.NativeShellRecovery()
			}
			raw, _ := json.Marshal(next)
			bounded, stop := context.WithTimeout(context.Background(), 10*time.Second)
			published, e := c.Client.PublishNativeShell(bounded, authenticated(c.Credential, &pb.PublishNativeShellRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(i.ActionID), ExpectedRevision: revision}, MachineId: string(i.Assignment.MachineID), InstanceId: string(c.Instance), ObservationJson: raw}))
			stop()
			if e != nil || published == nil || published.Msg == nil || published.Msg.Action == nil {
				return observation, history, send, domain.NativeShellRecovery()
			}
			revision = published.Msg.Action.Revision
			observation = next
			// Item completion alone does not prove terminal turn completion. The
			// independently retained native history extension supplies that boundary.
			if next.State == domain.NativeShellExited || next.State == domain.NativeShellCanceled {
				retained, e := native.RetainNativeShellCheckpoint(readCtx)
				if e == nil {
					return observation, retained, send, nil
				}
				// A completed item commonly precedes its turn terminal event. Continue
				// only while the original turn has no terminal claim yet.
				if native.NativeShellTerminal() {
					return observation, history, send, e
				}
			}
		} else if event.Kind != codex.MetadataEvent && event.Kind != codex.ThreadStatusEvent && event.Kind != codex.NoticeEvent && event.Kind != codex.NativeExtensionEvent {
			return observation, history, send, domain.NativeShellRecovery()
		}
	}
	return observation, history, send, domain.NativeShellRecovery()
}
func retainNativeShellResult(config Config, owner domain.ID, i domain.SessionCompactionInput, observation domain.NativeShellObservation, native codex.NativeShellCheckpoint, send nativeShellSend) (json.RawMessage, error) {
	c := config.execution
	p := nativeShellCheckpoint{Version: 1, ServerID: c.Credential.ServerID, DeviceID: c.Credential.DeviceID, JobID: owner, InstanceID: c.Instance, AssignmentRevision: c.Assignment.Revision, AssignmentDigest: executionInputDigest(c.Assignment.DocumentJson), Input: i, Send: send, Native: native}
	raw, err := json.Marshal(p)
	nativeRaw, _ := json.Marshal(native)
	if err != nil || len(raw) > maxCompactionCheckpoint {
		return nil, domain.NativeShellRecovery()
	}
	path, err := nativeShellCheckpointPath(config.Root, i.ActionID)
	if err != nil {
		return nil, err
	}
	if security.PrivateDir(filepath.Dir(path)) != nil {
		return nil, domain.NativeShellRecovery()
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, domain.NativeShellRecovery()
	}
	if err := security.WriteAtomic(path, raw); err != nil {
		return nil, err
	}
	observation.CleanupVerified = true
	observation.Sequence++
	result := domain.NativeShellResult{Version: 1, Observation: observation, History: &domain.NativeShellRef{JobID: owner, ActionID: i.ActionID, ExecutionID: i.Assignment.ExecutionID, CheckpointDigest: executionInputDigest(raw), NativeDigest: executionInputDigest(nativeRaw)}}
	if result.Validate() != nil {
		return nil, domain.NativeShellRecovery()
	}
	return json.Marshal(result)
}
