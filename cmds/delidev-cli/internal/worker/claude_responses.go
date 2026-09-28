package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type claudeReplyClient interface {
	ReplyClaimed(context.Context, domain.ID, claude.PermissionReply, func(context.Context, claude.PermissionReplyClaim) error) error
}
type claudeResponseJournal struct {
	Version     uint32                      `json:"version"`
	Control     responseControlIdentity     `json:"control"`
	ServerID    domain.ID                   `json:"server_id"`
	DeviceID    domain.ID                   `json:"device_id"`
	InstanceID  domain.ID                   `json:"instance_id"`
	ExecutionID domain.ID                   `json:"execution_id"`
	ClaimID     domain.ID                   `json:"claim_id"`
	Native      claude.PermissionReplyClaim `json:"native"`
	State       responseJournalState        `json:"state"`
	Transmitted bool                        `json:"transmitted"`
}
type claudeResponseAttempt struct {
	journal claudeResponseJournal
	input   domain.ClaudePermissionResponse
	echoed  bool
}

func (c *ClaudeContentPublisher) DeliverQuestionResponse(ctx, publicationCtx context.Context, control *pb.QuestionResponseControl, client claudeReplyClient) error {
	identity, err := responseControl(control)
	if err != nil {
		return err
	}
	return c.deliverClaudeResponse(ctx, publicationCtx, identity, domain.UserQuestionInteraction, client)
}
func (c *ClaudeContentPublisher) DeliverApprovalResponse(ctx, publicationCtx context.Context, control *pb.ApprovalResponseControl, client claudeReplyClient) error {
	identity, err := approvalResponseControl(control)
	if err != nil {
		return err
	}
	return c.deliverClaudeResponse(ctx, publicationCtx, identity, domain.NativeApprovalInteraction, client)
}

func (c *ClaudeContentPublisher) deliverClaudeResponse(ctx, publicationCtx context.Context, identity responseControlIdentity, kind domain.InteractionType, client claudeReplyClient) error {
	if c == nil || client == nil {
		return publicationUncertain()
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	p, config := b.publisher, b.publisher.config
	if c.replyUncertain || c.resultUsage || identity.JobID != p.job {
		return publicationUncertain()
	}
	var original domain.ExecutionInteractionUpdate
	for _, r := range c.interactions {
		if r.update.ID == identity.InteractionID {
			if r.update.Type != kind {
				return b.block()
			}
			if r.closed {
				return nil
			}
			original = r.update
			break
		}
	}
	if original.Claude == nil {
		return b.block()
	}
	arrival := original.Claude.ArrivalID
	if prior := c.responses[arrival]; prior != nil {
		if prior.journal.Control == identity {
			return nil
		}
		return b.block()
	}
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "claude-responses")
	if security.PrivateDir(directory) != nil {
		return b.block()
	}
	path := filepath.Join(directory, string(identity.InteractionID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return b.block()
	}
	j := claudeResponseJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: p.execution, ClaimID: domain.NewID(), State: responsePrepared}
	if writeJSON(path, j) != nil {
		return b.block()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	var record *pb.Resource
	var err error
	meta := &pb.Mutation{RequestId: string(j.ClaimID), Id: string(identity.InteractionID), ExpectedRevision: identity.Revision}
	if kind == domain.UserQuestionInteraction {
		r, claimErr := config.Client.ClaimQuestionResponse(bounded, authenticated(config.Credential, &pb.ClaimQuestionResponseRequest{Mutation: meta, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
		err = claimErr
		if r != nil && r.Msg != nil {
			record = r.Msg.Interaction
		}
	} else {
		r, claimErr := config.Client.ClaimApprovalResponse(bounded, authenticated(config.Credential, &pb.ClaimApprovalResponseRequest{Mutation: meta, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
		err = claimErr
		if r != nil && r.Msg != nil {
			record = r.Msg.Interaction
		}
	}
	cancel()
	if err != nil {
		_ = b.block()
		return rpc.ClientError(err)
	}
	var value domain.ExecutionInteraction
	if record == nil || record.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || record.SchemaVersion != 1 || record.Id != string(identity.InteractionID) || record.SessionId != string(p.input.SessionID) || record.Revision <= identity.Revision || domain.Decode(record.DocumentJson, &value) != nil || value.ExecutionID != p.execution || value.NativeThreadID != string(b.journal.SessionID) || value.NativeTurnID != b.turn || value.NativeItemID != original.NativeItemID || value.Type != kind || value.Closure != domain.InteractionOpen || !reflect.DeepEqual(value.Claude, original.Claude) || value.NativeRequestID.Text != original.NativeRequestID.Text || value.NativeRequestID.Kind != domain.InteractionTextID || value.NativeRequestID.Number != nil || value.OpenCode != nil || value.OpenCodeStop != nil || value.OpenCodeClosure != nil || value.Questions != nil || value.Approval != nil || value.ClaudeCancellation != nil || value.ClaudeSettlement != nil {
		return b.block()
	}
	var claim *domain.QuestionResponseClaim
	var response *domain.ClaudePermissionResponse
	if kind == domain.UserQuestionInteraction {
		r := value.Response
		if r == nil || value.ApprovalResponse != nil || r.ID != identity.ResponseID || r.State != domain.QuestionResponseClaimed || r.Delivery != nil || r.Acceptance != nil || r.ClaudeEcho != nil || r.Input.ValidateInteraction(value) != nil {
			return b.block()
		}
		claim, response = r.Claim, r.Input.Claude
	} else {
		r := value.ApprovalResponse
		if r == nil || value.Response != nil || r.ID != identity.ResponseID || r.State != domain.ApprovalResponseClaimed || r.Delivery != nil || r.Acceptance != nil || r.ClaudeEcho != nil || r.Input.ValidateInteraction(value) != nil {
			return b.block()
		}
		claim, response = r.Claim, r.Input.Claude
	}
	if claim == nil || claim.ID != j.ClaimID || claim.JobID != identity.JobID || claim.MachineID != config.Credential.MachineID || claim.InstanceID != config.Instance || claim.DeviceID != config.Credential.DeviceID || response == nil {
		return b.block()
	}
	digest, err := domain.ClaudeResponseDigest(value, *response)
	if err != nil {
		return b.block()
	}
	j.Native = claude.PermissionReplyClaim{Version: 1, OwnerID: p.job, SessionID: b.journal.SessionID, InputID: b.journal.InputID, TurnID: b.turn, ArrivalID: arrival, RequestID: original.NativeRequestID.Text, ToolID: original.NativeItemID, BodyDigest: digest}
	attempt := &claudeResponseAttempt{journal: j, input: *response}
	c.responses[arrival] = attempt
	native := claude.PermissionReply{Behavior: claude.PermissionBehavior(response.Behavior), Answers: response.Answers, Interrupt: response.Interrupt != nil && *response.Interrupt}
	if response.Message != nil {
		native.Message = *response.Message
	}
	claimed := false
	bounded, cancel = context.WithTimeout(ctx, 15*time.Second)
	sendErr := client.ReplyClaimed(bounded, arrival, native, func(ctx context.Context, original claude.PermissionReplyClaim) error {
		if ctx.Err() != nil || claimed || original != j.Native {
			return publicationUncertain()
		}
		attempt.journal.State = responseSendIntent
		if writeJSON(path, attempt.journal) != nil {
			return publicationUncertain()
		}
		claimed = true
		return nil
	})
	cancel()
	delivered := claimed && sendErr == nil
	attempt.journal.State, attempt.journal.Transmitted = responseObserved, delivered
	if writeJSON(path, attempt.journal) != nil {
		return b.block()
	}
	c.replyUncertain = !delivered
	event := domain.ExecutionEvent{}
	if kind == domain.UserQuestionInteraction {
		delivery := domain.QuestionDeliveryUncertain
		if delivered {
			delivery = domain.QuestionTransmitted
		}
		event.Kind, event.QuestionResponse = domain.ExecutionQuestionDeliveryObserved, &domain.ExecutionQuestionResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: j.ClaimID, NativeItemID: original.NativeItemID, Delivery: delivery}
	} else {
		delivery := domain.ApprovalDeliveryUncertain
		if delivered {
			delivery = domain.ApprovalTransmitted
		}
		event.Kind, event.ApprovalResponse = domain.ExecutionApprovalDeliveryObserved, &domain.ExecutionApprovalResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: j.ClaimID, NativeItemID: original.NativeItemID, Delivery: delivery}
	}
	c.queue = []claudeContentCommit{{event: event}}
	bounded, cancel = context.WithTimeout(publicationCtx, 15*time.Second)
	err = c.drain(bounded)
	cancel()
	if config.Logger != nil {
		config.Logger.InfoContext(publicationCtx, "claude_response_delivery_observed", "job_id", p.job, "interaction_id", identity.InteractionID, "response_id", identity.ResponseID, "claim_id", j.ClaimID, "transmitted", delivered)
	}
	if err != nil {
		return err
	}
	if !delivered {
		return publicationUncertain()
	}
	return nil
}

func (c *ClaudeContentPublisher) publishReplyEcho(ctx context.Context, n *claude.InteractionObservation) error {
	a := c.responses[n.ArrivalID]
	r, exists := c.interactions[n.ArrivalID]
	if !exists || a == nil || a.echoed || a.journal.State != responseObserved || n.ReplyDigest != a.journal.Native.BodyDigest || n.Request != nil || n.Canceled != r.closed {
		return c.binding.block()
	}
	u := &domain.ExecutionClaudeReplyEcho{InteractionID: r.update.ID, ResponseID: a.journal.Control.ResponseID, ClaimID: a.journal.ClaimID, ArrivalID: n.ArrivalID, NativeItemID: r.update.NativeItemID, BodyDigest: n.ReplyDigest}
	if u.Validate() != nil {
		return c.binding.block()
	}
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeReplyEchoObserved, ClaudeReplyEcho: u}, replyEcho: n.ArrivalID}}
	return c.drain(ctx)
}
