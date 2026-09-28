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

// QuestionOffer identifies one original native arrival. It contains no
// response grant: the immutable proposal remains with the accepted input.
type QuestionOffer struct {
	ArrivalID      domain.ID
	ToolID         string
	RequestDigest  string
	ProposalDigest string
}

// QuestionClaim is synchronized before the one original reply. Request
// namespaces and proposal bytes are hashed; no question, answer, option or annotation is
// retained in this durable mutation record.
type QuestionClaim struct {
	Version          uint32          `json:"version"`
	OwnerID          domain.ID       `json:"owner_id"`
	ProductSessionID domain.ID       `json:"product_session_id"`
	InputRequestID   domain.ID       `json:"input_request_id"`
	RequestID        domain.ID       `json:"request_id"`
	NativeSessionID  domain.ID       `json:"native_session_id"`
	NativePromptID   string          `json:"native_prompt_id"`
	ArrivalID        domain.ID       `json:"arrival_id"`
	ToolID           string          `json:"tool_id"`
	RequestDigest    string          `json:"request_digest"`
	ProposalDigest   string          `json:"proposal_digest"`
	Outcome          QuestionOutcome `json:"outcome"`
	BodyDigest       string          `json:"body_digest"`
}

func (c QuestionClaim) Validate() error {
	if c.Outcome != QuestionAccepted && c.Outcome != QuestionCancelled && c.Outcome != QuestionSkipInterview || c.Version != 1 || !nativeUUID(c.NativePromptID, 4) || !text(c.ToolID, 256) {
		return apiConfigurationError()
	}
	seen := map[domain.ID]bool{}
	for _, id := range []domain.ID{c.OwnerID, c.ProductSessionID, c.InputRequestID, c.RequestID, c.NativeSessionID, c.ArrivalID} {
		if id.Validate() != nil || seen[id] {
			return apiConfigurationError()
		}
		seen[id] = true
	}
	for _, value := range []string{c.RequestDigest, c.ProposalDigest, c.BodyDigest} {
		digest, err := hex.DecodeString(value)
		if err != nil || len(digest) != sha256.Size || hex.EncodeToString(digest) != value {
			return apiConfigurationError()
		}
	}
	if c.Outcome == QuestionCancelled && c.BodyDigest != fileDigest([]byte(`{"outcome":"cancelled"}`)) {
		return apiConfigurationError()
	}
	return nil
}

type QuestionDelivery struct {
	Claim       QuestionClaim
	Claimed     bool
	Attempted   bool
	Delivered   bool
	Resolved    bool
	ToolPhase   fileToolPhase
	ProblemCode domain.Code
}

type questionReply struct {
	offer       QuestionOffer
	event       nativewire.Event
	proposal    []questionItem
	observation QuestionDelivery
	done        chan struct{}
}

func (c *textControl) offerQuestion(event nativewire.Event, fact questionFact) (QuestionOffer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key, err := fileToolRequestKey(event.ID)
	if !c.profile.questionsOnly() && !c.profile.mixed() || !c.running || c.terminal || fact.Request == nil || event.Kind != nativewire.ServerRequest || event.Token.Validate() != nil || err != nil || c.questions[event.Token] != nil || len(c.questions) >= 128 {
		return QuestionOffer{}, incompatible()
	}
	if c.questions == nil {
		c.questions = map[domain.ID]*questionReply{}
	}
	offer := QuestionOffer{ArrivalID: event.Token, ToolID: fact.Request.ID, RequestDigest: fileDigest([]byte(key)), ProposalDigest: fileDigest(event.Params)}
	// The sender needs only the immutable native identity. Never expose its
	// mutable RawMessage ID through a publication callback.
	raw, _ := json.Marshal(fact.Request.Questions)
	var proposal []questionItem
	if decode(raw, &proposal) != nil {
		return QuestionOffer{}, incompatible()
	}
	c.questions[event.Token] = &questionReply{offer: offer, proposal: proposal, event: nativewire.Event{Kind: event.Kind, Method: event.Method, ID: append(json.RawMessage(nil), event.ID...), Token: event.Token}}
	return offer, nil
}

func (c *textControl) inspectQuestionReply(arrival domain.ID) (QuestionDelivery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.questions[arrival]
	if r == nil || r.done == nil {
		return QuestionDelivery{}, sessionUncertain()
	}
	return r.observation, nil
}

// ReplyQuestion can run while publication of its original proposal is
// blocked. Reserving this boundary is irreversible even if persistence fails.
func (a *apiConnection) ReplyQuestion(ctx context.Context, request, arrival domain.ID, answer QuestionAnswer, record func(context.Context, QuestionClaim) error) (result QuestionDelivery, returned error) {
	if request.Validate() != nil || arrival.Validate() != nil || record == nil {
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
	r := c.questions[arrival]
	if !c.profile.questionsOnly() && !c.profile.mixed() || !c.running || c.terminal || r == nil || r.done != nil || request == a.creationRequest || request == a.modeRequest {
		c.mu.Unlock()
		return result, sessionUncertain()
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
	for _, prior := range c.plans {
		if prior.offer.ArrivalID == request || prior.done != nil && prior.observation.Claim.RequestID == request {
			c.mu.Unlock()
			return result, sessionUncertain()
		}
	}
	body, err := answer.body(r.proposal)
	if err != nil {
		c.mu.Unlock()
		return result, err
	}
	claim := QuestionClaim{Version: 1, OwnerID: a.inspection.OwnerID, ProductSessionID: a.product, InputRequestID: c.input, RequestID: request, NativeSessionID: a.session, NativePromptID: c.prompt, ArrivalID: arrival, ToolID: r.offer.ToolID, RequestDigest: r.offer.RequestDigest, ProposalDigest: r.offer.ProposalDigest, Outcome: answer.Outcome, BodyDigest: fileDigest(body)}
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
		result, _ = c.inspectQuestionReply(arrival)
		if logger := a.inspection.Logger; logger != nil {
			logger.InfoContext(ctx, "Grok Build original question reply settled", "owner_id", claim.OwnerID, "input_id", claim.InputRequestID, "request_id", request, "arrival_id", arrival, "claimed", result.Claimed, "attempted", result.Attempted, "delivered", result.Delivered, "code", result.ProblemCode)
		}
		close(r.done)
	}()
	if err := record(life, claim); err != nil {
		return result, sessionUncertain()
	}
	c.mu.Lock()
	r.observation.Claimed = true
	r.observation.Attempted = true
	c.mu.Unlock()
	if err := a.wire.Reply(life, r.event, body); err != nil {
		return result, sessionUncertain()
	}
	c.mu.Lock()
	r.observation.Delivered = true
	c.mu.Unlock()
	return result, nil
}

func (c *textControl) observeQuestionReply(ctx context.Context, fact questionFact) error {
	var id string
	if fact.Interaction != nil && fact.Interaction.Stage == questionAnswerResolved {
		id = fact.Interaction.ID
	}
	if fact.Observation != nil && fact.Observation.Phase == fileToolCompleted {
		id = fact.Observation.ID
	}
	if id == "" {
		// The earlier automatic permission resolution does not settle the
		// subsequent question, and has no client reply to acknowledge.
		return nil
	}
	c.mu.Lock()
	var reply *questionReply
	for _, candidate := range c.questions {
		if candidate.offer.ToolID == id {
			reply = candidate
			break
		}
	}
	if reply == nil || reply.done == nil {
		c.mu.Unlock()
		return incompatible()
	}
	done := reply.done
	c.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return sessionUncertain()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := &reply.observation
	if !v.Claimed || !v.Attempted || !v.Delivered || v.ProblemCode != "" {
		return sessionUncertain()
	}
	if fact.Interaction != nil {
		if v.Resolved {
			return incompatible()
		}
		v.Resolved = true
	} else {
		if !v.Resolved || v.ToolPhase != "" {
			return incompatible()
		}
		v.ToolPhase = fileToolCompleted
	}
	return nil
}

func (c *textControl) questionsSettled(tools *questionObserver) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tools == nil || len(tools.tools) != len(c.questions) {
		return false
	}
	for id, tool := range tools.tools {
		reply := c.questions[tool.arrival]
		if tool.phase != fileToolCompleted || tool.stage != questionAnswerResolved || reply == nil || reply.offer.ToolID != id || reply.done == nil {
			return false
		}
		v := reply.observation
		if !v.Claimed || !v.Attempted || !v.Delivered || !v.Resolved || v.ToolPhase != fileToolCompleted || v.ProblemCode != "" {
			return false
		}
	}
	return true
}

func (c *textControl) joinQuestionReplies() {
	c.mu.Lock()
	var pending []<-chan struct{}
	for _, reply := range c.questions {
		if reply.done != nil {
			pending = append(pending, reply.done)
		}
	}
	c.mu.Unlock()
	for _, done := range pending {
		<-done
	}
}
