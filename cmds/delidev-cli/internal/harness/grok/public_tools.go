package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// publicToolEvent snapshots the original native envelope after its private
// observer accepts it and before any caller can mutate a publication callback.
func publicToolEvent(event nativewire.Event, fact mixedToolFact) (*domain.GrokToolEvent, error) {
	result := domain.GrokToolEvent{Method: domain.GrokToolMethod(event.Method), ArrivalID: event.Token}
	if domain.Decode(event.Params, &result.Payload) != nil {
		return nil, incompatible()
	}
	if event.Kind == nativewire.ServerRequest {
		key, err := fileToolRequestKey(event.ID)
		if err != nil {
			return nil, err
		}
		id := domain.InteractionRequestID{Kind: domain.InteractionTextID, Text: key[2:]}
		if key[:2] == "n:" {
			id = domain.InteractionRequestID{Kind: domain.InteractionDecimalID, Decimal: key[2:]}
		}
		result.RequestID = &id
	} else if event.Kind != nativewire.Notification || event.Token != "" {
		return nil, incompatible()
	}
	if fact.File != nil {
		result.InheritedPermission = fact.File.InheritedPermission
		if o := fact.File.PlanFile; o != nil {
			result.PlanOrigin = &domain.GrokPlanOrigin{EntryToolID: o.EntryToolID, EntryEventID: o.EntryEventID, Revision: o.Revision}
		}
	}
	return &result, nil
}

// nativeToolPayload reconstructs only inert typed evidence for the existing
// native validators. Public exact counters are never parsed through floats.
func nativeToolPayload(payload domain.GrokToolPayload) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, incompatible()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil, incompatible()
	}
	counts := map[string]bool{"totalTokens": true, "agentTimestampMs": true, "streamStartMs": true, "turnStartMs": true, "tool_index": true, "total_lines": true, "offset": true, "old_line": true, "new_line": true}
	var visit func(any) error
	visit = func(value any) error {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				if counts[key] && child != nil {
					text, ok := child.(string)
					if !ok {
						return incompatible()
					}
					if _, err := strconv.ParseUint(text, 10, 64); err != nil {
						return incompatible()
					}
					node[key] = json.Number(text)
				} else if err := visit(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range node {
				if err := visit(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

// ToolJournal is a pure observation reducer. It has no API connection, native
// sender, filesystem reader or restoration capability. Server publication runs
// the same original validators independently against its retained typed prefix.
type ToolJournal struct {
	observer *mixedTools
	session  domain.ID
	prompt   string
	bytes    int
	events   int
	requests map[domain.ID]mixedToolFact
	last     mixedToolFact
}

func NewToolJournal(session domain.ID, prompt string, mode domain.GrokMode, planPathValue string) (*ToolJournal, error) {
	observer, err := newMixedTools(session, prompt)
	if err != nil || !mode.Valid() {
		return nil, incompatible()
	}
	observer.plans = &planObserver{session: session, prompt: prompt, path: planPathValue, mode: NativeMode(mode), tools: map[string]planToolState{}, arrivals: map[domain.ID]bool{}, requests: map[string]bool{}}
	observer.files.plans = observer.plans
	observer.questions.mode = NativeMode(mode)
	return &ToolJournal{observer: observer, session: session, prompt: prompt, requests: map[domain.ID]mixedToolFact{}}, nil
}
func (j *ToolJournal) Observe(value domain.GrokToolEvent) error {
	if j == nil || value.Payload.Session != j.session || j.events >= 4096 {
		return incompatible()
	}
	raw, err := nativeToolPayload(value.Payload)
	if err != nil || len(raw) > nativewire.MaxFrame || j.bytes+len(raw) > 4<<20 {
		return incompatible()
	}
	event := nativewire.Event{Kind: nativewire.Notification, Method: string(value.Method), Params: raw}
	if value.RequestID != nil {
		if value.ArrivalID.Validate() != nil {
			return incompatible()
		}
		key, err := value.RequestID.Key()
		if err != nil {
			return err
		}
		event.Kind, event.Token = nativewire.ServerRequest, value.ArrivalID
		if key[:2] == "n:" {
			event.ID = json.RawMessage(key[2:])
		} else {
			event.ID, _ = json.Marshal(key[2:])
		}
	} else if value.ArrivalID != "" {
		return incompatible()
	}
	// The original native plan locator is inert comparison data. It is selected
	// once by the independently observed entry result, never used for file I/O.
	if u := value.Payload.Update; u != nil && u.Output != nil && u.Output.Entered != nil && j.observer.plans.path == "" {
		if !planPath(u.Output.Entered.Path) {
			return incompatible()
		}
		j.observer.plans.path = u.Output.Entered.Path
	}
	fact, err := j.observer.observe(event)
	if err != nil {
		return err
	}
	inherited := domain.ID("")
	var origin *domain.GrokPlanOrigin
	if fact.File != nil {
		inherited = fact.File.InheritedPermission
		if p := fact.File.PlanFile; p != nil {
			origin = &domain.GrokPlanOrigin{EntryToolID: p.EntryToolID, EntryEventID: p.EntryEventID, Revision: p.Revision}
		}
	}
	if inherited != value.InheritedPermission || (origin == nil) != (value.PlanOrigin == nil) || origin != nil && *origin != *value.PlanOrigin {
		return incompatible()
	}
	if event.Kind == nativewire.ServerRequest {
		j.requests[event.Token] = fact
	}
	j.last = fact
	j.bytes += len(raw)
	j.events++
	return nil
}

func (j *ToolJournal) ObservePlanDecision(tool string, outcome PlanOutcome) error {
	return j.observer.plans.bindResponse(tool, outcome)
}
func (j *ToolJournal) ObserveEditPolicy(arrival domain.ID) error {
	return j.observer.files.observeEditPolicy(arrival)
}
func (j *ToolJournal) Mode() domain.GrokMode { return domain.GrokMode(j.observer.plans.mode) }
func (j *ToolJournal) Settled() bool {
	if j == nil || !j.observer.plans.settled() {
		return false
	}
	for _, t := range j.observer.files.tools {
		if t.phase != fileToolCompleted && t.phase != fileToolFailed {
			return false
		}
	}
	for _, t := range j.observer.questions.tools {
		if t.phase != fileToolCompleted {
			return false
		}
	}
	return true
}

// ObserveWithReplies joins independent durable delivery before an original
// native resolution/result. Automatic permission cycles never acquire replies.
func (j *ToolJournal) ObserveWithReplies(value domain.GrokToolEvent, sequence uint64, replies map[domain.ID]domain.ExecutionInteraction) (string, error) {
	if err := j.Observe(value); err != nil {
		return "", err
	}
	fact := j.last
	if fact.File != nil && value.InheritedPermission != "" {
		return "", nil
	}
	var id string
	var arrival domain.ID
	resolved, completed := false, false
	if f := fact.File; f != nil {
		if f.Interaction != nil && f.Interaction.Kind == fileInteractionResolved {
			id = f.Interaction.ID
			resolved = true
		}
		if f.Observation != nil && (f.Observation.Phase == fileToolCompleted || f.Observation.Phase == fileToolFailed) {
			id = f.Observation.ID
			completed = true
		}
		arrival = j.observer.files.tools[id].permission
	} else if q := fact.Question; q != nil {
		if q.Interaction != nil && q.Interaction.Stage == questionAnswerResolved {
			id = q.Interaction.ID
			resolved = true
		}
		if q.Observation != nil && q.Observation.Phase == fileToolCompleted {
			id = q.Observation.ID
			completed = true
		}
		arrival = j.observer.questions.tools[id].arrival
	} else if p := fact.Plan; p != nil {
		if p.Interaction != nil && p.Interaction.Stage == planApprovalResolved {
			id = p.Interaction.ID
			resolved = true
		}
		if p.Observation != nil && p.Observation.Name == exitPlanTool && p.Observation.Phase == fileToolCompleted {
			id = p.Observation.ID
			completed = true
		}
		arrival = j.observer.plans.tools[id].arrival
	}
	if id == "" || arrival == "" {
		return "", nil
	}
	original, ok := replies[arrival]
	if !ok || original.Grok == nil || original.NativeItemID != id || original.Grok.Event.ArrivalID != arrival || original.Grok.Validate(original.Type, original.NativeRequestID, id) != nil {
		return "", incompatible()
	}
	if original.Type == domain.UserQuestionInteraction {
		r := original.Response
		if r == nil || r.Claim == nil || r.Delivery == nil || r.Delivery.State != domain.QuestionTransmitted || r.Delivery.Sequence >= sequence || r.Input.ValidateInteraction(original) != nil || r.State != domain.QuestionResponseTransmitted && r.State != domain.QuestionResponseAccepted {
			return "", incompatible()
		}
	} else {
		r := original.ApprovalResponse
		if r == nil || r.Claim == nil || r.Delivery == nil || r.Delivery.State != domain.ApprovalTransmitted || r.Delivery.Sequence >= sequence || r.Input.ValidateInteraction(original) != nil || r.State != domain.ApprovalResponseTransmitted && r.State != domain.ApprovalResponseAccepted {
			return "", incompatible()
		}
		if p := fact.Plan; p != nil && resolved {
			if err := j.ObservePlanDecision(id, PlanOutcome(r.Input.Grok.Outcome)); err != nil {
				return "", err
			}
		}
		if f := fact.File; f != nil && completed {
			decision := r.Input.Grok.Decision
			if f.Observation.Phase == fileToolFailed && decision != domain.GrokRejectOnce || f.Observation.Phase == fileToolCompleted && decision != domain.GrokAllowOnce && decision != domain.GrokAllowSession {
				return "", incompatible()
			}
			if decision == domain.GrokAllowSession {
				if err := j.ObserveEditPolicy(arrival); err != nil {
					return "", err
				}
			}
		}
	}
	if completed {
		return id, nil
	}
	return "", nil
}

// ValidatePublicQuestion uses the same bounded native encoder before queue
// acceptance and again before the original native send.
func ValidatePublicQuestion(original *domain.GrokInteractionRequest, response domain.GrokQuestionResponse) error {
	_, err := PublicQuestionDigest(original, response)
	return err
}
func PublicQuestionDigest(original *domain.GrokInteractionRequest, response domain.GrokQuestionResponse) (string, error) {
	if original == nil || response.Validate(original) != nil {
		return "", incompatible()
	}
	raw, err := nativeToolPayload(original.Event.Payload)
	if err != nil {
		return "", err
	}
	mode := NativeDefaultMode
	if original.Event.Payload.Mode != nil {
		mode = NativeMode(*original.Event.Payload.Mode)
	}
	request, err := parseQuestionRequestForMode(raw, original.Event.Payload.Session, mode)
	if err != nil {
		return "", err
	}
	annotations := map[string]QuestionAnnotation{}
	if response.Annotations == nil {
		annotations = nil
	} else {
		for key, value := range response.Annotations {
			annotations[key] = QuestionAnnotation{Notes: value.Notes}
		}
	}
	body, err := (QuestionAnswer{Outcome: QuestionOutcome(response.Outcome), Answers: response.Answers, Annotations: annotations, PartialAnswers: response.PartialAnswers}).body(request.Questions)
	return fileDigest(body), err
}
func PublicRequestDigest(id domain.InteractionRequestID) (string, error) {
	key, err := id.Key()
	if err != nil {
		return "", err
	}
	return fileDigest([]byte(key)), nil
}

func (j *ToolJournal) ValidateRequest(request *domain.GrokInteractionRequest, tool string) error {
	if j == nil || request == nil {
		return incompatible()
	}
	fact, ok := j.requests[request.Event.ArrivalID]
	if !ok {
		return incompatible()
	}
	if p := fact.Plan; p != nil {
		if p.Artifact == nil || p.Request == nil || p.Request.ID != tool || request.Plan == nil {
			return incompatible()
		}
		a := p.Artifact
		expected := domain.GrokPlanProposal{Origin: domain.GrokPlanOrigin{EntryToolID: a.origin.EntryToolID, EntryEventID: a.origin.EntryEventID, Revision: a.origin.Revision}, WriteToolID: a.writeToolID, ContentDigest: fmt.Sprintf("%x", a.contentDigest)}
		if *request.Plan != expected {
			return incompatible()
		}
	} else if request.Plan != nil {
		return incompatible()
	}
	return nil
}
