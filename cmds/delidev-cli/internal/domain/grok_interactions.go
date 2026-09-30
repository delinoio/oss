package domain

import "encoding/json"

type GrokFileDecision string
type GrokPlanDecision string
type GrokQuestionOutcome string

const (
	GrokAllowOnce         GrokFileDecision    = "allow-once"
	GrokAllowSession      GrokFileDecision    = "allow-edits-session"
	GrokRejectOnce        GrokFileDecision    = "reject-once"
	GrokPlanApproved      GrokPlanDecision    = "approved"
	GrokPlanCancelled     GrokPlanDecision    = "cancelled"
	GrokPlanAbandoned     GrokPlanDecision    = "abandoned"
	GrokQuestionAccepted  GrokQuestionOutcome = "accepted"
	GrokQuestionCancelled GrokQuestionOutcome = "cancelled"
	GrokQuestionSkipped   GrokQuestionOutcome = "skip_interview"
)

type GrokPlanProposal struct {
	Origin        GrokPlanOrigin `json:"origin"`
	WriteToolID   string         `json:"write_tool_id"`
	ContentDigest string         `json:"content_digest"`
}
type GrokInteractionRequest struct {
	Version        string            `json:"version"`
	ObservationID  ID                `json:"observation_id"`
	Event          GrokToolEvent     `json:"event"`
	RequestDigest  string            `json:"request_digest"`
	ProposalDigest string            `json:"proposal_digest"`
	Plan           *GrokPlanProposal `json:"plan,omitempty"`
}

func (r GrokInteractionRequest) Validate(kind InteractionType, id InteractionRequestID, tool string) error {
	if r.Version != GrokProtocolVersion || r.ObservationID.Validate() != nil || r.Event.ArrivalID.Validate() != nil || r.Event.RequestID == nil || r.Event.ProposalJSON == "" {
		return invalidInteraction()
	}
	original, e1 := id.Key()
	retained, e2 := r.Event.RequestID.Key()
	if e1 != nil || e2 != nil || original != retained {
		return invalidInteraction()
	}
	for _, digest := range []string{r.RequestDigest, r.ProposalDigest} {
		if !validSHA256(digest) {
			return invalidInteraction()
		}
	}
	p := r.Event.Payload
	switch r.Event.Method {
	case GrokFilePermissionMethod:
		if kind != NativeApprovalInteraction || p.Tool == nil || p.Tool.ID == nil || *p.Tool.ID != tool || r.Plan != nil {
			return invalidInteraction()
		}
	case GrokQuestionMethod:
		if kind != UserQuestionInteraction || p.ToolID == nil || *p.ToolID != tool || p.Questions == nil || r.Plan != nil {
			return invalidInteraction()
		}
	case GrokPlanMethod:
		if kind != NativeApprovalInteraction || p.ToolID == nil || *p.ToolID != tool || p.PlanContent == nil || r.Plan == nil || r.Plan.Origin.Revision == 0 || r.Plan.Origin.Revision > 128 || Text(r.Plan.WriteToolID, "original plan Write", 256, true) != nil || !validSHA256(r.Plan.ContentDigest) {
			return invalidInteraction()
		}
	default:
		return invalidInteraction()
	}
	return nil
}
func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}

type GrokApprovalResponse struct {
	Decision GrokFileDecision `json:"decision,omitempty"`
	Outcome  GrokPlanDecision `json:"outcome,omitempty"`
}

func (r GrokApprovalResponse) Validate(original *GrokInteractionRequest) error {
	if original == nil {
		return invalidApprovalResponse()
	}
	switch original.Event.Method {
	case GrokFilePermissionMethod:
		if r.Outcome != "" || r.Decision != GrokAllowOnce && r.Decision != GrokAllowSession && r.Decision != GrokRejectOnce {
			return invalidApprovalResponse()
		}
	case GrokPlanMethod:
		if r.Decision != "" || r.Outcome != GrokPlanApproved && r.Outcome != GrokPlanCancelled && r.Outcome != GrokPlanAbandoned {
			return invalidApprovalResponse()
		}
	default:
		return invalidApprovalResponse()
	}
	return nil
}

type GrokQuestionAnnotation struct {
	Notes string `json:"notes"`
}
type GrokQuestionResponse struct {
	Outcome        GrokQuestionOutcome               `json:"outcome"`
	Answers        map[string]string                 `json:"answers,omitempty"`
	Annotations    map[string]GrokQuestionAnnotation `json:"annotations,omitempty"`
	PartialAnswers map[string]string                 `json:"partial_answers,omitempty"`
}

func (r GrokQuestionResponse) Validate(original *GrokInteractionRequest) error {
	if original == nil || original.Event.Method != GrokQuestionMethod || original.Event.Payload.Questions == nil {
		return invalidQuestionResponse()
	}
	keys := map[string]bool{}
	for _, q := range *original.Event.Payload.Questions {
		keys[q.Text] = true
	}
	validate := func(answers map[string]string) bool {
		for key, value := range answers {
			if !keys[key] || Text(value, "original Grok answer", 64<<10, true) != nil {
				return false
			}
		}
		return true
	}
	switch r.Outcome {
	case GrokQuestionAccepted:
		if len(r.Answers) != len(keys) || !validate(r.Answers) || r.PartialAnswers != nil {
			return invalidQuestionResponse()
		}
		for key, value := range r.Annotations {
			if !keys[key] || Text(value.Notes, "original Grok notes", 64<<10, false) != nil {
				return invalidQuestionResponse()
			}
		}
	case GrokQuestionCancelled:
		if r.Answers != nil || r.Annotations != nil || r.PartialAnswers != nil {
			return invalidQuestionResponse()
		}
	case GrokQuestionSkipped:
		if r.Answers != nil || r.Annotations != nil || !validate(r.PartialAnswers) {
			return invalidQuestionResponse()
		}
	default:
		return invalidQuestionResponse()
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > MaxQuestionResponseBytes {
		return invalidQuestionResponse()
	}
	return nil
}

// Null answer/annotation values cannot silently become empty native strings.
func (r *GrokQuestionResponse) UnmarshalJSON(raw []byte) error {
	type plain GrokQuestionResponse
	var value plain
	if Decode(raw, &value) != nil {
		return invalidQuestionResponse()
	}
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil {
		return invalidQuestionResponse()
	}
	for _, key := range []string{"answers", "annotations", "partial_answers"} {
		if v, ok := fields[key]; ok && string(v) == "null" {
			return invalidQuestionResponse()
		}
	}
	var wire struct {
		Outcome     *GrokQuestionOutcome `json:"outcome"`
		Answers     map[string]*string   `json:"answers,omitempty"`
		Annotations map[string]*struct {
			Notes *string `json:"notes"`
		} `json:"annotations,omitempty"`
		Partial map[string]*string `json:"partial_answers,omitempty"`
	}
	if Decode(raw, &wire) != nil || wire.Outcome == nil {
		return invalidQuestionResponse()
	}
	for _, answers := range []map[string]*string{wire.Answers, wire.Partial} {
		for _, answer := range answers {
			if answer == nil {
				return invalidQuestionResponse()
			}
		}
	}
	for _, annotation := range wire.Annotations {
		if annotation == nil || annotation.Notes == nil {
			return invalidQuestionResponse()
		}
	}
	*r = GrokQuestionResponse(value)
	return nil
}

func (r *GrokApprovalResponse) UnmarshalJSON(raw []byte) error {
	type plain GrokApprovalResponse
	var value plain
	var fields map[string]json.RawMessage
	if Decode(raw, &value) != nil || Decode(raw, &fields) != nil || len(fields) != 1 {
		return invalidApprovalResponse()
	}
	for key, v := range fields {
		if key != "decision" && key != "outcome" || string(v) == "null" {
			return invalidApprovalResponse()
		}
	}
	*r = GrokApprovalResponse(value)
	return nil
}
