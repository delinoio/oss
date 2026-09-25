package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type ArtifactKind string
type ArtifactDeltaKind string
type PlanStepStatus string

const (
	PlanArtifact      ArtifactKind = "plan"
	ReasoningArtifact ArtifactKind = "reasoning"

	PlanTextDelta         ArtifactDeltaKind = "plan-text"
	ReasoningSummaryDelta ArtifactDeltaKind = "reasoning-summary"
	ReasoningContentDelta ArtifactDeltaKind = "reasoning-content"
	ReasoningSummaryAdded ArtifactDeltaKind = "reasoning-summary-added"

	PlanPending   PlanStepStatus = "pending"
	PlanRunning   PlanStepStatus = "inProgress"
	PlanCompleted PlanStepStatus = "completed"
)

const MaxArtifactParts = 1024

// The pinned protocol explicitly makes completed plan text authoritative even
// when it differs from concatenated deltas. Keep the two observations separate.
// Reasoning contains only text the native protocol actually supplied; absent
// or encrypted provider reasoning is never reconstructed or substituted.
type Artifact struct {
	ID      string
	Kind    ArtifactKind
	Text    string
	Summary []string
	Content []string
}

type ArtifactDelta struct {
	Kind  ArtifactDeltaKind
	Index *int64
	Text  string
}

type PlanStep struct {
	Step   string
	Status PlanStepStatus
}
type PlanUpdate struct {
	Explanation *string
	Steps       []PlanStep
}

func (c *Client) observeArtifactUpdateLocked(native nativewire.Event) (Event, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &fields) != nil {
		return Event{}, incompatible()
	}
	var params struct {
		ThreadID     domain.ID         `json:"threadId"`
		TurnID       domain.ID         `json:"turnId"`
		ItemID       *string           `json:"itemId"`
		Delta        *string           `json:"delta"`
		SummaryIndex *int64            `json:"summaryIndex"`
		ContentIndex *int64            `json:"contentIndex"`
		Explanation  *string           `json:"explanation"`
		Plan         []json.RawMessage `json:"plan"`
		Diff         *string           `json:"diff"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || params.TurnID.Validate() != nil {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	turn, known := c.execution.turns[params.TurnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	result := Event{ThreadID: c.thread, TurnID: params.TurnID, Correlated: known, Late: turn.Turn.Status.terminal()}
	allowed := []string{"threadId", "turnId"}
	switch native.Method {
	case "turn/plan/updated":
		allowed = append(allowed, "explanation", "plan")
		if params.Plan == nil || len(params.Plan) > MaxArtifactParts || (params.Explanation != nil && domain.Text(*params.Explanation, "native plan explanation", nativewire.MaxFrame, false) != nil) {
			return Event{}, incompatible()
		}
		plan := &PlanUpdate{Explanation: params.Explanation, Steps: []PlanStep{}}
		for _, raw := range params.Plan {
			var step struct {
				Text   *string        `json:"step"`
				Status PlanStepStatus `json:"status"`
			}
			if domain.Decode(raw, &step) != nil || step.Text == nil || domain.Text(*step.Text, "native plan step", nativewire.MaxFrame, false) != nil || !slices.Contains([]PlanStepStatus{PlanPending, PlanRunning, PlanCompleted}, step.Status) {
				return Event{}, incompatible()
			}
			plan.Steps = append(plan.Steps, PlanStep{Step: *step.Text, Status: step.Status})
		}
		result.Kind, result.Plan = TurnPlanEvent, plan
	case "turn/diff/updated":
		allowed = append(allowed, "diff")
		if params.Diff == nil || domain.Text(*params.Diff, "native turn diff", nativewire.MaxFrame, false) != nil {
			return Event{}, incompatible()
		}
		result.Kind, result.Diff = TurnDiffEvent, params.Diff
	default:
		allowed = append(allowed, "itemId")
		if params.ItemID == nil || domain.Text(*params.ItemID, "native artifact identity", 1024, true) != nil {
			return Event{}, incompatible()
		}
		result.Kind, result.ItemID = ArtifactDeltaEvent, *params.ItemID
		delta := &ArtifactDelta{}
		switch native.Method {
		case "item/plan/delta":
			delta.Kind = PlanTextDelta
		case "item/reasoning/summaryTextDelta":
			allowed = append(allowed, "summaryIndex")
			delta.Kind, delta.Index = ReasoningSummaryDelta, params.SummaryIndex
		case "item/reasoning/summaryPartAdded":
			allowed = append(allowed, "summaryIndex")
			delta.Kind, delta.Index = ReasoningSummaryAdded, params.SummaryIndex
		case "item/reasoning/textDelta":
			allowed = append(allowed, "contentIndex")
			delta.Kind, delta.Index = ReasoningContentDelta, params.ContentIndex
		default:
			return Event{}, incompatible()
		}
		if delta.Kind != PlanTextDelta && (delta.Index == nil || *delta.Index < 0 || *delta.Index >= MaxArtifactParts) {
			return Event{}, incompatible()
		}
		if delta.Kind != ReasoningSummaryAdded {
			allowed = append(allowed, "delta")
			if params.Delta == nil || domain.Text(*params.Delta, "native artifact delta", nativewire.MaxFrame, false) != nil {
				return Event{}, incompatible()
			}
			delta.Text = *params.Delta
		}
		result.ArtifactDelta = delta
	}
	for field := range fields {
		if !slices.Contains(allowed, field) {
			return Event{}, incompatible()
		}
	}
	return result, nil
}

func decodeArtifact(raw json.RawMessage, kind string) (*Artifact, error) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil {
		return nil, incompatible()
	}
	var item struct {
		Type    ArtifactKind    `json:"type"`
		ID      string          `json:"id"`
		Text    *string         `json:"text"`
		Summary json.RawMessage `json:"summary"`
		Content json.RawMessage `json:"content"`
	}
	if domain.Decode(raw, &item) != nil || string(item.Type) != kind || domain.Text(item.ID, "native artifact identity", 1024, true) != nil {
		return nil, incompatible()
	}
	result := &Artifact{ID: item.ID, Kind: item.Type}
	allowed := []string{"type", "id"}
	switch item.Type {
	case PlanArtifact:
		allowed = append(allowed, "text")
		if item.Text == nil || domain.Text(*item.Text, "native plan text", nativewire.MaxFrame, false) != nil {
			return nil, incompatible()
		}
		result.Text = *item.Text
	case ReasoningArtifact:
		allowed = append(allowed, "summary", "content")
		var err error
		result.Summary, err = decodeArtifactParts(item.Summary)
		if err != nil {
			return nil, err
		}
		result.Content, err = decodeArtifactParts(item.Content)
		if err != nil {
			return nil, err
		}
	default:
		return nil, incompatible()
	}
	for field := range fields {
		if !slices.Contains(allowed, field) {
			return nil, incompatible()
		}
	}
	return result, nil
}

func decodeArtifactParts(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		// The pinned native schema defines an explicit empty-array default.
		return []string{}, nil
	}
	var parts []*string
	if json.Unmarshal(raw, &parts) != nil || parts == nil || len(parts) > MaxArtifactParts {
		return nil, incompatible()
	}
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == nil || domain.Text(*part, "native artifact part", nativewire.MaxFrame, false) != nil {
			return nil, incompatible()
		}
		result = append(result, *part)
	}
	return result, nil
}
