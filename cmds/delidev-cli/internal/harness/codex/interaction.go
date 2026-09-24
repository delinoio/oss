package codex

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type InteractionClosure string
type QuestionDelivery string

const (
	InteractionOpen           InteractionClosure = "open"
	InteractionNativeClosed   InteractionClosure = "native-closed"
	InteractionTurnEnded      InteractionClosure = "turn-ended"
	QuestionNotSent           QuestionDelivery   = "not-sent"
	QuestionTransmitted       QuestionDelivery   = "transmitted"
	QuestionDeliveryUncertain QuestionDelivery   = "uncertain"
)

// Closure and pipe delivery are independent facts. Neither confirms that the
// native tool accepted an answer. Native acceptance needs separate evidence.
type InteractionStatus struct {
	ID         domain.ID
	TurnID     domain.ID
	ItemID     string
	Closure    InteractionClosure
	ResponseID domain.ID
	Delivery   QuestionDelivery
}

type trackedInteraction struct {
	status    InteractionStatus
	native    nativewire.Event
	questions *QuestionRequest
	bytes     int
}

type interactionState struct {
	arrivals  map[domain.ID]*trackedInteraction
	native    map[string]domain.ID
	responses map[domain.ID]bool
	bytes     int
	open      int
}

const (
	maxTrackedInteractions = 4096
	maxOpenInteractions    = 128
	maxQuestionBytes       = 8 << 20
	maxAnswerBytes         = 256 << 10
)

func interactionConflict() *domain.Error {
	return domain.Fail(domain.Conflict, "The native interaction is not eligible for this response.", "Reload the original request and reconcile any prior response; do not replay a transmitted or uncertain answer.")
}
func interactionUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "Native question response delivery requires reconciliation.", "Retain the original response identity and inspect native state before another send; request closure alone does not confirm acceptance.")
}
func (s *interactionState) blocksInput() bool {
	for _, interaction := range s.arrivals {
		if interaction.status.Closure == InteractionOpen || interaction.status.Delivery != QuestionNotSent {
			return true
		}
	}
	return false
}
func requestKey(id NativeRequestID) string {
	if id.Kind == TextRequestID {
		return "s:" + id.Text
	}
	return "n:" + strconv.FormatInt(*id.Number, 10)
}

func cloneQuestions(request *QuestionRequest) *QuestionRequest {
	copy := *request
	if copy.AutoResolutionMS != nil {
		value := *copy.AutoResolutionMS
		copy.AutoResolutionMS = &value
	}
	copy.Questions = slices.Clone(request.Questions)
	for i := range copy.Questions {
		copy.Questions[i].Options = slices.Clone(copy.Questions[i].Options)
	}
	return &copy
}

func (c *Client) retainQuestionLocked(native nativewire.Event, turn domain.ID, item string, interaction *Interaction, eligible bool) error {
	s := &c.execution.interactions
	key := requestKey(interaction.NativeID)
	// The pinned server allocates monotonically increasing request IDs. Refuse
	// reuse even after closure: resolved notifications contain no arrival token
	// and could otherwise retire a replacement request with the same wire ID.
	if s.native[key] != "" || s.arrivals[interaction.ID] != nil {
		return incompatible()
	}
	if len(s.arrivals) >= maxTrackedInteractions {
		return domain.Fail(domain.ResourceExhausted, "Native interaction tracking reached its bound.", "Drain and persist native state before replacing the connection explicitly.")
	}
	raw, err := json.Marshal(interaction.Questions)
	if err != nil || len(raw) > maxQuestionBytes-s.bytes || s.open >= maxOpenInteractions {
		return domain.Fail(domain.ResourceExhausted, "Pending native questions reached their bound.", "Resolve or stop pending interactions before accepting more native work.")
	}
	if s.arrivals == nil {
		s.arrivals = map[domain.ID]*trackedInteraction{}
		s.native = map[string]domain.ID{}
		s.responses = map[domain.ID]bool{}
	}
	owned := &trackedInteraction{
		status: InteractionStatus{ID: interaction.ID, TurnID: turn, ItemID: item, Closure: InteractionOpen, Delivery: QuestionNotSent},
		// Retain only reply authority, never another copy of raw question data.
		native:    nativewire.Event{Kind: nativewire.ServerRequest, ID: slices.Clone(native.ID), Token: native.Token},
		questions: cloneQuestions(interaction.Questions), bytes: len(raw),
	}
	if !eligible {
		if _, err := c.wire.RetireRequest(owned.native); err != nil {
			return err
		}
		owned.status.Closure = InteractionTurnEnded
		owned.questions, owned.bytes = nil, 0
	} else {
		s.open++
		s.bytes += owned.bytes
	}
	s.arrivals[interaction.ID], s.native[key] = owned, interaction.ID
	return nil
}

func (c *Client) closeInteractionLocked(owned *trackedInteraction, closure InteractionClosure) error {
	if owned.status.Closure != InteractionOpen {
		return nil
	}
	retired, err := c.wire.RetireRequest(owned.native)
	if err != nil {
		return err
	}
	if !retired && owned.status.Delivery == QuestionNotSent {
		return incompatible()
	}
	owned.status.Closure = closure
	s := &c.execution.interactions
	s.bytes -= owned.bytes
	s.open--
	owned.questions, owned.bytes = nil, 0
	return nil
}

func (c *Client) endInteractionsLocked(turn domain.ID) error {
	for _, owned := range c.execution.interactions.arrivals {
		if owned.status.TurnID == turn {
			if err := c.closeInteractionLocked(owned, InteractionTurnEnded); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) observeInteractionClosedLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID  domain.ID       `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	id, err := decodeNativeRequestID(params.RequestID)
	if err != nil {
		return Event{}, err
	}
	s := &c.execution.interactions
	arrival := s.native[requestKey(id)]
	if arrival == "" {
		// An unsupported approval/tool request can also resolve. Its unknown
		// ownership remains private; it cannot close a supported question.
		return privateNative(native), nil
	}
	owned := s.arrivals[arrival]
	if err := c.closeInteractionLocked(owned, InteractionNativeClosed); err != nil {
		return Event{}, err
	}
	status := owned.status
	turn, known := c.execution.turns[status.TurnID]
	return Event{Kind: InteractionClosedEvent, ThreadID: c.thread, TurnID: status.TurnID, ItemID: status.ItemID, InteractionState: &status, Correlated: known, Late: turn.Turn.Status.terminal()}, nil
}

// QuestionAnswers is memory-only. The coordinator must protect secret answers
// and journal an immutable response claim before calling AnswerQuestions.
// Serialized normalized events and ownership journals must never include it.
type QuestionAnswers struct {
	Answers map[string][]string `json:"-"`
}
type nativeQuestionAnswer struct {
	Answers []string `json:"answers"`
}
type nativeQuestionResponse struct {
	Answers map[string]nativeQuestionAnswer `json:"answers"`
}

func validateQuestionAnswers(original *QuestionRequest, response QuestionAnswers) (nativeQuestionResponse, error) {
	invalid := func() (nativeQuestionResponse, error) {
		return nativeQuestionResponse{}, domain.Fail(domain.InvalidArgument, "The answer does not match the original native questions.", "Use each original question identity and its offered options or explicitly supported free text; do not add approval or policy fields.")
	}
	if original == nil || len(response.Answers) != len(original.Questions) {
		return invalid()
	}
	result := nativeQuestionResponse{Answers: map[string]nativeQuestionAnswer{}}
	for _, question := range original.Questions {
		if question.Secret {
			return nativeQuestionResponse{}, domain.Fail(domain.Unsupported, "Protected native question answers are not yet supported.", "Do not send the secret as ordinary input; use a validated protected answer workflow when available.")
		}
		answers, exists := response.Answers[question.ID]
		if !exists || answers == nil || len(answers) > 128 {
			return invalid()
		}
		seen := map[string]bool{}
		for _, answer := range answers {
			if domain.Text(answer, "native question answer", maxAnswerBytes, true) != nil || seen[answer] {
				return invalid()
			}
			if len(question.Options) != 0 && !question.Other && !slices.ContainsFunc(question.Options, func(option QuestionOption) bool { return option.Label == answer }) {
				return invalid()
			}
			seen[answer] = true
		}
		result.Answers[question.ID] = nativeQuestionAnswer{Answers: slices.Clone(answers)}
	}
	if err := questionResponseSize(result); err != nil {
		return nativeQuestionResponse{}, err
	}
	return result, nil
}

// ValidateQuestionResponseSize preflights this profile's complete native wire
// shape before durable owner acceptance. It validates size only; the caller
// must separately validate every answer against the original non-secret request.
func ValidateQuestionResponseSize(answers QuestionAnswers) error {
	response := nativeQuestionResponse{Answers: map[string]nativeQuestionAnswer{}}
	for id, values := range answers.Answers {
		response.Answers[id] = nativeQuestionAnswer{Answers: values}
	}
	return questionResponseSize(response)
}

func questionResponseSize(response nativeQuestionResponse) error {
	raw, err := json.Marshal(response)
	if err != nil || len(raw) > maxAnswerBytes {
		return domain.Fail(domain.ResourceExhausted, "The native question response exceeds its bound.", "Reduce the response without dropping any question identity.")
	}
	return nil
}

// AnswerQuestions sends at most once for a caller-journaled response identity.
// A successful return means only pipe transmission. The caller must retain
// pending acceptance until dedicated native evidence confirms the response.
func (c *Client) AnswerQuestions(ctx context.Context, responseID, interactionID, turnID domain.ID, answers QuestionAnswers) (InteractionStatus, error) {
	for _, id := range []domain.ID{responseID, interactionID, turnID} {
		if err := id.Validate(); err != nil {
			return InteractionStatus{}, err
		}
	}
	if err := c.acquireControl(ctx); err != nil {
		return InteractionStatus{}, err
	}
	defer func() { <-c.control }()
	if c.mode != ThreadProtocol || c.execution == nil {
		return InteractionStatus{}, unsupportedSettings()
	}
	if c.problem != nil {
		return InteractionStatus{}, c.problem
	}
	s := &c.execution.interactions
	owned := s.arrivals[interactionID]
	if owned == nil || owned.status.TurnID != turnID || c.execution.active != turnID || c.execution.paused || c.execution.interrupt != "" || owned.status.Closure != InteractionOpen || owned.status.Delivery != QuestionNotSent || s.responses[responseID] {
		return InteractionStatus{}, interactionConflict()
	}
	if len(s.responses) >= maxTrackedInteractions {
		return InteractionStatus{}, domain.Fail(domain.ResourceExhausted, "Native response tracking reached its bound.", "Reconcile pending responses before replacing the native connection.")
	}
	response, err := validateQuestionAnswers(owned.questions, answers)
	if err != nil {
		return owned.status, err
	}
	if ctx.Err() != nil {
		return owned.status, domain.SafeError(ctx.Err())
	}
	s.responses[responseID] = true
	owned.status.ResponseID = responseID
	err = c.wire.Reply(ctx, owned.native, response)
	if err == nil {
		owned.status.Delivery = QuestionTransmitted
	} else if domain.SafeError(err).Code == domain.RecoveryRequired {
		owned.status.Delivery = QuestionDeliveryUncertain
		c.execution.paused = true
		c.problem = interactionUncertain()
		err = c.problem
	}
	if c.logger != nil {
		c.logger.InfoContext(ctx, "Codex question response delivery observed", "owner_id", c.ownerID, "interaction_id", interactionID, "response_id", responseID, "turn_id", turnID, "delivery", owned.status.Delivery)
	}
	return owned.status, err
}

func (c *Client) InspectInteraction(ctx context.Context, interactionID domain.ID) (InteractionStatus, error) {
	if err := interactionID.Validate(); err != nil {
		return InteractionStatus{}, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return InteractionStatus{}, err
	}
	defer func() { <-c.control }()
	if c.execution == nil || c.execution.interactions.arrivals[interactionID] == nil {
		return InteractionStatus{}, interactionConflict()
	}
	return c.execution.interactions.arrivals[interactionID].status, nil
}
