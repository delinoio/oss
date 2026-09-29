package grok

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type PlanOffer struct {
	ArrivalID      domain.ID
	ToolID         string
	RequestDigest  string
	ProposalDigest string
	Origin         PlanFileOrigin
	WriteToolID    string
	ContentDigest  string
}

// PlanClaim preserves the original submitted artifact revision and exact
// native decision without persisting its content or private native file path.
type PlanClaim struct {
	Version          uint32      `json:"version"`
	OwnerID          domain.ID   `json:"owner_id"`
	ProductSessionID domain.ID   `json:"product_session_id"`
	InputRequestID   domain.ID   `json:"input_request_id"`
	RequestID        domain.ID   `json:"request_id"`
	NativeSessionID  domain.ID   `json:"native_session_id"`
	NativePromptID   string      `json:"native_prompt_id"`
	ArrivalID        domain.ID   `json:"arrival_id"`
	ToolID           string      `json:"tool_id"`
	RequestDigest    string      `json:"request_digest"`
	ProposalDigest   string      `json:"proposal_digest"`
	EntryToolID      string      `json:"entry_tool_id"`
	EntryEventID     string      `json:"entry_event_id"`
	Revision         uint64      `json:"revision"`
	WriteToolID      string      `json:"write_tool_id"`
	ContentDigest    string      `json:"content_digest"`
	Outcome          PlanOutcome `json:"outcome"`
	BodyDigest       string      `json:"body_digest"`
}

func planReplyBody(outcome PlanOutcome) (json.RawMessage, error) {
	if !outcome.valid() {
		return nil, apiConfigurationError()
	}
	return json.Marshal(struct {
		Outcome PlanOutcome `json:"outcome"`
	}{outcome})
}

func (c PlanClaim) Validate() error {
	if c.Version != 1 || !nativeUUID(c.NativePromptID, 4) || c.Revision == 0 || c.Revision > 128 || !text(c.ToolID, 256) || !text(c.EntryToolID, 256) || !text(c.WriteToolID, 256) || c.ToolID == c.EntryToolID || c.ToolID == c.WriteToolID || c.EntryToolID == c.WriteToolID {
		return apiConfigurationError()
	}
	seen := map[domain.ID]bool{}
	for _, id := range []domain.ID{c.OwnerID, c.ProductSessionID, c.InputRequestID, c.RequestID, c.NativeSessionID, c.ArrivalID} {
		if id.Validate() != nil || seen[id] {
			return apiConfigurationError()
		}
		seen[id] = true
	}
	if _, err := eventIndex(c.EntryEventID, c.NativeSessionID); err != nil {
		return apiConfigurationError()
	}
	for _, v := range []string{c.RequestDigest, c.ProposalDigest, c.ContentDigest} {
		b, err := hex.DecodeString(v)
		if err != nil || len(b) != sha256.Size || hex.EncodeToString(b) != v {
			return apiConfigurationError()
		}
	}
	body, err := planReplyBody(c.Outcome)
	if err != nil || c.BodyDigest != fileDigest(body) {
		return apiConfigurationError()
	}
	return nil
}

type PlanDelivery struct {
	Claim       PlanClaim
	Claimed     bool
	Attempted   bool
	Delivered   bool
	Resolved    bool
	ToolPhase   fileToolPhase
	ProblemCode domain.Code
}

type planReply struct {
	offer       PlanOffer
	event       nativewire.Event
	observation PlanDelivery
	done        chan struct{}
}

func (c *textControl) offerPlan(event nativewire.Event, fact planFact) (PlanOffer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key, err := fileToolRequestKey(event.ID)
	if !c.profile.planning() || !c.running || c.terminal || event.Kind != nativewire.ServerRequest || event.Token.Validate() != nil || err != nil || fact.Request == nil || fact.Artifact == nil || c.plans[event.Token] != nil || len(c.plans) >= 128 {
		return PlanOffer{}, incompatible()
	}
	if c.plans == nil {
		c.plans = map[domain.ID]*planReply{}
	}
	a := fact.Artifact
	offer := PlanOffer{ArrivalID: event.Token, ToolID: fact.Request.ID, RequestDigest: fileDigest([]byte(key)), ProposalDigest: fileDigest(event.Params), Origin: a.origin, WriteToolID: a.writeToolID, ContentDigest: hex.EncodeToString(a.contentDigest[:])}
	c.plans[event.Token] = &planReply{offer: offer, event: nativewire.Event{Kind: event.Kind, Method: event.Method, ID: append(json.RawMessage(nil), event.ID...), Token: event.Token}}
	return offer, nil
}

func (c *textControl) inspectPlanReply(arrival domain.ID) (PlanDelivery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.plans[arrival]
	if r == nil || r.done == nil {
		return PlanDelivery{}, sessionUncertain()
	}
	return r.observation, nil
}

func (a *apiConnection) ReplyPlan(ctx context.Context, request, arrival domain.ID, outcome PlanOutcome, record func(context.Context, PlanClaim) error) (result PlanDelivery, returned error) {
	body, err := planReplyBody(outcome)
	if err != nil || request.Validate() != nil || arrival.Validate() != nil || record == nil {
		return result, apiConfigurationError()
	}
	if ctx.Err() != nil {
		return result, domain.SafeError(ctx.Err())
	}
	c := a.textControl()
	if c == nil {
		return result, sessionUncertain()
	}
	c.mu.Lock()
	r := c.plans[arrival]
	if !c.profile.planning() || !c.running || c.terminal || r == nil || r.done != nil || request == a.creationRequest || request == a.modeRequest {
		c.mu.Unlock()
		return result, sessionUncertain()
	}
	for _, prior := range c.plans {
		if prior.offer.ArrivalID == request || prior.done != nil && prior.observation.Claim.RequestID == request {
			c.mu.Unlock()
			return result, sessionUncertain()
		}
	}
	for _, prior := range c.questions {
		if prior.offer.ArrivalID == request || prior.done != nil && prior.observation.Claim.RequestID == request {
			c.mu.Unlock()
			return result, sessionUncertain()
		}
	}
	for _, prior := range c.permissions {
		if prior.offer.ArrivalID == request || prior.done != nil && prior.observation.Claim.RequestID == request {
			c.mu.Unlock()
			return result, sessionUncertain()
		}
	}
	o := r.offer
	claim := PlanClaim{Version: 1, OwnerID: a.inspection.OwnerID, ProductSessionID: a.product, InputRequestID: c.input, RequestID: request, NativeSessionID: a.session, NativePromptID: c.prompt, ArrivalID: arrival, ToolID: o.ToolID, RequestDigest: o.RequestDigest, ProposalDigest: o.ProposalDigest, EntryToolID: o.Origin.EntryToolID, EntryEventID: o.Origin.EntryEventID, Revision: o.Origin.Revision, WriteToolID: o.WriteToolID, ContentDigest: o.ContentDigest, Outcome: outcome, BodyDigest: fileDigest(body)}
	if claim.Validate() != nil {
		c.mu.Unlock()
		return result, apiConfigurationError()
	}
	r.observation.Claim = claim
	r.done = make(chan struct{})
	inputDone := c.inputDone
	c.mu.Unlock()
	life, cancel := context.WithTimeout(ctx, 10*time.Second)
	watchStop, watchDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-a.wire.Done():
			cancel()
		case <-inputDone:
			cancel()
		case <-watchStop:
		}
	}()
	defer func() {
		cancel()
		if returned != nil {
			_ = a.Close()
			c.mu.Lock()
			r.observation.ProblemCode = domain.RecoveryRequired
			c.mu.Unlock()
			returned = sessionUncertain()
		}
		close(watchStop)
		<-watchDone
		result, _ = c.inspectPlanReply(arrival)
		if logger := a.inspection.Logger; logger != nil {
			logger.InfoContext(ctx, "Grok Build original Plan reply settled", "owner_id", claim.OwnerID, "input_id", claim.InputRequestID, "request_id", request, "arrival_id", arrival, "revision", claim.Revision, "claimed", result.Claimed, "attempted", result.Attempted, "delivered", result.Delivered, "code", result.ProblemCode)
		}
		close(r.done)
	}()
	if err := record(life, claim); err != nil {
		return result, sessionUncertain()
	}
	c.mu.Lock()
	r.observation.Claimed, r.observation.Attempted = true, true
	c.mu.Unlock()
	if err := a.wire.Reply(life, r.event, body); err != nil {
		return result, sessionUncertain()
	}
	c.mu.Lock()
	r.observation.Delivered = true
	c.mu.Unlock()
	return result, nil
}

func (c *textControl) observePlanReply(ctx context.Context, fact planFact) (PlanOutcome, error) {
	var id string
	if fact.Interaction != nil && fact.Interaction.Stage == planApprovalResolved {
		id = fact.Interaction.ID
	}
	if fact.Observation != nil && fact.Observation.Name == exitPlanTool && fact.Observation.Phase == fileToolCompleted {
		id = fact.Observation.ID
	}
	if id == "" {
		return "", nil
	}
	c.mu.Lock()
	var reply *planReply
	for _, r := range c.plans {
		if r.offer.ToolID == id {
			reply = r
			break
		}
	}
	if reply == nil || reply.done == nil {
		c.mu.Unlock()
		return "", incompatible()
	}
	done := reply.done
	c.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return "", sessionUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := &reply.observation
	if !v.Claimed || !v.Attempted || !v.Delivered || v.ProblemCode != "" {
		return "", sessionUncertain()
	}
	if fact.Interaction != nil {
		if v.Resolved {
			return "", incompatible()
		}
		v.Resolved = true
		return v.Claim.Outcome, nil
	}
	if !v.Resolved || v.ToolPhase != "" {
		return "", incompatible()
	}
	v.ToolPhase = fileToolCompleted
	return "", nil
}

func (c *textControl) plansSettled(o *planObserver) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if o == nil || !o.settled() {
		return false
	}
	exits := 0
	for id, tool := range o.tools {
		if tool.name != exitPlanTool {
			continue
		}
		exits++
		r := c.plans[tool.arrival]
		if r == nil || r.done == nil || r.offer.ToolID != id {
			return false
		}
		v := r.observation
		if !v.Claimed || !v.Attempted || !v.Delivered || !v.Resolved || v.ToolPhase != fileToolCompleted || v.ProblemCode != "" || v.Claim.Outcome != tool.outcome {
			return false
		}
	}
	return exits == len(c.plans)
}

func (c *textControl) joinPlanReplies() {
	c.mu.Lock()
	var pending []<-chan struct{}
	for _, r := range c.plans {
		if r.done != nil {
			pending = append(pending, r.done)
		}
	}
	c.mu.Unlock()
	for _, done := range pending {
		<-done
	}
}
