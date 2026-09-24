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

func approvalResponseControl(control *pb.ApprovalResponseControl) (responseControlIdentity, error) {
	if control == nil || control.Revision == 0 {
		return responseControlIdentity{}, publicationUncertain()
	}
	identity := responseControlIdentity{domain.ID(control.JobId), domain.ID(control.InteractionId), domain.ID(control.ResponseId), control.Revision}
	for _, id := range []domain.ID{identity.JobID, identity.InteractionID, identity.ResponseID} {
		if err := id.Validate(); err != nil {
			return responseControlIdentity{}, err
		}
	}
	return identity, nil
}

// Ownership journals contain only immutable metadata, never answer/approval
// content. An existing journal cannot authorize another native response send.
type approvalResponseJournal struct {
	Version     uint32                  `json:"version"`
	Control     responseControlIdentity `json:"control"`
	ServerID    domain.ID               `json:"server_id"`
	DeviceID    domain.ID               `json:"device_id"`
	InstanceID  domain.ID               `json:"instance_id"`
	ExecutionID domain.ID               `json:"execution_id"`
	ThreadID    domain.ID               `json:"thread_id"`
	TurnID      domain.ID               `json:"turn_id"`
	ClaimID     domain.ID               `json:"claim_id"`
	State       responseJournalState    `json:"state"`
	Delivery    domain.ApprovalDelivery `json:"delivery,omitempty"`
}

type nativeApprovalResponder interface {
	RespondApproval(context.Context, domain.ID, domain.ID, domain.ID, codex.ApprovalDecision) (codex.InteractionStatus, error)
	GrantPermissions(context.Context, domain.ID, domain.ID, domain.ID, codex.PermissionGrant) (codex.InteractionStatus, error)
	InspectInteraction(context.Context, domain.ID) (codex.InteractionStatus, error)
}

func startApprovalResponseController(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, controls <-chan *pb.ApprovalResponseControl, mapper *CodexEventPublisher, client nativeApprovalResponder) func() error {
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
				if err := mapper.deliverApprovalResponse(owned, nativeCtx, control, client); err != nil {
					// A targeted Stop cancels claim/send work but keeps its native
					// interruption grace period. Missing claim/delivery proof is
					// retained server-side when native closure or loss is observed.
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

func (c *CodexEventPublisher) deliverApprovalResponse(ctx, publicationCtx context.Context, control *pb.ApprovalResponseControl, client nativeApprovalResponder) error {
	// Keep a delivery publication before its queued native closure/terminal
	// publication. NextEvent may observe native state concurrently, but it
	// releases native control before waiting on this publication lock.
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.finished {
		return nil
	}
	identity, err := approvalResponseControl(control)
	if err != nil {
		return err
	}
	if c.publisher == nil || c.blocked || c.thread == "" || c.turn == "" || identity.JobID != c.publisher.job {
		return publicationUncertain()
	}
	original, exists := c.interactions[identity.InteractionID]
	if !exists || original.Type != domain.NativeApprovalInteraction {
		return publicationUncertain()
	}
	if original.Closure != "" {
		return nil // A previously emitted control cannot reopen a closed request.
	}
	if _, exists := c.approvalResponses[identity.InteractionID]; exists {
		return publicationUncertain()
	}
	config := c.publisher.config
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "approval-responses")
	if err := security.PrivateDir(directory); err != nil {
		return publicationUncertain()
	}
	path := filepath.Join(directory, string(identity.InteractionID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return publicationUncertain()
	}
	journal := approvalResponseJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: c.publisher.execution, ThreadID: c.thread, TurnID: c.turn, ClaimID: domain.NewID(), State: responsePrepared}
	if err := writeJSON(path, journal); err != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	response, err := config.Client.ClaimApprovalResponse(bounded, authenticated(config.Credential, &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{RequestId: string(journal.ClaimID), Id: string(identity.InteractionID), ExpectedRevision: identity.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
	cancel()
	if err != nil {
		return rpc.ClientError(err)
	}
	if response == nil || response.Msg == nil {
		return publicationUncertain()
	}
	record := response.Msg.Interaction
	var value domain.ExecutionInteraction
	if record == nil || record.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || record.SchemaVersion != 1 || record.Id != string(identity.InteractionID) || record.SessionId != string(c.publisher.input.SessionID) || record.Revision <= identity.Revision || domain.Decode(record.DocumentJson, &value) != nil || value.ExecutionID != c.publisher.execution || value.NativeThreadID != string(c.thread) || value.NativeTurnID != string(c.turn) || value.NativeItemID != original.NativeItemID || value.Type != original.Type || value.Closure != domain.InteractionOpen || value.ApprovalResponse == nil {
		return publicationUncertain()
	}
	originalKey, originalErr := original.NativeRequestID.Key()
	claimedKey, claimedErr := value.NativeRequestID.Key()
	if originalErr != nil || claimedErr != nil || originalKey != claimedKey {
		return publicationUncertain()
	}
	saved := value.ApprovalResponse
	claim := saved.Claim
	if saved.ID != identity.ResponseID || saved.State != domain.ApprovalResponseClaimed || saved.Delivery != nil || claim == nil || claim.ID != journal.ClaimID || claim.JobID != identity.JobID || claim.MachineID != config.Credential.MachineID || claim.InstanceID != config.Instance || claim.DeviceID != config.Credential.DeviceID || saved.Input.Validate(value.Approval) != nil {
		return publicationUncertain()
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	prepared, err := codex.PrepareApprovalResponse(value.Approval, saved.Input)
	if err != nil {
		return err
	}
	journal.State = responseSendIntent
	if err := writeJSON(path, journal); err != nil {
		return publicationUncertain()
	}
	bounded, cancel = context.WithTimeout(ctx, 15*time.Second)
	var status codex.InteractionStatus
	var sendErr error
	if prepared.Decision != nil {
		status, sendErr = client.RespondApproval(bounded, identity.ResponseID, identity.InteractionID, c.turn, *prepared.Decision)
	} else {
		status, sendErr = client.GrantPermissions(bounded, identity.ResponseID, identity.InteractionID, c.turn, *prepared.Grant)
	}
	cancel()
	delivery := domain.ApprovalDeliveryUncertain
	if status.ID == identity.InteractionID && status.TurnID == c.turn && status.ItemID == original.NativeItemID && (status.ResponseID == identity.ResponseID || (status.ResponseID == "" && status.Delivery == codex.QuestionNotSent)) {
		switch status.Delivery {
		case codex.QuestionNotSent:
			delivery = domain.ApprovalNotSent
		case codex.QuestionTransmitted:
			delivery = domain.ApprovalTransmitted
		}
	} else {
		// A definitive pre-send refusal can return no status. Inspect the
		// retained original arrival; missing/foreign evidence stays uncertain.
		bounded, cancel = context.WithTimeout(publicationCtx, 5*time.Second)
		inspected, err := client.InspectInteraction(bounded, identity.InteractionID)
		cancel()
		if err == nil && inspected.ID == identity.InteractionID && inspected.TurnID == c.turn && inspected.ItemID == original.NativeItemID && inspected.Delivery == codex.QuestionNotSent && inspected.ResponseID == "" {
			delivery = domain.ApprovalNotSent
		}
	}
	journal.State, journal.Delivery = responseObserved, delivery
	if err := writeJSON(path, journal); err != nil {
		return publicationUncertain()
	}
	bounded, cancel = context.WithTimeout(publicationCtx, 15*time.Second)
	err = c.publishApprovalDeliveryLocked(bounded, domain.ExecutionApprovalResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: journal.ClaimID, NativeItemID: original.NativeItemID, Delivery: delivery})
	cancel()
	if err != nil {
		return err
	}
	if config.Logger != nil {
		config.Logger.InfoContext(publicationCtx, "approval_response_delivery_retained", "job_id", identity.JobID, "interaction_id", identity.InteractionID, "response_id", identity.ResponseID, "claim_id", journal.ClaimID, "delivery", delivery)
	}
	if delivery == domain.ApprovalDeliveryUncertain {
		return publicationUncertain()
	}
	if sendErr != nil {
		if code := domain.SafeError(sendErr).Code; delivery == domain.ApprovalNotSent && (code == domain.Conflict || code == domain.Canceled) {
			return nil
		}
		return sendErr
	}
	if delivery != domain.ApprovalTransmitted {
		return publicationUncertain()
	}
	return nil
}
