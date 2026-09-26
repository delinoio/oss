package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxObservedInteractions = 1024

type RejectionPolicy string

// Only a separately verified effective native setting can establish this
// policy. An omitted policy grants no rejection-based terminal inference.
const (
	StopOnInteractionRejection     RejectionPolicy = "stop-on-rejection"
	ContinueOnInteractionRejection RejectionPolicy = "continue-on-rejection"
)

func validRejectionPolicy(value RejectionPolicy) bool {
	return value == StopOnInteractionRejection || value == ContinueOnInteractionRejection
}

type observedInteraction struct {
	value              NativeInteraction
	raw                []byte
	arrival            string
	closed             bool
	attempt            *interactionAttempt
	rejected           bool
	rejectionSources   []string
	rejectionReserved  bool
	pendingAbsent      bool
	alwaysAccepted     bool
	alwaysObservations []string
}

type interactionAttempt struct {
	receipt    InteractionReceipt
	body       []byte
	claim      SessionClaim
	sent       bool
	permission *PermissionDecision
	correction bool
}

type interactionHTTPAttempt struct {
	path   string
	digest string
}

// InteractionReceipt distinguishes the HTTP result from the original native
// reply event. Permission events echo only the decision, never correction
// feedback. HTTPAccepted covers the exact original body; NativeAccepted alone
// cannot prove feedback delivery. Neither fact completes its tool or permits
// repeating the answer.
type InteractionReceipt struct {
	RequestID         domain.ID
	InputRequestID    domain.ID
	InteractionID     string
	ArrivalID         string
	HTTPAccepted      bool
	NativeAccepted    bool
	FeedbackRequested bool
}

type InteractionResponse struct {
	Decision *PermissionDecision
	Answers  [][]string `json:"-"`
	Reject   bool
	Feedback *string `json:"-"`
}

type permissionResponse struct {
	Reply   PermissionDecision `json:"reply"`
	Message *string            `json:"message,omitempty"`
}

func (o *inputObserver) interaction(event NativeEvent) (*NativeInteraction, error) {
	value, err := decodeNativeInteraction(event.Kind, event.Properties)
	if err != nil || value.SessionID != o.input.receipt.SessionID || value.Tool == nil || o.interactions[value.ID] != nil || o.progress.SettledObserved {
		return nil, observerProblem()
	}
	if len(o.interactions) >= maxObservedInteractions {
		return nil, eventBound()
	}
	part := o.parts[o.calls[value.Tool.CallID]]
	message := o.messages[value.Tool.MessageID]
	if part == nil || message == nil || message.finalized || message.value.Assistant == nil || value.Tool.MessageID != o.progress.AssistantID || part.value.MessageID != value.Tool.MessageID || part.value.Tool.State != ToolPending && part.value.Tool.State != ToolRunning {
		return nil, observerProblem()
	}
	if value.Kind == QuestionInteraction && part.value.Tool.Name != "question" {
		return nil, observerProblem()
	}
	o.interactions[value.ID] = &observedInteraction{value: value, raw: canonicalNative(event.Properties), arrival: event.ID}
	copy, _ := decodeNativeInteraction(event.Kind, event.Properties)
	return &copy, nil
}

func (o *inputObserver) interactionReply(event NativeEvent) (*NativeInteractionReply, error) {
	value, err := decodeNativeInteractionReply(event.Kind, event.Properties)
	if err != nil || value.SessionID != o.input.receipt.SessionID {
		return nil, observerProblem()
	}
	interaction := o.interactions[value.RequestID]
	if interaction == nil || interaction.closed || interaction.value.Kind != value.Kind {
		return nil, observerProblem()
	}
	if interaction.attempt == nil {
		// Native permission policy closes other pending requests in this
		// original session. Preserve observations without inventing a direct
		// response claim or replaying native wildcard matching ourselves.
		if value.Kind != PermissionInteraction || value.Decision == nil || *value.Decision != PermissionReject && *value.Decision != PermissionAlways {
			return nil, observerProblem()
		}
		for id, prior := range o.interactions {
			if prior.value.Kind != PermissionInteraction || !prior.closed || prior.attempt == nil || !prior.attempt.receipt.NativeAccepted {
				continue
			}
			if value.Rejected && prior.rejected {
				interaction.rejectionSources = append(interaction.rejectionSources, id)
			}
			if *value.Decision == PermissionAlways && prior.alwaysAccepted && len(prior.value.Permission.Always) != 0 {
				// The native event establishes this original request's policy
				// closure. Retain prior direct always observations as context,
				// not a guessed exact wildcard-rule or direct response owner.
				interaction.alwaysObservations = append(interaction.alwaysObservations, id)
			}
		}
		if value.Rejected && len(interaction.rejectionSources) == 0 || *value.Decision == PermissionAlways && len(interaction.alwaysObservations) == 0 {
			return nil, observerProblem()
		}
		slices.Sort(interaction.rejectionSources)
		slices.Sort(interaction.alwaysObservations)
		interaction.closed, interaction.rejected = true, value.Rejected
		return &value, nil
	}
	if !interaction.attempt.sent {
		return nil, observerProblem()
	}
	var body []byte
	if value.Kind == PermissionInteraction {
		if value.Decision == nil || *value.Decision != PermissionOnce && *value.Decision != PermissionReject && *value.Decision != PermissionAlways {
			return nil, observerProblem()
		}
		if interaction.attempt.permission == nil || *interaction.attempt.permission != *value.Decision {
			return nil, observerProblem()
		}
		// The original claim retains the complete body's digest, including
		// feedback. Native permission events acknowledge only the decision.
		// Do not fabricate a feedback echo from this narrower observation.
		body = interaction.attempt.body
	} else {
		if value.Rejected {
			if interaction.attempt.claim.Kind != RejectQuestionMutation {
				return nil, observerProblem()
			}
		} else {
			if interaction.attempt.claim.Kind != ReplyQuestionMutation {
				return nil, observerProblem()
			}
			body, _ = json.Marshal(struct {
				Answers [][]string `json:"answers"`
			}{value.Answers})
		}
	}
	if !bytes.Equal(body, interaction.attempt.body) {
		return nil, observerProblem()
	}
	interaction.closed = true
	interaction.rejected = value.Rejected
	interaction.alwaysAccepted = value.Decision != nil && *value.Decision == PermissionAlways
	interaction.attempt.receipt.NativeAccepted = true
	if value.Kind == PermissionInteraction && value.Rejected {
		for _, pending := range o.interactions {
			if pending.value.Kind == PermissionInteraction && !pending.closed {
				pending.rejectionReserved = true
			}
		}
	}
	return &value, nil
}

func (o *inputObserver) interactionReceipt(id string) (InteractionReceipt, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	interaction := o.interactions[id]
	if interaction == nil || interaction.attempt == nil {
		return InteractionReceipt{}, sessionInvalid()
	}
	return interaction.attempt.receipt, nil
}

func (o *inputObserver) prepareInteraction(request domain.ID, id string, response InteractionResponse) (*interactionAttempt, string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.problem != nil {
		return nil, "", o.problem
	}
	interaction := o.interactions[id]
	if request.Validate() != nil || request == o.creation.request || request == o.input.receipt.RequestID || o.responseIDs[request] || interaction == nil {
		return nil, "", sessionInvalid()
	}
	if interaction.pendingAbsent {
		return nil, "", sessionUncertain()
	}
	if interaction.closed || interaction.attempt != nil || interaction.rejectionReserved {
		return nil, "", sessionConflict()
	}
	for _, prior := range o.interactions {
		if prior.attempt != nil && !prior.closed {
			return nil, "", sessionConflict()
		}
	}
	var body []byte
	var kind SessionMutation
	var path string
	if interaction.value.Kind == PermissionInteraction {
		if response.Decision == nil || *response.Decision != PermissionOnce && *response.Decision != PermissionReject && *response.Decision != PermissionAlways || response.Answers != nil || response.Reject {
			return nil, "", interactionProblem()
		}
		if *response.Decision == PermissionReject && !validRejectionPolicy(o.rejectionPolicy) {
			return nil, "", interactionProblem()
		}
		if response.Feedback != nil && (*response.Decision != PermissionReject || domain.Text(*response.Feedback, "correction feedback", 64<<10, false) != nil) {
			return nil, "", sessionInvalid()
		}
		body, _ = json.Marshal(permissionResponse{Reply: *response.Decision, Message: response.Feedback})
		kind, path = ReplyPermissionMutation, "/permission/"+id+"/reply"
	} else {
		if response.Decision != nil || response.Feedback != nil {
			return nil, "", sessionInvalid()
		}
		if response.Reject {
			if response.Answers != nil || !validRejectionPolicy(o.rejectionPolicy) {
				return nil, "", interactionProblem()
			}
			kind, path = RejectQuestionMutation, "/question/"+id+"/reject"
		} else {
			if !validQuestionAnswers(interaction.value.Questions, response.Answers) {
				return nil, "", sessionInvalid()
			}
			body, _ = json.Marshal(struct {
				Answers [][]string `json:"answers"`
			}{response.Answers})
			kind, path = ReplyQuestionMutation, "/question/"+id+"/reply"
		}
	}
	if len(body) > maxHTTPBody {
		return nil, "", sessionInvalid()
	}
	if len(body) > maxObservedBytes-o.bytes {
		return nil, "", eventBound()
	}
	tool := interaction.value.Tool
	attempt := &interactionAttempt{
		body:    body,
		receipt: InteractionReceipt{RequestID: request, InputRequestID: o.input.receipt.RequestID, InteractionID: id, ArrivalID: interaction.arrival},
		claim:   SessionClaim{RequestID: request, Kind: kind, SessionID: interaction.value.SessionID, MessageID: tool.MessageID, PartID: o.calls[tool.CallID], BodyDigest: mutationDigest(body), InputRequestID: o.input.receipt.RequestID, InteractionID: id, ArrivalID: interaction.arrival, CallID: tool.CallID},
	}
	if response.Decision != nil {
		decision := *response.Decision
		attempt.permission = &decision
		attempt.correction = response.Feedback != nil && *response.Feedback != ""
		attempt.receipt.FeedbackRequested = response.Feedback != nil
	}
	// Consume this scope before the durable callback: even a failed callback
	// can have synchronized its original claim before losing the response.
	interaction.attempt = attempt
	o.responseIDs[request] = true
	o.bytes += len(body)
	return attempt, path, nil
}

func (o *inputObserver) rejectedTool(interaction *observedInteraction) bool {
	if !interaction.closed || !interaction.rejected {
		return false
	}
	part := o.parts[o.calls[interaction.value.Tool.CallID]]
	return part != nil && part.value.Tool.State == ToolError
}

func (o *inputObserver) rejectionStopsMessage(id string) bool {
	if o.rejectionPolicy != StopOnInteractionRejection {
		return false
	}
	for _, interaction := range o.interactions {
		if interaction.value.Tool.MessageID != id || !o.rejectedTool(interaction) {
			continue
		}
		// Direct nonempty correction raises native CorrectedError and keeps
		// the loop active. Cascaded rejections always raise RejectedError,
		// even when their source included feedback, and can stop this step.
		if interaction.attempt == nil || !interaction.attempt.correction {
			return true
		}
	}
	return false
}

func validQuestionAnswers(questions []NativeQuestion, answers [][]string) bool {
	if answers == nil || len(answers) != len(questions) {
		return false
	}
	for i, answer := range answers {
		q := questions[i]
		if answer == nil || len(answer) > 256 || (q.Multiple == nil || !*q.Multiple) && len(answer) > 1 {
			return false
		}
		seen := map[string]bool{}
		for _, text := range answer {
			if domain.Text(text, "answer", 64<<10, false) != nil || seen[text] {
				return false
			}
			seen[text] = true
			matches := 0
			for _, option := range q.Options {
				if option.Label == text {
					matches++
				}
			}
			if matches > 1 || matches == 0 && q.Custom != nil && !*q.Custom {
				return false
			}
		}
	}
	return true
}

func (s *sessionAPI) replyInteraction(ctx context.Context, observer *inputObserver, request domain.ID, id string, response InteractionResponse) (InteractionReceipt, error) {
	if err := s.enter(ctx); err != nil {
		return InteractionReceipt{}, err
	}
	defer s.leave()
	if observer == nil || observer != s.observer || s.claim == nil || s.events == nil || s.input == nil {
		return InteractionReceipt{}, sessionInvalid()
	}
	if s.problem != nil {
		return InteractionReceipt{}, s.problem
	}
	if problem := s.events.status(); problem != nil {
		return InteractionReceipt{}, problem
	}
	if _, err := s.readSession(ctx); err != nil {
		return InteractionReceipt{}, err
	}
	if _, err := s.pendingInteraction(ctx, observer, id); err != nil {
		return InteractionReceipt{}, err
	}
	attempt, path, err := observer.prepareInteraction(request, id, response)
	if err != nil {
		return InteractionReceipt{}, err
	}
	current := func() InteractionReceipt { receipt, _ := observer.interactionReceipt(id); return receipt }
	bounded, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(observer.ctx, cancel)
	defer stop()
	if err := s.claim(bounded, attempt.claim); err != nil {
		s.diagnostic(ctx, attempt.claim.Kind, err)
		return current(), sessionUncertain()
	}
	observer.mu.Lock()
	problem := observer.problem
	if problem == nil && bounded.Err() == nil {
		attempt.sent = true
	}
	sent := attempt.sent
	body := slices.Clone(attempt.body)
	observer.mu.Unlock()
	if !sent {
		return current(), sessionUncertain()
	}
	s.replyAttempt = &interactionHTTPAttempt{path: path, digest: attempt.claim.BodyDigest}
	defer func() { s.replyAttempt = nil }()
	raw, _, err := s.request(bounded, http.MethodPost, path, body, http.StatusOK)
	if err != nil || string(bytes.TrimSpace(raw)) != "true" {
		if err == nil {
			err = sessionProblem()
		}
		s.diagnostic(ctx, attempt.claim.Kind, err)
		return current(), sessionUncertain()
	}
	observer.mu.Lock()
	attempt.receipt.HTTPAccepted = true
	observer.mu.Unlock()
	return current(), nil
}
