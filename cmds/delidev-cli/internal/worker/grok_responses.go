package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type grokPublicResponseJournal struct {
	Version     uint32                  `json:"version"`
	Control     responseControlIdentity `json:"control"`
	ServerID    domain.ID               `json:"server_id"`
	DeviceID    domain.ID               `json:"device_id"`
	InstanceID  domain.ID               `json:"instance_id"`
	ExecutionID domain.ID               `json:"execution_id"`
	ClaimID     domain.ID               `json:"claim_id"`
	State       responseJournalState    `json:"state"`
	Transmitted bool                    `json:"transmitted"`
}

func (c *GrokEventPublisher) DeliverQuestion(ctx, publicationCtx context.Context, control *pb.QuestionResponseControl, api *grok.OwnedAPI) error {
	identity, err := responseControl(control)
	if err != nil {
		return err
	}
	return c.deliver(ctx, publicationCtx, identity, domain.UserQuestionInteraction, api)
}
func (c *GrokEventPublisher) DeliverApproval(ctx, publicationCtx context.Context, control *pb.ApprovalResponseControl, api *grok.OwnedAPI) error {
	identity, err := approvalResponseControl(control)
	if err != nil {
		return err
	}
	return c.deliver(ctx, publicationCtx, identity, domain.NativeApprovalInteraction, api)
}

// The narrow reply boundary keeps receipt publication independent of native I/O.
// Production callers pass only the original OwnedAPI for the accepted input.
type grokReplyAPI interface {
	ReplyQuestion(context.Context, domain.ID, domain.ID, grok.QuestionAnswer) (grok.QuestionDelivery, error)
	ReplyFilePermission(context.Context, domain.ID, domain.ID, grok.FilePermissionDecision) (grok.FilePermissionDelivery, error)
	ReplyPlan(context.Context, domain.ID, domain.ID, grok.PlanOutcome) (grok.PlanDelivery, error)
}

func (c *GrokEventPublisher) deliver(ctx, publicationCtx context.Context, identity responseControlIdentity, kind domain.InteractionType, api grokReplyAPI) error {
	if c == nil || api == nil {
		return publicationUncertain()
	}
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	config := b.publisher.config
	claims, err := b.readClaims()
	if err != nil || b.stage != grokInputAccepted || !b.acceptStopExtension(claims) || identity.JobID != b.publisher.job {
		return b.block()
	}
	var original domain.ExecutionInteractionUpdate
	for _, value := range c.interactions {
		if value.ID == identity.InteractionID {
			original = value
			break
		}
	}
	if original.Grok == nil || original.Type != kind {
		return b.block()
	}
	arrival := original.Grok.Event.ArrivalID
	if prior, ok := c.controls[arrival]; ok {
		if prior == identity {
			return nil
		}
		return b.block()
	}
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "grok-responses")
	if security.PrivateDir(directory) != nil {
		return b.block()
	}
	path := filepath.Join(directory, string(identity.InteractionID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return b.block()
	}
	j := grokPublicResponseJournal{Version: 1, Control: identity, ServerID: config.Credential.ServerID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: b.publisher.execution, ClaimID: domain.NewID(), State: responsePrepared}
	if writeJSON(path, j) != nil {
		return b.block()
	}
	c.controls[arrival] = identity
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	meta := &pb.Mutation{RequestId: string(j.ClaimID), Id: string(identity.InteractionID), ExpectedRevision: identity.Revision}
	var record *pb.Resource
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
	if record == nil || record.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || record.SchemaVersion != 1 || record.Id != string(identity.InteractionID) || record.SessionId != string(b.reference.SessionID) || record.Revision <= identity.Revision || domain.Decode(record.DocumentJson, &value) != nil || value.ExecutionID != b.publisher.execution || value.NativeThreadID != string(b.thread) || value.NativeTurnID != b.turn || value.NativeItemID != original.NativeItemID || value.Type != kind || value.Closure != domain.InteractionOpen || !reflect.DeepEqual(value.Grok, original.Grok) || !reflect.DeepEqual(value.NativeRequestID, original.NativeRequestID) || value.Claude != nil || value.OpenCode != nil || value.Questions != nil || value.Approval != nil {
		return b.block()
	}
	var claim *domain.QuestionResponseClaim
	if kind == domain.UserQuestionInteraction {
		r := value.Response
		if r == nil || value.ApprovalResponse != nil || r.ID != identity.ResponseID || r.State != domain.QuestionResponseClaimed || r.Delivery != nil || r.Acceptance != nil || r.Input.ValidateInteraction(value) != nil || grok.ValidatePublicQuestion(value.Grok, *r.Input.Grok) != nil {
			return b.block()
		}
		claim = r.Claim
	} else {
		r := value.ApprovalResponse
		if r == nil || value.Response != nil || r.ID != identity.ResponseID || r.State != domain.ApprovalResponseClaimed || r.Delivery != nil || r.Acceptance != nil || r.Input.ValidateInteraction(value) != nil {
			return b.block()
		}
		claim = r.Claim
	}
	if claim == nil || claim.ID != j.ClaimID || claim.JobID != identity.JobID || claim.MachineID != config.Credential.MachineID || claim.InstanceID != config.Instance || claim.DeviceID != config.Credential.DeviceID {
		return b.block()
	}
	c.replies[arrival] = value
	j.State = responseSendIntent
	if writeJSON(path, j) != nil {
		return b.block()
	}
	bounded, cancel = context.WithTimeout(ctx, 15*time.Second)
	claimed, delivered := false, false
	var sendErr error
	if kind == domain.UserQuestionInteraction {
		r := value.Response.Input.Grok
		annotations := map[string]grok.QuestionAnnotation{}
		if r.Annotations == nil {
			annotations = nil
		} else {
			for key, value := range r.Annotations {
				annotations[key] = grok.QuestionAnnotation{Notes: value.Notes}
			}
		}
		result, e := api.ReplyQuestion(bounded, identity.ResponseID, arrival, grok.QuestionAnswer{Outcome: grok.QuestionOutcome(r.Outcome), Answers: r.Answers, Annotations: annotations, PartialAnswers: r.PartialAnswers})
		claimed, delivered, sendErr = result.Claimed, result.Delivered, e
	} else if original.Grok.Event.Method == domain.GrokFilePermissionMethod {
		result, e := api.ReplyFilePermission(bounded, identity.ResponseID, arrival, grok.FilePermissionDecision(value.ApprovalResponse.Input.Grok.Decision))
		claimed, delivered, sendErr = result.Claimed, result.Delivered, e
	} else {
		result, e := api.ReplyPlan(bounded, identity.ResponseID, arrival, grok.PlanOutcome(value.ApprovalResponse.Input.Grok.Outcome))
		claimed, delivered, sendErr = result.Claimed, result.Delivered, e
	}
	cancel()
	delivered = claimed && delivered && sendErr == nil
	j.State, j.Transmitted = responseObserved, delivered
	if writeJSON(path, j) != nil {
		return b.block()
	}
	event := domain.ExecutionEvent{}
	if kind == domain.UserQuestionInteraction {
		state := domain.QuestionDeliveryUncertain
		if delivered {
			state = domain.QuestionTransmitted
		} else if !claimed {
			state = domain.QuestionNotSent
		}
		event.Kind, event.QuestionResponse = domain.ExecutionQuestionDeliveryObserved, &domain.ExecutionQuestionResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: j.ClaimID, NativeItemID: original.NativeItemID, Delivery: state}
		value.Response.Delivery = &domain.QuestionDeliveryObservation{State: state, Sequence: b.sequence + 1}
		value.Response.State = domain.QuestionResponseTransmitted
		if state == domain.QuestionNotSent {
			value.Response.State = domain.QuestionResponseCanceled
		} else if state == domain.QuestionDeliveryUncertain {
			value.Response.State = domain.QuestionResponseUncertain
		}
	} else {
		state := domain.ApprovalDeliveryUncertain
		if delivered {
			state = domain.ApprovalTransmitted
		} else if !claimed {
			state = domain.ApprovalNotSent
		}
		event.Kind, event.ApprovalResponse = domain.ExecutionApprovalDeliveryObserved, &domain.ExecutionApprovalResponseUpdate{InteractionID: identity.InteractionID, ResponseID: identity.ResponseID, ClaimID: j.ClaimID, NativeItemID: original.NativeItemID, Delivery: state}
		value.ApprovalResponse.Delivery = &domain.ApprovalDeliveryObservation{State: state, Sequence: b.sequence + 1}
		value.ApprovalResponse.State = domain.ApprovalResponseTransmitted
		if state == domain.ApprovalNotSent {
			value.ApprovalResponse.State = domain.ApprovalResponseCanceled
		} else if state == domain.ApprovalDeliveryUncertain {
			value.ApprovalResponse.State = domain.ApprovalResponseUncertain
		}
	}
	bounded, cancel = context.WithTimeout(publicationCtx, 15*time.Second)
	err = b.publishExtra(bounded, event)
	cancel()
	if err != nil {
		return err
	}
	c.replies[arrival] = value
	if config.Logger != nil {
		config.Logger.InfoContext(publicationCtx, "grok_response_delivery_observed", "interaction_id", identity.InteractionID, "response_id", identity.ResponseID, "claim_id", j.ClaimID, "transmitted", delivered)
	}
	if !delivered {
		return b.block()
	}
	return nil
}

// Control delivery is serialized with original publication. Targeted Stop and
// revocation cancel admission/sends, while native cleanup remains separately joined.
func startGrokControls(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, config Config, publisher *GrokEventPublisher, api *grok.OwnedAPI) func() error {
	owned, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		for {
			var err error
			select {
			case <-owned.Done():
				done <- nil
				return
			case control, ok := <-config.questionControls:
				if !ok {
					err = publicationUncertain()
				} else {
					err = publisher.DeliverQuestion(owned, nativeCtx, control, api)
				}
			case control, ok := <-config.approvalControls:
				if !ok {
					err = publicationUncertain()
				} else {
					err = publisher.DeliverApproval(owned, nativeCtx, control, api)
				}
			case <-config.steerControls:
				err = domain.Fail(domain.Unsupported, "Grok Steer requires a separate original-input profile.", "Retain the original input without substituting another response.")
			}
			if err != nil {
				if owned.Err() != nil {
					done <- nil
				} else {
					done <- err
					cancelNative()
				}
				return
			}
		}
	}()
	var once sync.Once
	var result error
	return func() error { once.Do(func() { stop(); result = <-done }); return result }
}
