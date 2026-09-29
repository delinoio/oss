package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The production implementation is the original owned controller, with fixed
// native journal callbacks. Tests inject bounded original-delivery fixtures.
type grokPublicReplyAPI interface {
	ReplyFilePermission(context.Context, domain.ID, domain.ID, grok.FilePermissionDecision) (grok.FilePermissionDelivery, error)
	InspectFilePermission(domain.ID) (grok.FilePermissionDelivery, error)
	ReplyQuestion(context.Context, domain.ID, domain.ID, grok.QuestionAnswer) (grok.QuestionDelivery, error)
	InspectQuestion(domain.ID) (grok.QuestionDelivery, error)
	ReplyPlan(context.Context, domain.ID, domain.ID, grok.PlanOutcome) (grok.PlanDelivery, error)
	InspectPlan(domain.ID) (grok.PlanDelivery, error)
}

func startGrokResponses(ctx, publication context.Context, cancelNative context.CancelFunc, config Config, c *GrokBindingPublisher, api grokPublicReplyAPI) func() error {
	owned, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		for {
			var identity responseControlIdentity
			var err error
			question := false
			select {
			case <-owned.Done():
				done <- nil
				return
			case value, ok := <-config.questionControls:
				if !ok {
					done <- publicationUncertain()
					cancelNative()
					return
				}
				identity, err = responseControl(value)
				question = true
			case value, ok := <-config.approvalControls:
				if !ok {
					done <- publicationUncertain()
					cancelNative()
					return
				}
				identity, err = approvalResponseControl(value)
			}
			if err == nil {
				err = c.deliverGrokResponse(owned, publication, identity, question, api)
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
	return func() error { stop(); return <-done }
}

func (c *GrokBindingPublisher) deliverGrokResponse(ctx, publication context.Context, identity responseControlIdentity, question bool, api grokPublicReplyAPI) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	r := c.interactions[identity.InteractionID]
	if c.stage != grokInputAccepted || identity.JobID != c.reference.JobID || r == nil || (r.request.Kind == domain.GrokQuestionInteraction) != question {
		return publicationUncertain()
	}
	if r.response != "" {
		if r.response == identity.ResponseID && r.delivered {
			return nil
		}
		return publicationUncertain()
	}
	config := c.publisher.config
	directory := filepath.Join(config.Root, "jobs", string(identity.JobID), "grok-responses")
	if security.PrivateDir(directory) != nil {
		return publicationUncertain()
	}
	path := filepath.Join(directory, string(identity.InteractionID)+".json")
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return publicationUncertain()
	}
	claimID := domain.NewID()
	journal := struct {
		Version     uint32                  `json:"version"`
		Control     responseControlIdentity `json:"control"`
		ClaimID     domain.ID               `json:"claim_id"`
		ExecutionID domain.ID               `json:"execution_id"`
		State       responseJournalState    `json:"state"`
		Delivery    domain.ApprovalDelivery `json:"delivery,omitempty"`
	}{1, identity, claimID, c.reference.ExecutionID, responsePrepared, ""}
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	bounded, cancel := context.WithTimeout(ctx, 15*time.Second)
	var record *pb.Resource
	if question {
		response, err := config.Client.ClaimQuestionResponse(bounded, authenticated(config.Credential, &pb.ClaimQuestionResponseRequest{Mutation: &pb.Mutation{RequestId: string(claimID), Id: string(identity.InteractionID), ExpectedRevision: identity.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
		cancel()
		if err != nil {
			return rpc.ClientError(err)
		}
		if response != nil && response.Msg != nil {
			record = response.Msg.Interaction
		}
	} else {
		response, err := config.Client.ClaimApprovalResponse(bounded, authenticated(config.Credential, &pb.ClaimApprovalResponseRequest{Mutation: &pb.Mutation{RequestId: string(claimID), Id: string(identity.InteractionID), ExpectedRevision: identity.Revision}, MachineId: string(config.Credential.MachineID), InstanceId: string(config.Instance), JobId: string(identity.JobID), ResponseId: string(identity.ResponseID)}))
		cancel()
		if err != nil {
			return rpc.ClientError(err)
		}
		if response != nil && response.Msg != nil {
			record = response.Msg.Interaction
		}
	}
	var value domain.ExecutionInteraction
	kind := domain.NativeApprovalInteraction
	if question {
		kind = domain.UserQuestionInteraction
	}
	original := domain.ExecutionInteraction{ExecutionID: c.reference.ExecutionID, NativeThreadID: string(c.thread), NativeTurnID: c.turn, NativeItemID: r.tool, NativeRequestID: domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: string(r.id)}, Type: kind, Grok: &r.request}
	if record == nil || record.Kind != pb.EntityKind_ENTITY_KIND_INTERACTION || record.SchemaVersion != 1 || record.Id != string(r.id) || record.SessionId != string(c.reference.SessionID) || record.Revision <= identity.Revision || domain.Decode(record.DocumentJson, &value) != nil || !sameGrokRequest(original, value) || value.Closure != domain.InteractionOpen {
		return publicationUncertain()
	}
	var claim *domain.QuestionResponseClaim
	if question {
		saved := value.Response
		if saved == nil || value.ApprovalResponse != nil || saved.ID != identity.ResponseID || saved.State != domain.QuestionResponseClaimed || saved.Delivery != nil || saved.Input.ValidateInteraction(value) != nil {
			return publicationUncertain()
		}
		claim = saved.Claim
	} else {
		saved := value.ApprovalResponse
		if saved == nil || value.Response != nil || saved.ID != identity.ResponseID || saved.State != domain.ApprovalResponseClaimed || saved.Delivery != nil || saved.Input.ValidateInteraction(value) != nil {
			return publicationUncertain()
		}
		claim = saved.Claim
	}
	if claim == nil || claim.ID != claimID || claim.JobID != identity.JobID || claim.MachineID != config.Credential.MachineID || claim.InstanceID != config.Instance || claim.DeviceID != config.Credential.DeviceID {
		return publicationUncertain()
	}
	if ctx.Err() != nil {
		return domain.SafeError(ctx.Err())
	}
	journal.State = responseSendIntent
	if writeJSON(path, journal) != nil {
		return publicationUncertain()
	}
	r.response, r.claim = identity.ResponseID, claimID
	bounded, cancel = context.WithTimeout(ctx, 15*time.Second)
	delivered := false
	var sendErr error
	switch r.request.Kind {
	case domain.GrokQuestionInteraction:
		a := value.Response.Input.Grok
		annotations := map[string]grok.QuestionAnnotation{}
		for key, v := range a.Annotations {
			annotations[key] = grok.QuestionAnnotation{Notes: v.Notes}
		}
		if a.Annotations == nil {
			annotations = nil
		}
		v, err := api.ReplyQuestion(bounded, identity.ResponseID, r.id, grok.QuestionAnswer{Outcome: grok.QuestionOutcome(a.Outcome), Answers: a.Answers, Annotations: annotations, PartialAnswers: a.PartialAnswers})
		sendErr = err
		if err != nil {
			v, _ = api.InspectQuestion(r.id)
		}
		delivered = v.Claim.RequestID == identity.ResponseID && v.Claim.ArrivalID == r.id && v.Claim.RequestDigest == r.request.RequestDigest && v.Claimed && v.Attempted && v.Delivered && v.ProblemCode == ""
	case domain.GrokFilePermission:
		v, err := api.ReplyFilePermission(bounded, identity.ResponseID, r.id, grok.FilePermissionDecision(value.ApprovalResponse.Input.Grok.Decision))
		sendErr = err
		if err != nil {
			v, _ = api.InspectFilePermission(r.id)
		}
		delivered = v.Claim.RequestID == identity.ResponseID && v.Claim.ArrivalID == r.id && v.Claim.RequestDigest == r.request.RequestDigest && v.Claimed && v.Attempted && v.Delivered && v.ProblemCode == ""
	case domain.GrokPlanApproval:
		v, err := api.ReplyPlan(bounded, identity.ResponseID, r.id, grok.PlanOutcome(value.ApprovalResponse.Input.Grok.Decision))
		sendErr = err
		if err != nil {
			v, _ = api.InspectPlan(r.id)
		}
		delivered = v.Claim.RequestID == identity.ResponseID && v.Claim.ArrivalID == r.id && v.Claim.ContentDigest == r.request.Plan.ContentDigest && v.Claim.Revision == r.request.Plan.Revision && v.Claimed && v.Attempted && v.Delivered && v.ProblemCode == ""
	}
	cancel()
	claims, err := c.readClaims()
	if err != nil {
		return c.block()
	}
	c.proof = claims
	delivery := domain.ApprovalDeliveryUncertain
	if delivered {
		delivery = domain.ApprovalTransmitted
	}
	journal.State, journal.Delivery = responseObserved, delivery
	if writeJSON(path, journal) != nil {
		return c.block()
	}
	event := domain.ExecutionEvent{Kind: domain.ExecutionApprovalDeliveryObserved, ApprovalResponse: &domain.ExecutionApprovalResponseUpdate{InteractionID: r.id, ResponseID: r.response, ClaimID: r.claim, NativeItemID: r.tool, Delivery: delivery}}
	if question {
		event.Kind, event.ApprovalResponse, event.QuestionResponse = domain.ExecutionQuestionDeliveryObserved, nil, &domain.ExecutionQuestionResponseUpdate{InteractionID: r.id, ResponseID: r.response, ClaimID: r.claim, NativeItemID: r.tool, Delivery: domain.QuestionDelivery(delivery)}
	}
	if err := c.publishPublicLocked(publication, event); err != nil {
		return err
	}
	r.delivered = delivered
	if config.Logger != nil {
		config.Logger.InfoContext(publication, "grok_response_delivery_retained", "interaction_id", r.id, "response_id", r.response, "delivery", delivery)
	}
	if !delivered || sendErr != nil {
		return publicationUncertain()
	}
	return nil
}
