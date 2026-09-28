package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Content remains in the server response and original native process. This
// journal stores only ownership, a digest and delivery classification; retained
// state can never authorize a replacement process to resend the response.
type openCodeResponseJournal struct {
	Version      uint32                  `json:"version"`
	Control      responseControlIdentity `json:"control"`
	ServerID     domain.ID               `json:"server_id"`
	DeviceID     domain.ID               `json:"device_id"`
	InstanceID   domain.ID               `json:"instance_id"`
	ExecutionID  domain.ID               `json:"execution_id"`
	ClaimID      domain.ID               `json:"claim_id"`
	Native       opencode.SessionClaim   `json:"native"`
	State        responseJournalState    `json:"state"`
	HTTPAccepted bool                    `json:"http_accepted"`
}

type openCodeResponseAttempt struct {
	journal           openCodeResponseJournal
	accepted          bool
	original          domain.ExecutionInteractionUpdate
	decision          domain.OpenCodePermissionDecision
	rejected          bool
	correction        bool
	feedbackRequested bool
}

func (c *OpenCodeEventPublisher) DeliverQuestionResponse(ctx, publicationCtx context.Context, control *pb.QuestionResponseControl) error {
	identity, err := responseControl(control)
	if err != nil {
		return err
	}
	return c.deliverOpenCodeResponse(ctx, publicationCtx, identity, domain.UserQuestionInteraction)
}

func (c *OpenCodeEventPublisher) DeliverApprovalResponse(ctx, publicationCtx context.Context, control *pb.ApprovalResponseControl) error {
	identity, err := approvalResponseControl(control)
	if err != nil {
		return err
	}
	return c.deliverOpenCodeResponse(ctx, publicationCtx, identity, domain.NativeApprovalInteraction)
}

func (c *OpenCodeEventPublisher) deliverOpenCodeResponse(ctx, publicationCtx context.Context, identity responseControlIdentity, kind domain.InteractionType) error {
	if c == nil || c.api == nil || c.text == nil {
		return publicationUncertain()
	}
	// The original reader may observe the reply while HTTP completes. Publish
	// delivery before consuming that queued native observation under this lock.
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil || c.finished || c.stopRequest != "" {
		return nil
	}
	b := c.text.binding
	p := b.publisher
	if c.blocked || identity.JobID != p.job {
		return publicationUncertain()
	}
	var original domain.ExecutionInteractionUpdate
	for _, value := range c.interactions {
		if value.ID == identity.InteractionID {
			original = value
			break
		}
	}
	if original.ID == "" {
		for _, closed := range c.closedInteractions {
			if closed.original.ID == identity.InteractionID && closed.original.Type == kind && closed.original.OpenCode != nil {
				// A server control emitted before original native policy closure
				// may arrive afterwards. Positive original closure is authoritative:
				// do not claim, send or publish another response to that request.
				return nil
			}
		}
	}
	if original.ID == "" || original.Type != kind || original.OpenCode == nil || c.responses[original.NativeRequestID.Text] != nil {
		return c.fail(publicationUncertain())
	}
	config := p.config
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "opencode-responses")
	if security.PrivateDir(directory) != nil {
		return c.fail(publicationUncertain())
	}
	path := filepath.Join(directory, string(identity.InteractionID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return c.fail(publicationUncertain())
	}
	journal := openCodeResponseJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: p.execution, ClaimID: domain.NewID(), State: responsePrepared}
	if writeJSON(path, journal) != nil {
		return c.fail(publicationUncertain())
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	var record *pb.Resource
	var err error
	meta := &pb.Mutation{RequestId: string(journal.ClaimID), Id: string(identity.InteractionID), ExpectedRevision: identity.Revision}
	if kind == domain.UserQuestionInteraction {
		response, claimErr := config.Client.ClaimQuestionResponse(bounded, authenticated(config.Credential, &pb.ClaimQuestionResponseRequest{Mutation: meta, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
		err = claimErr
		if response != nil && response.Msg != nil {
			record = response.Msg.Interaction
		}
	} else {
		response, claimErr := config.Client.ClaimApprovalResponse(bounded, authenticated(config.Credential, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
		err = claimErr
		if response != nil && response.Msg != nil {
			record = response.Msg.Interaction
		}
	}
	cancel()
	if err != nil {
		return c.fail(rpc.ClientError(err))
	}
	var value domain.ExecutionInteraction
	if record == nil || record.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || record.SchemaVersion != 1 || record.Id != string(identity.InteractionID) || record.SessionId != string(p.input.SessionID) || record.Revision <= identity.Revision || domain.Decode(record.DocumentJson, &value) != nil || value.ExecutionID != p.execution || value.NativeThreadID != b.thread || value.NativeTurnID != b.turn || value.NativeItemID != original.NativeItemID || value.Type != kind || value.Closure != domain.InteractionOpen || !reflect.DeepEqual(value.OpenCode, original.OpenCode) || value.NativeRequestID.Text != original.NativeRequestID.Text || value.NativeRequestID.Kind != domain.InteractionTextID || value.NativeRequestID.Number != nil || value.Questions != nil || value.Approval != nil {
		return c.fail(publicationUncertain())
	}
	var claim *domain.QuestionResponseClaim
	var native opencode.InteractionResponse
	var question *domain.OpenCodeQuestionResponse
	var permission *domain.OpenCodePermissionResponse
	mutation := opencode.ReplyQuestionMutation
	if kind == domain.UserQuestionInteraction {
		r := value.Response
		if value.ApprovalResponse != nil || r == nil || r.ID != identity.ResponseID || r.State != domain.QuestionResponseClaimed || r.Delivery != nil || r.Acceptance != nil || r.Input.ValidateInteraction(value) != nil {
			return c.fail(publicationUncertain())
		}
		claim, question, native.Answers = r.Claim, r.Input.OpenCode, r.Input.OpenCode.Answers
		native.Reject = question.Reject
		if native.Reject {
			mutation = opencode.RejectQuestionMutation
		}
	} else {
		r := value.ApprovalResponse
		if value.Response != nil || r == nil || r.ID != identity.ResponseID || r.State != domain.ApprovalResponseClaimed || r.Delivery != nil || r.Acceptance != nil || r.Input.ValidateInteraction(value) != nil {
			return c.fail(publicationUncertain())
		}
		claim, permission = r.Claim, r.Input.OpenCode
		decision := opencode.PermissionDecision(permission.Decision)
		native.Decision, native.Feedback, mutation = &decision, permission.Feedback, opencode.ReplyPermissionMutation
	}
	if claim == nil || claim.ID != journal.ClaimID || claim.JobID != identity.JobID || claim.MachineID != config.Credential.MachineID || claim.InstanceID != config.Instance || claim.DeviceID != config.Credential.DeviceID {
		return c.fail(publicationUncertain())
	}
	digest, err := domain.OpenCodeResponseDigest(question, permission)
	if err != nil {
		return c.fail(err)
	}
	journal.Native = opencode.SessionClaim{RequestID: identity.ResponseID, Kind: mutation, SessionID: b.thread, MessageID: original.OpenCode.NativeMessageID, PartID: original.NativeItemID, BodyDigest: digest, InputRequestID: b.reference.InputRequestID, InteractionID: original.NativeRequestID.Text, ArrivalID: original.OpenCode.NativeEventID, CallID: original.OpenCode.CallID}
	if journal.Native.Validate() != nil || ctx.Err() != nil {
		return c.fail(publicationUncertain())
	}
	journal.State = responseSendIntent
	if writeJSON(path, journal) != nil {
		return c.fail(publicationUncertain())
	}
	b.mu.Lock()
	claims, err := b.readClaims()
	valid := err == nil && b.validPublicationClaims(claims) && b.stage == openCodeAccepted && !c.text.blocked && b.expectedReply == nil
	if valid {
		expected := journal.Native
		b.expectedReply = &expected
	}
	b.mu.Unlock()
	if !valid {
		return c.fail(publicationUncertain())
	}
	attempt := &openCodeResponseAttempt{journal: journal, original: original, rejected: question != nil && question.Reject}
	if permission != nil {
		attempt.decision = permission.Decision
		attempt.rejected = permission.Decision == domain.OpenCodePermissionReject
		attempt.feedbackRequested = permission.Feedback != nil
		attempt.correction = permission.Feedback != nil && *permission.Feedback != ""
	}
	if c.responses == nil {
		c.responses = map[string]*openCodeResponseAttempt{}
	}
	c.responses[original.NativeRequestID.Text] = attempt
	bounded, cancel = context.WithTimeout(ctx, 15*time.Second)
	receipt, sendErr := c.api.Reply(bounded, identity.ResponseID, original.NativeRequestID.Text, native)
	cancel()
	b.mu.Lock()
	claims, err = b.readClaims()
	valid = err == nil && b.validPublicationClaims(claims) && b.expectedReply == nil && len(b.replyClaims) != 0 && b.replyClaims[len(b.replyClaims)-1] == journal.Native
	b.mu.Unlock()
	delivered := valid && receipt.RequestID == identity.ResponseID && receipt.InputRequestID == b.reference.InputRequestID && receipt.InteractionID == original.NativeRequestID.Text && receipt.ArrivalID == original.OpenCode.NativeEventID && receipt.HTTPAccepted && receipt.FeedbackRequested == attempt.feedbackRequested
	attempt.journal.State, attempt.journal.HTTPAccepted = responseObserved, delivered
	if writeJSON(path, attempt.journal) != nil {
		return c.fail(publicationUncertain())
	}
	event := domain.ExecutionEvent{NativeThreadID: b.thread, NativeTurnID: b.turn}
	if kind == domain.UserQuestionInteraction {
		delivery := domain.QuestionDeliveryUncertain
		if delivered {
			delivery = domain.QuestionTransmitted
		}
		event.Kind, event.QuestionResponse = domain.ExecutionQuestionDeliveryObserved, &domain.ExecutionQuestionResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: journal.ClaimID, NativeItemID: original.NativeItemID, Delivery: delivery}
	} else {
		delivery := domain.ApprovalDeliveryUncertain
		if delivered {
			delivery = domain.ApprovalTransmitted
		}
		event.Kind, event.ApprovalResponse = domain.ExecutionApprovalDeliveryObserved, &domain.ExecutionApprovalResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: journal.ClaimID, NativeItemID: original.NativeItemID, Delivery: delivery}
	}
	bounded, cancel = context.WithTimeout(publicationCtx, 15*time.Second)
	err = p.Publish(bounded, event)
	cancel()
	if err != nil {
		return c.fail(err)
	}
	if config.Logger != nil {
		config.Logger.InfoContext(publicationCtx, "opencode_response_delivery_retained", "job_id", p.job, "interaction_id", identity.InteractionID, "response_id", identity.ResponseID, "claim_id", journal.ClaimID, "http_accepted", delivered)
	}
	if !delivered || sendErr != nil {
		return c.fail(publicationUncertain())
	}
	return nil
}
