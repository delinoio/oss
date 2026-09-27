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

type FilePermissionDecision string

const (
	AllowFileOnce  FilePermissionDecision = "allow-once"
	RejectFileOnce FilePermissionDecision = "reject-once"
)

type filePermissionAnswer struct {
	Outcome struct {
		Outcome string                 `json:"outcome"`
		Option  FilePermissionDecision `json:"optionId"`
	} `json:"outcome"`
}

func fileAnswer(decision FilePermissionDecision) (filePermissionAnswer, error) {
	var answer filePermissionAnswer
	if decision != AllowFileOnce && decision != RejectFileOnce {
		return answer, incompatible()
	}
	answer.Outcome.Outcome, answer.Outcome.Option = "selected", decision
	return answer, nil
}

func fileDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// FilePermissionOffer identifies one original native arrival. It contains no
// response grant: the immutable proposal remains with the accepted input.
type FilePermissionOffer struct {
	ArrivalID      domain.ID
	ToolID         string
	RequestDigest  string
	ProposalDigest string
}

// FilePermissionClaim is synchronized before the one original reply. Request
// namespaces and proposal bytes are hashed; no path, file contents or label is
// retained in this durable mutation record.
type FilePermissionClaim struct {
	Version          uint32                 `json:"version"`
	OwnerID          domain.ID              `json:"owner_id"`
	ProductSessionID domain.ID              `json:"product_session_id"`
	InputRequestID   domain.ID              `json:"input_request_id"`
	RequestID        domain.ID              `json:"request_id"`
	NativeSessionID  domain.ID              `json:"native_session_id"`
	NativePromptID   string                 `json:"native_prompt_id"`
	ArrivalID        domain.ID              `json:"arrival_id"`
	ToolID           string                 `json:"tool_id"`
	RequestDigest    string                 `json:"request_digest"`
	ProposalDigest   string                 `json:"proposal_digest"`
	Decision         FilePermissionDecision `json:"decision"`
	BodyDigest       string                 `json:"body_digest"`
}

func (c FilePermissionClaim) Validate() error {
	answer, err := fileAnswer(c.Decision)
	if err != nil || c.Version != 1 || !nativeUUID(c.NativePromptID, 4) || !text(c.ToolID, 256) {
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
	raw, _ := json.Marshal(answer)
	if c.BodyDigest != fileDigest(raw) {
		return apiConfigurationError()
	}
	return nil
}

type FilePermissionDelivery struct {
	Claim       FilePermissionClaim
	Claimed     bool
	Attempted   bool
	Delivered   bool
	Resolved    bool
	ToolPhase   fileToolPhase
	ProblemCode domain.Code
}

type fileReply struct {
	offer       FilePermissionOffer
	event       nativewire.Event
	observation FilePermissionDelivery
	done        chan struct{}
}

func (c *textControl) offerFilePermission(event nativewire.Event, fact fileToolFact) (FilePermissionOffer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key, err := fileToolRequestKey(event.ID)
	if c.profile != fileWriteInput || !c.running || c.terminal || fact.Permission == nil || event.Kind != nativewire.ServerRequest || event.Token.Validate() != nil || err != nil || c.permissions[event.Token] != nil || len(c.permissions) >= 128 {
		return FilePermissionOffer{}, incompatible()
	}
	if c.permissions == nil {
		c.permissions = map[domain.ID]*fileReply{}
	}
	offer := FilePermissionOffer{ArrivalID: event.Token, ToolID: fact.Permission.Tool.ID, RequestDigest: fileDigest([]byte(key)), ProposalDigest: fileDigest(event.Params)}
	// The sender needs only the immutable native identity. Never expose its
	// mutable RawMessage ID through a publication callback.
	c.permissions[event.Token] = &fileReply{offer: offer, event: nativewire.Event{Kind: event.Kind, Method: event.Method, ID: append(json.RawMessage(nil), event.ID...), Token: event.Token}}
	return offer, nil
}

func (c *textControl) inspectFileReply(arrival domain.ID) (FilePermissionDelivery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.permissions[arrival]
	if r == nil || r.done == nil {
		return FilePermissionDelivery{}, sessionUncertain()
	}
	return r.observation, nil
}

// ReplyFilePermission can run while publication of its original proposal is
// blocked. Reserving this boundary is irreversible even if persistence fails.
func (a *apiConnection) ReplyFilePermission(ctx context.Context, request, arrival domain.ID, decision FilePermissionDecision, record func(context.Context, FilePermissionClaim) error) (result FilePermissionDelivery, returned error) {
	answer, err := fileAnswer(decision)
	if err != nil {
		return result, err
	}
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
	r := c.permissions[arrival]
	if c.profile != fileWriteInput || !c.running || c.terminal || r == nil || r.done != nil || request == a.creationRequest {
		c.mu.Unlock()
		return result, sessionUncertain()
	}
	for _, prior := range c.permissions {
		if prior.done != nil && prior.observation.Claim.RequestID == request {
			c.mu.Unlock()
			return result, sessionUncertain()
		}
	}
	body, _ := json.Marshal(answer)
	claim := FilePermissionClaim{Version: 1, OwnerID: a.inspection.OwnerID, ProductSessionID: a.product, InputRequestID: c.input, RequestID: request, NativeSessionID: a.session, NativePromptID: c.prompt, ArrivalID: arrival, ToolID: r.offer.ToolID, RequestDigest: r.offer.RequestDigest, ProposalDigest: r.offer.ProposalDigest, Decision: decision, BodyDigest: fileDigest(body)}
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
		result, _ = c.inspectFileReply(arrival)
		if logger := a.inspection.Logger; logger != nil {
			logger.InfoContext(ctx, "Grok Build original file permission reply settled", "owner_id", claim.OwnerID, "input_id", claim.InputRequestID, "request_id", request, "arrival_id", arrival, "claimed", result.Claimed, "attempted", result.Attempted, "delivered", result.Delivered, "code", result.ProblemCode)
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
	if err := a.wire.Reply(life, r.event, answer); err != nil {
		return result, sessionUncertain()
	}
	c.mu.Lock()
	r.observation.Delivered = true
	c.mu.Unlock()
	return result, nil
}

// Resolve only after the original sender has settled. A native resolution has
// no decision echo; it must never itself authorize an answer or a successful
// tool. Completed and failed tools are checked separately against that answer.
func (c *textControl) observeFileReply(ctx context.Context, fact fileToolFact) error {
	var id string
	if fact.Interaction != nil && fact.Interaction.Kind == fileInteractionResolved {
		id = fact.Interaction.ID
	}
	if fact.Observation != nil && (fact.Observation.Phase == fileToolCompleted || fact.Observation.Phase == fileToolFailed) {
		id = fact.Observation.ID
	}
	if id == "" {
		return nil
	}
	c.mu.Lock()
	var reply *fileReply
	for _, candidate := range c.permissions {
		if candidate.offer.ToolID == id {
			reply = candidate
			break
		}
	}
	if reply == nil {
		c.mu.Unlock()
		return nil
	} // A Read has no client request.
	done := reply.done
	c.mu.Unlock()
	if done == nil {
		return incompatible()
	}
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
		phase := fact.Observation.Phase
		if !v.Resolved || v.ToolPhase != "" || phase == fileToolCompleted && v.Claim.Decision != AllowFileOnce || phase == fileToolFailed && v.Claim.Decision != RejectFileOnce {
			return incompatible()
		}
		v.ToolPhase = phase
	}
	return nil
}

func (c *textControl) filesSettled(tools *fileToolObserver, rejected bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tools == nil {
		return false
	}
	failed := 0
	for _, tool := range tools.tools {
		if !tool.resolved {
			return false
		}
		if tool.phase == fileToolFailed {
			failed++
		} else if tool.phase != fileToolCompleted {
			return false
		}
	}
	denied := 0
	for _, reply := range c.permissions {
		v := reply.observation
		if reply.done == nil || !v.Claimed || !v.Attempted || !v.Delivered || !v.Resolved || v.ProblemCode != "" {
			return false
		}
		if v.Claim.Decision == RejectFileOnce {
			if v.ToolPhase != fileToolFailed {
				return false
			}
			denied++
		} else if v.ToolPhase != fileToolCompleted {
			return false
		}
	}
	return !rejected && failed == 0 && denied == 0 || rejected && failed == 1 && denied == 1
}

func (c *textControl) joinFileReplies() {
	c.mu.Lock()
	var pending []<-chan struct{}
	for _, reply := range c.permissions {
		if reply.done != nil {
			pending = append(pending, reply.done)
		}
	}
	c.mu.Unlock()
	for _, done := range pending {
		<-done
	}
}
