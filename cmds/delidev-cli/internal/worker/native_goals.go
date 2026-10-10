// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type goalControlIdentity struct {
	JobID    domain.ID `json:"job_id"`
	ActionID domain.ID `json:"action_id"`
	Revision uint64    `json:"revision"`
}

func goalControl(control *pb.GoalActionControl) (goalControlIdentity, error) {
	if control == nil {
		return goalControlIdentity{}, publicationUncertain()
	}
	id := goalControlIdentity{domain.ID(control.JobId), domain.ID(control.ActionId), control.Revision}
	if id.JobID.Validate() != nil || id.ActionID.Validate() != nil || id.Revision == 0 {
		return id, publicationUncertain()
	}
	return id, nil
}

type goalJournal struct {
	Version     uint32                         `json:"version"`
	Control     goalControlIdentity            `json:"control"`
	ServerID    domain.ID                      `json:"server_id"`
	DeviceID    domain.ID                      `json:"device_id"`
	InstanceID  domain.ID                      `json:"instance_id"`
	ExecutionID domain.ID                      `json:"execution_id"`
	ThreadID    domain.ID                      `json:"thread_id"`
	ClaimID     domain.ID                      `json:"claim_id"`
	ReportID    domain.ID                      `json:"report_id"`
	State       string                         `json:"state"`
	Input       *domain.NativeGoalActionInput  `json:"input,omitempty"`
	Result      *domain.NativeGoalActionResult `json:"result,omitempty"`
}

func goalSnapshot(goal *codex.Goal) json.RawMessage {
	if goal == nil {
		return json.RawMessage("null")
	}
	snapshot := domain.NativeGoalSnapshot{Objective: goal.Objective, Status: domain.NativeGoalStatus(goal.Status), TokensUsed: strconv.FormatInt(goal.TokensUsed, 10), TimeUsedSeconds: strconv.FormatInt(goal.TimeUsedSeconds, 10), CreatedAt: strconv.FormatInt(goal.CreatedAt, 10), UpdatedAt: strconv.FormatInt(goal.UpdatedAt, 10)}
	if goal.TokenBudget != nil {
		b := strconv.FormatInt(*goal.TokenBudget, 10)
		snapshot.TokenBudget = &b
	}
	raw, _ := json.Marshal(snapshot)
	return raw
}
func startGoalController(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, controls <-chan *pb.GoalActionControl, mapper *CodexEventPublisher, client *codex.Client) func() error {
	owned, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		for {
			select {
			case <-owned.Done():
				done <- nil
				return
			case control, ok := <-controls:
				if !ok {
					done <- publicationUncertain()
					cancelNative()
					return
				}
				if err := mapper.deliverGoal(owned, nativeCtx, control, client); err != nil {
					done <- err
					cancelNative()
					return
				}
			}
		}
	}()
	return func() error { stop(); return <-done }
}
func (c *CodexEventPublisher) deliverGoal(ctx, nativeCtx context.Context, control *pb.GoalActionControl, client *codex.Client) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	identity, err := goalControl(control)
	if err != nil {
		return err
	}
	if c.publisher == nil || c.blocked || c.finished || c.thread == "" || identity.JobID != c.publisher.job || !c.publisher.input.NativeGoals {
		return publicationUncertain()
	}
	config := c.publisher.config
	dir := filepath.Join(config.Root, "jobs", string(identity.JobID), "goals")
	if security.PrivateDir(dir) != nil {
		return publicationUncertain()
	}
	path := filepath.Join(dir, string(identity.ActionID)+".json")
	// Existing journals are inspection-only. Reconnecting a stream cannot
	// recreate a native action or replace its original claim identity.
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return publicationUncertain()
	}
	journal := goalJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: c.publisher.execution, ThreadID: c.thread, ClaimID: domain.NewID(), ReportID: domain.NewID(), State: "prepared"}
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	claimed, err := config.Client.ClaimSessionGoalAction(bounded, authenticated(config.Credential, &pb.ClaimSessionGoalActionRequest{Mutation: &pb.Mutation{RequestId: string(journal.ClaimID), Id: string(identity.ActionID), ExpectedRevision: identity.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID)}))
	if err != nil {
		return publicationUncertain()
	}
	resource := claimed.Msg.Action
	var job domain.Job
	var input domain.NativeGoalActionInput
	if resource == nil || resource.Kind != pb.EntityKind_ENTITY_KIND_JOB || resource.Id != string(identity.ActionID) || resource.SessionId != string(c.publisher.input.SessionID) || resource.Revision != identity.Revision+1 || domain.Decode(resource.DocumentJson, &job) != nil || job.Type != domain.NativeGoalActionJob || job.State != domain.JobClaimed || job.ParentID != identity.JobID || job.MachineID != config.Credential.MachineID || job.InstanceID != config.Instance || job.AssignedDeviceID != config.Credential.DeviceID || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.ClaimID != journal.ClaimID || input.RequestID != identity.ActionID || input.ExecutionJobID != identity.JobID || input.ExecutionID != c.publisher.execution || input.NativeThreadID != c.thread {
		return publicationUncertain()
	}
	journal.Input = &input
	journal.State = "claimed"
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	result := domain.NativeGoalActionResult{Version: 1, ClaimID: journal.ClaimID, RequestID: input.RequestID, ExecutionID: input.ExecutionID, NativeThreadID: input.NativeThreadID}
	if ctx.Err() != nil {
		result.Problem = domain.NativeGoalUncertain()
	} else if input.Command == domain.NativeGoalReadCommand {
		goal, readErr := client.ReadGoal(ctx, input.RequestID)
		if readErr != nil {
			result.Problem = domain.NativeGoalUncertain()
		} else {
			result.Observation = goalSnapshot(goal)
		}
	} else {
		action := codex.GoalClearAction
		var set *codex.GoalSet
		if input.Command == domain.NativeGoalSetCommand {
			action = codex.GoalSetAction
			set = &codex.GoalSet{Objective: input.Set.Objective, TokenBudget: append(json.RawMessage(nil), input.Set.TokenBudget...)}
			if input.Set.Status != nil {
				status := codex.GoalStatus(*input.Set.Status)
				set.Status = &status
			}
		}
		mutation, nativeErr := client.MutateGoal(ctx, input.RequestID, action, set, func(intent codex.GoalIntent) error {
			if ctx.Err() != nil || intent.RequestID != input.RequestID || intent.ThreadID != c.thread || intent.Action != action {
				return publicationUncertain()
			}
			expected, _ := json.Marshal(set)
			actual, _ := json.Marshal(intent.Set)
			if string(expected) != string(actual) {
				return publicationUncertain()
			}
			journal.State = "sending"
			return writeJSON(path, journal)
		})
		if nativeErr != nil {
			result.Problem = domain.NativeGoalUncertain()
		} else if input.Command == domain.NativeGoalClearCommand {
			result.Cleared = &mutation.Cleared
		} else {
			result.Observation = goalSnapshot(mutation.Goal)
		}
	}
	journal.Result = &result
	journal.State = "observed"
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	raw, _ := json.Marshal(result)
	reportCtx, stop := context.WithTimeout(nativeCtx, 15*time.Second)
	defer stop()
	response, err := config.Client.ReportSessionGoalAction(reportCtx, authenticated(config.Credential, &pb.ReportSessionGoalActionRequest{Mutation: &pb.Mutation{RequestId: string(journal.ReportID), Id: string(identity.ActionID), ExpectedRevision: resource.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResultJson: raw}))
	if err != nil || response.Msg.Action == nil || response.Msg.Action.Id != resource.Id || response.Msg.Action.SessionId != resource.SessionId || response.Msg.Session == nil || response.Msg.Session.Id != resource.SessionId {
		return publicationUncertain()
	}
	journal.State = "reported"
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	if result.Problem != nil {
		return domain.NativeGoalUncertain()
	}
	return nil
}

// FinishGoalRun publishes only the last independently observed terminal after
// a native non-active goal boundary. The caller still owns descendant joining,
// process closure and checkpoint proof; none is inferred from this observation.
func (c *CodexEventPublisher) FinishGoalRun(ctx context.Context, goal *codex.Goal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.publisher == nil || !c.publisher.input.NativeGoals || c.goalTerminal == nil || c.finished || c.blocked || goal != nil && goal.Status == codex.GoalActive {
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionGoalObserved, Goal: &domain.NativeGoalObservation{Snapshot: goalSnapshot(goal)}}); err != nil {
		return err
	}
	outcome := domain.ExecutionSucceeded
	terminal := c.goalTerminal
	switch terminal.Turn.Status {
	case codex.TurnCompleted:
	case codex.TurnFailed:
		outcome = domain.ExecutionFailed
	case codex.TurnInterrupted:
		outcome = domain.ExecutionStopped
	default:
		return publicationUncertain()
	}
	event := domain.ExecutionEvent{Kind: domain.ExecutionTurnFinished, Outcome: outcome}
	if terminal.Turn.Problem != nil {
		event.ProblemCode = terminal.Turn.Problem.Code
	}
	if err := c.publish(ctx, event); err != nil {
		return err
	}
	c.finished = true
	return nil
}
