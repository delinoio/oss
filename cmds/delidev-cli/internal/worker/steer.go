package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type steerControlIdentity struct {
	JobID    domain.ID `json:"job_id"`
	SteerID  domain.ID `json:"steer_id"`
	Revision uint64    `json:"revision"`
}

func steerControl(control *pb.SteerInputControl) (steerControlIdentity, error) {
	if control == nil || control.Revision == 0 {
		return steerControlIdentity{}, publicationUncertain()
	}
	identity := steerControlIdentity{domain.ID(control.JobId), domain.ID(control.SteerId), control.Revision}
	if identity.JobID.Validate() != nil || identity.SteerID.Validate() != nil {
		return steerControlIdentity{}, publicationUncertain()
	}
	return identity, nil
}

type steerJournal struct {
	Version     uint32                        `json:"version"`
	Control     steerControlIdentity          `json:"control"`
	ServerID    domain.ID                     `json:"server_id"`
	DeviceID    domain.ID                     `json:"device_id"`
	InstanceID  domain.ID                     `json:"instance_id"`
	ExecutionID domain.ID                     `json:"execution_id"`
	ThreadID    domain.ID                     `json:"thread_id"`
	TurnID      domain.ID                     `json:"turn_id"`
	ClaimID     domain.ID                     `json:"claim_id"`
	State       responseJournalState          `json:"state"`
	Observation *domain.ExecutionSteerUpdate  `json:"observation,omitempty"`
	Input       *domain.ExecutionInputBinding `json:"input,omitempty"`
	Resolution  *domain.ExecutionSteerUpdate  `json:"resolution,omitempty"`
}

type nativeSteerer interface {
	Steer(context.Context, domain.ID, domain.ID, domain.ID, domain.SessionInput) (codex.TurnResult, error)
	InspectSteerAcceptance(context.Context, domain.ID) (codex.SteerObservation, error)
}

func startSteerController(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, controls <-chan *pb.SteerInputControl, mapper *CodexEventPublisher, client nativeSteerer) func() error {
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
				if err := mapper.deliverSteer(owned, nativeCtx, control, client); err != nil {
					if owned.Err() != nil {
						done <- nil
					} else {
						done <- err
						cancelNative()
					}
					return
				}
			}
		}
	}()
	return func() error { stop(); return <-done }
}

func (c *CodexEventPublisher) deliverSteer(ctx, publicationCtx context.Context, control *pb.SteerInputControl, client nativeSteerer) error {
	// The same lock orders claims/native delivery/publication before messages,
	// questions and terminal observations. Native event parsing releases its
	// own control lock before waiting here, so inspection can still make progress.
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.finished {
		return nil
	}
	identity, err := steerControl(control)
	if err != nil {
		return err
	}
	if c.publisher == nil || c.blocked || c.thread == "" || c.turn == "" || identity.JobID != c.publisher.job {
		return publicationUncertain()
	}
	if _, exists := c.steers[identity.SteerID]; exists {
		return publicationUncertain()
	}
	config := c.publisher.config
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "steers")
	if security.PrivateDir(directory) != nil {
		return publicationUncertain()
	}
	path := filepath.Join(directory, string(identity.SteerID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return publicationUncertain()
	}
	journal := steerJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: c.publisher.execution, ThreadID: c.thread, TurnID: c.turn, ClaimID: domain.NewID(), State: responsePrepared}
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	response, err := config.Client.ClaimSteerInput(bounded, authenticated(config.Credential, &pb.ClaimSteerInputRequest{Mutation: &pb.Mutation{RequestId: string(journal.ClaimID), Id: string(identity.SteerID), ExpectedRevision: identity.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID)}))
	cancel()
	if err != nil {
		err = rpc.ClientError(err)
		if domain.SafeError(err).Code == domain.Conflict {
			return nil
		}
		return err
	}
	if response == nil || response.Msg == nil {
		return publicationUncertain()
	}
	r, ir := response.Msg.Steer, response.Msg.Input
	var attempt domain.SteerAttempt
	var queued domain.QueuedInput
	if r == nil || ir == nil || r.Kind != pb.EntityKind_ENTITY_KIND_STEER || ir.Kind != pb.EntityKind_ENTITY_KIND_QUEUE || r.SchemaVersion != 1 || ir.SchemaVersion != 1 || r.Id != string(identity.SteerID) || r.SessionId != string(c.publisher.input.SessionID) || ir.SessionId != r.SessionId || r.Revision <= identity.Revision || domain.Decode(r.DocumentJson, &attempt) != nil || domain.Decode(ir.DocumentJson, &queued) != nil {
		return publicationUncertain()
	}
	claim := attempt.Claim
	if attempt.Version != 1 || attempt.JobID != identity.JobID || attempt.ExecutionID != c.publisher.execution || attempt.NativeThreadID != c.thread || attempt.NativeTurnID != c.turn || attempt.State != domain.SteerClaimed || attempt.Observation != nil || attempt.InputID != domain.ID(ir.Id) || attempt.InputID.Validate() != nil || attempt.Mode != c.publisher.input.Input.Mode || attempt.ContentRevision == 0 || queued.ContentRevision != attempt.ContentRevision || queued.Mode != attempt.Mode || queued.Delivery != domain.InputClaimed || queued.ExecutionID != attempt.ExecutionID || queued.NativeRequestID != identity.SteerID || domain.BindExecutionInput(attempt.InputID, queued.Prompt).PromptDigest != attempt.PromptDigest || claim == nil || claim.ID != journal.ClaimID || claim.MachineID != config.Credential.MachineID || claim.InstanceID != config.Instance || claim.DeviceID != config.Credential.DeviceID {
		return publicationUncertain()
	}
	input := domain.SessionInput{Prompt: queued.Prompt, Mode: queued.Mode}
	if input.Validate() != nil {
		return publicationUncertain()
	}
	binding := domain.BindExecutionInput(attempt.InputID, queued.Prompt)
	journal.Input = &binding
	u := domain.ExecutionSteerUpdate{SteerID: identity.SteerID, InputID: attempt.InputID, ClaimID: journal.ClaimID, Delivery: domain.SteerNotSent, Evidence: domain.SteerPreflightRejection, ProblemCode: domain.Canceled}
	if ctx.Err() == nil {
		journal.State = responseSendIntent
		if writeJSON(path, journal) != nil {
			return publicationUncertain()
		}
		bounded, cancel = context.WithTimeout(ctx, 15*time.Second)
		result, sendErr := client.Steer(bounded, identity.SteerID, attempt.InputID, c.turn, input)
		cancel()
		if sendErr == nil && result.RequestID == identity.SteerID && result.InputID == attempt.InputID && result.TurnID == c.turn {
			u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNativeAccepted, domain.SteerNativeAcknowledgment, ""
		} else {
			u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNativeUncertain, "", domain.RecoveryRequired
			// A fresh bounded read is automatic after uncertain transmission. No
			// retry of turn/steer is permitted, including after a lost native ACK.
			bounded, cancel = context.WithTimeout(publicationCtx, 15*time.Second)
			observed, inspectionErr := client.InspectSteerAcceptance(bounded, identity.SteerID)
			cancel()
			if observed.RequestID == identity.SteerID && observed.InputID == attempt.InputID && observed.ThreadID == c.thread && observed.TurnID == c.turn {
				if observed.Delivery == codex.SteerAccepted {
					u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNativeAccepted, domain.SteerEvidence(observed.Evidence), ""
					if inspectionErr != nil {
						u.ProblemCode = domain.RecoveryRequired
					}
				} else if observed.Delivery == codex.SteerNotSent && inspectionErr == nil && sendErr != nil {
					u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNotSent, domain.SteerPreflightRejection, domain.SafeError(sendErr).Code
					if observed.Evidence == codex.SteerRejection {
						u.Evidence = domain.SteerNativeRejection
					}
				}
			} else if inspectionErr != nil && domain.SafeError(inspectionErr).Code == domain.NotFound && sendErr != nil {
				code := domain.SafeError(sendErr).Code
				if code == domain.Conflict || code == domain.Unsupported || code == domain.Canceled || code == domain.InvalidArgument || code == domain.ResourceExhausted {
					u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNotSent, domain.SteerPreflightRejection, code
				}
			}
		}
	}
	if u.Validate() != nil {
		return publicationUncertain()
	}
	journal.State, journal.Observation = responseObserved, &u
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, cancel = context.WithTimeout(publicationCtx, 15*time.Second)
	err = c.publish(bounded, domain.ExecutionEvent{Kind: domain.ExecutionSteerObserved, Steer: &u})
	cancel()
	if err != nil {
		return err
	}
	c.steers[u.SteerID] = u
	if u.Delivery == domain.SteerNativeAccepted {
		c.acceptedInputs = append(c.acceptedInputs, domain.BindExecutionInput(u.InputID, queued.Prompt))
	}
	if config.Logger != nil {
		config.Logger.InfoContext(publicationCtx, "steer_delivery_retained", "job_id", identity.JobID, "steer_id", u.SteerID, "claim_id", u.ClaimID, "delivery", u.Delivery, "evidence", u.Evidence)
	}
	if u.Delivery == domain.SteerNativeUncertain || u.ProblemCode == domain.RecoveryRequired {
		return publicationUncertain()
	}
	return nil
}

// A late response is evidence about the original send, never new send authority.
// Retain both observations before publishing the resolution, even when the
// controller already canceled native execution after an inconclusive inspection.
func (c *CodexEventPublisher) publishLateSteer(ctx context.Context, previous domain.ExecutionSteerUpdate, event codex.Event) error {
	u := previous
	observed := event.Steer
	switch observed.Delivery {
	case codex.SteerAccepted:
		if event.Problem != nil || observed.Evidence != codex.SteerAcknowledgment {
			return publicationUncertain()
		}
		u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNativeAccepted, domain.SteerNativeAcknowledgment, domain.RecoveryRequired
	case codex.SteerNotSent:
		if event.Problem == nil || observed.Evidence != codex.SteerRejection {
			return publicationUncertain()
		}
		u.Delivery, u.Evidence, u.ProblemCode = domain.SteerNotSent, domain.SteerNativeRejection, event.Problem.Code
	default:
		return publicationUncertain()
	}
	if u.Validate() != nil {
		return publicationUncertain()
	}
	config := c.publisher.config
	path := filepath.Join(config.Root, "jobs", string(c.publisher.job), "steers", string(u.SteerID)+".json")
	raw, err := security.ReadPrivate(path, 64<<10)
	var journal steerJournal
	if err != nil || domain.Decode(raw, &journal) != nil || journal.Version != 1 || journal.State != responseObserved || journal.Control.JobID != c.publisher.job || journal.Control.SteerID != u.SteerID || journal.Control.Revision == 0 || journal.ServerID != config.Credential.ServerID || journal.DeviceID != config.Credential.DeviceID || journal.InstanceID != config.Instance || journal.ExecutionID != c.publisher.execution || journal.ThreadID != c.thread || journal.TurnID != c.turn || journal.ClaimID != u.ClaimID || journal.Observation == nil || *journal.Observation != previous || journal.Resolution != nil || journal.Input == nil || journal.Input.InputID != u.InputID {
		return publicationUncertain()
	}
	if _, err := domain.CheckedExecutionInputs(journal.Input.InputID, journal.Input.PromptDigest, []domain.ExecutionInputBinding{*journal.Input}); err != nil {
		return publicationUncertain()
	}
	journal.Resolution = &u
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := c.publish(bounded, domain.ExecutionEvent{Kind: domain.ExecutionSteerObserved, Steer: &u}); err != nil {
		return err
	}
	c.steers[u.SteerID] = u
	if u.Delivery == domain.SteerNativeAccepted {
		c.acceptedInputs = append(c.acceptedInputs, *journal.Input)
	}
	if config.Logger != nil {
		config.Logger.InfoContext(bounded, "steer_late_resolution_retained", "job_id", c.publisher.job, "steer_id", u.SteerID, "claim_id", u.ClaimID, "delivery", u.Delivery)
	}
	return nil
}
