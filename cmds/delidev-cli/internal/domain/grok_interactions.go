package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

type GrokInteractionKind string
type GrokDecision string
type GrokQuestionOutcome string

const (
	GrokFilePermission      GrokInteractionKind = "file-permission"
	GrokQuestionInteraction GrokInteractionKind = "question"
	GrokPlanApproval        GrokInteractionKind = "plan-approval"
	GrokAllowOnce           GrokDecision        = "allow-once"
	GrokAllowEditsSession   GrokDecision        = "allow-edits-session"
	GrokRejectOnce          GrokDecision        = "reject-once"
	GrokPlanApproved        GrokDecision        = "approved"
	GrokPlanCancelled       GrokDecision        = "cancelled"
	GrokPlanAbandoned       GrokDecision        = "abandoned"
	GrokQuestionAccepted    GrokQuestionOutcome = "accepted"
	GrokQuestionCancelled   GrokQuestionOutcome = "cancelled"
	GrokQuestionSkipped     GrokQuestionOutcome = "skip_interview"
)

type GrokQuestion struct {
	Question    string           `json:"question"`
	Options     []QuestionOption `json:"options"`
	MultiSelect *bool            `json:"multiSelect"`
}

type GrokPlanRevision struct {
	EntryToolID   string `json:"entry_tool_id"`
	EntryEventID  string `json:"entry_event_id"`
	Revision      uint64 `json:"revision"`
	WriteToolID   string `json:"write_tool_id"`
	Content       string `json:"content"`
	ContentDigest string `json:"content_digest"`
}

func GrokDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// Grok hashes Plan content as its original JSON string, including escaping.
func GrokValueDigest(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return GrokDigest(raw)
}

func validGrokDigest(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}

func (p GrokPlanRevision) Validate(thread string) error {
	if p.Revision == 0 || p.Revision > 128 || Text(p.EntryToolID, "native Plan entry", 256, true) != nil || Text(p.WriteToolID, "native Plan write", 256, true) != nil || p.EntryToolID == p.WriteToolID || Text(p.Content, "native Plan content", 256<<10, true) != nil || p.ContentDigest != GrokValueDigest(p.Content) {
		return invalidInteraction()
	}
	_, err := GrokEventIndex(p.EntryEventID, thread)
	return err
}

// Arrival and proposal digests identify the original native request. They are
// observations, never a response grant or filesystem authority.
type GrokInteractionRequest struct {
	Version        string              `json:"version"`
	Kind           GrokInteractionKind `json:"kind"`
	ArrivalID      ID                  `json:"arrival_id"`
	RequestDigest  string              `json:"request_digest"`
	ProposalDigest string              `json:"proposal_digest"`
	Mode           GrokMode            `json:"mode"`
	ToolName       GrokToolName        `json:"tool_name"`
	Path           string              `json:"path,omitempty"`
	Content        string              `json:"content,omitempty"`
	Questions      []GrokQuestion      `json:"questions,omitempty"`
	Plan           *GrokPlanRevision   `json:"plan,omitempty"`
}

func (r GrokInteractionRequest) Validate(kind InteractionType, request InteractionRequestID, item string) error {
	if r.Version != GrokProtocolVersion || r.ArrivalID.Validate() != nil || request.Kind != InteractionTextID || request.Text != string(r.ArrivalID) || request.Number != nil || Text(item, "native tool identity", 256, true) != nil || !validGrokDigest(r.RequestDigest) || !validGrokDigest(r.ProposalDigest) || !r.Mode.Valid() {
		return invalidInteraction()
	}
	switch r.Kind {
	case GrokFilePermission:
		if kind != NativeApprovalInteraction || r.Mode != GrokDefaultMode || r.ToolName != "write" || Text(r.Path, "native file path", 8192, true) != nil || Text(r.Content, "native file content", 256<<10, false) != nil || r.Plan != nil || r.Questions != nil {
			return invalidInteraction()
		}
	case GrokQuestionInteraction:
		if kind != UserQuestionInteraction || r.ToolName != "ask_user_question" || r.Path != "" || r.Content != "" || r.Plan != nil || len(r.Questions) == 0 || len(r.Questions) > 32 {
			return invalidInteraction()
		}
		seen := map[string]bool{}
		for _, q := range r.Questions {
			if Text(q.Question, "native question", 16<<10, true) != nil || seen[q.Question] || len(q.Options) == 0 || len(q.Options) > 64 {
				return invalidInteraction()
			}
			seen[q.Question] = true
			labels := map[string]bool{}
			for _, o := range q.Options {
				if Text(o.Label, "native option", 4096, true) != nil || Text(o.Description, "native option description", 16<<10, false) != nil || labels[o.Label] {
					return invalidInteraction()
				}
				labels[o.Label] = true
			}
		}
	case GrokPlanApproval:
		if kind != NativeApprovalInteraction || r.Mode != GrokPlanMode || r.ToolName != "exit_plan_mode" || r.Path != "" || r.Content != "" || r.Questions != nil || r.Plan == nil {
			return invalidInteraction()
		}
	default:
		return invalidInteraction()
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 512<<10 {
		return invalidInteraction()
	}
	return nil
}

type GrokApprovalResponse struct {
	Decision GrokDecision `json:"decision"`
}

func (r GrokApprovalResponse) Validate(original GrokInteractionRequest) error {
	switch original.Kind {
	case GrokFilePermission:
		if slices.Contains([]GrokDecision{GrokAllowOnce, GrokAllowEditsSession, GrokRejectOnce}, r.Decision) {
			return nil
		}
	case GrokPlanApproval:
		if slices.Contains([]GrokDecision{GrokPlanApproved, GrokPlanCancelled, GrokPlanAbandoned}, r.Decision) {
			return nil
		}
	}
	return invalidApprovalResponse()
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

func (r GrokQuestionResponse) Validate(original GrokInteractionRequest) error {
	if original.Kind != GrokQuestionInteraction {
		return invalidQuestionResponse()
	}
	keys := map[string]bool{}
	for _, q := range original.Questions {
		keys[q.Question] = true
	}
	valid := func(answers map[string]string) bool {
		for key, value := range answers {
			if !keys[key] || Text(value, "native answer", 64<<10, true) != nil {
				return false
			}
		}
		return true
	}
	switch r.Outcome {
	case GrokQuestionAccepted:
		if len(r.Answers) != len(keys) || !valid(r.Answers) || r.PartialAnswers != nil {
			return invalidQuestionResponse()
		}
		for key, v := range r.Annotations {
			if !keys[key] || Text(v.Notes, "native answer annotation", 64<<10, false) != nil {
				return invalidQuestionResponse()
			}
		}
	case GrokQuestionCancelled:
		if r.Answers != nil || r.Annotations != nil || r.PartialAnswers != nil {
			return invalidQuestionResponse()
		}
	case GrokQuestionSkipped:
		if r.Answers != nil || r.Annotations != nil || !valid(r.PartialAnswers) {
			return invalidQuestionResponse()
		}
	default:
		return invalidQuestionResponse()
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > 256<<10 {
		return invalidQuestionResponse()
	}
	return nil
}
