package domain

import (
	"encoding/json"
	"slices"
)

type ArtifactKind string
type ArtifactDeltaKind string
type ProgressKind string
type PlanStepStatus string

const (
	OpenCodeRevisionArtifact ArtifactKind = "opencode-revision"
	PlanArtifact             ArtifactKind = "plan"
	ReasoningArtifact        ArtifactKind = "reasoning"
	ReasoningTextArtifact    ArtifactKind = "reasoning-text"

	PlanTextDelta         ArtifactDeltaKind = "plan-text"
	ReasoningSummaryDelta ArtifactDeltaKind = "reasoning-summary"
	ReasoningContentDelta ArtifactDeltaKind = "reasoning-content"
	ReasoningTextDelta    ArtifactDeltaKind = "reasoning-text"
	ReasoningSummaryAdded ArtifactDeltaKind = "reasoning-summary-added"

	OpenCodeWorkspaceProgressKind ProgressKind = "opencode-workspace"
	OpenCodeChangesProgressKind   ProgressKind = "opencode-changes"
	OpenCodeTodoProgressKind      ProgressKind = "opencode-todo"
	PlanProgress                  ProgressKind = "plan"
	DiffProgress                  ProgressKind = "diff"

	PlanPending   PlanStepStatus = "pending"
	PlanRunning   PlanStepStatus = "running"
	PlanCompleted PlanStepStatus = "completed"
)

const MaxArtifactParts = 1024

type ArtifactSnapshot struct {
	Grok     *GrokPlanRevision `json:"grok,omitempty"`
	Revision *OpenCodeRevision `json:"revision,omitempty"`
	Kind     ArtifactKind      `json:"kind"`
	Text     string            `json:"text"`
	Summary  []string          `json:"summary"`
	Content  []string          `json:"content"`
}

func (s *ArtifactSnapshot) UnmarshalJSON(raw []byte) error {
	// encoding/json otherwise turns null string elements into empty strings,
	// which would manufacture native content at an observed reasoning index.
	var wire struct {
		Grok     *GrokPlanRevision `json:"grok,omitempty"`
		Revision *OpenCodeRevision `json:"revision,omitempty"`
		Kind     ArtifactKind      `json:"kind"`
		Text     *string           `json:"text"`
		Summary  []*string         `json:"summary"`
		Content  []*string         `json:"content"`
	}
	if Decode(raw, &wire) != nil || wire.Text == nil {
		return invalidArtifact()
	}
	*s = ArtifactSnapshot{Kind: wire.Kind, Text: *wire.Text, Revision: wire.Revision, Grok: wire.Grok}
	for _, pair := range []struct {
		source []*string
		target *[]string
	}{{wire.Summary, &s.Summary}, {wire.Content, &s.Content}} {
		if pair.source == nil {
			continue
		}
		*pair.target = make([]string, 0, len(pair.source))
		for _, part := range pair.source {
			if part == nil {
				return invalidArtifact()
			}
			*pair.target = append(*pair.target, *part)
		}
	}
	return nil
}

type ArtifactDelta struct {
	Kind  ArtifactDeltaKind `json:"kind"`
	Index *int64            `json:"index"`
	Text  string            `json:"text"`
}

func invalidArtifact() error {
	return Fail(InvalidArgument, "Invalid native artifact observation.", "Preserve the bounded typed content, indices and native ownership.")
}

func (s ArtifactSnapshot) Validate() error {
	if s.Grok != nil && (s.Kind != PlanArtifact || s.Text != s.Grok.Content || s.Revision != nil) {
		return invalidArtifact()
	}
	if s.Kind == OpenCodeRevisionArtifact {
		if s.Revision == nil || s.Text != "" || s.Summary != nil || s.Content != nil || s.Revision.Validate() != nil {
			return invalidArtifact()
		}
		return nil
	}
	if s.Revision != nil {
		return invalidArtifact()
	}
	switch s.Kind {
	case PlanArtifact, ReasoningTextArtifact:
		if s.Summary != nil || s.Content != nil || Text(s.Text, "native artifact text", MaxMessageText, false) != nil {
			return invalidArtifact()
		}
	case ReasoningArtifact:
		if s.Text != "" || s.Summary == nil || s.Content == nil || len(s.Summary) > MaxArtifactParts || len(s.Content) > MaxArtifactParts {
			return invalidArtifact()
		}
		for _, parts := range [][]string{s.Summary, s.Content} {
			for _, part := range parts {
				if Text(part, "native reasoning part", MaxMessageText, false) != nil {
					return invalidArtifact()
				}
			}
		}
	default:
		return invalidArtifact()
	}
	return nil
}

func (d ArtifactDelta) ArtifactKind() ArtifactKind {
	switch d.Kind {
	case PlanTextDelta:
		return PlanArtifact
	case ReasoningTextDelta:
		return ReasoningTextArtifact
	case ReasoningSummaryDelta, ReasoningContentDelta, ReasoningSummaryAdded:
		return ReasoningArtifact
	default:
		return ""
	}
}

func (d ArtifactDelta) Validate() error {
	if d.ArtifactKind() == "" || Text(d.Text, "native artifact delta", MaxMessageText, false) != nil {
		return invalidArtifact()
	}
	if d.Kind == PlanTextDelta || d.Kind == ReasoningTextDelta {
		if d.Index != nil {
			return invalidArtifact()
		}
	} else if d.Index == nil || *d.Index < 0 || *d.Index >= MaxArtifactParts || (d.Kind == ReasoningSummaryAdded && d.Text != "") {
		return invalidArtifact()
	}
	return nil
}

type ExecutionArtifactUpdate struct {
	ID             ID                `json:"id"`
	NativeID       string            `json:"native_id"`
	NativeParentID string            `json:"native_parent_id,omitempty"`
	Snapshot       *ArtifactSnapshot `json:"snapshot,omitempty"`
	Delta          *ArtifactDelta    `json:"delta,omitempty"`
}

func (k ExecutionEventKind) IsArtifact() bool {
	return slices.Contains([]ExecutionEventKind{ExecutionArtifactStarted, ExecutionArtifactCompleted, ExecutionArtifactDelta}, k)
}

func (u ExecutionArtifactUpdate) Validate(kind ExecutionEventKind) error {
	if u.ID.Validate() != nil || Text(u.NativeID, "native artifact identity", 1024, true) != nil || Text(u.NativeParentID, "native artifact parent", 1024, false) != nil {
		return invalidArtifact()
	}
	switch kind {
	case ExecutionArtifactStarted, ExecutionArtifactCompleted:
		if u.Snapshot == nil || u.Delta != nil || u.Snapshot.Validate() != nil {
			return invalidArtifact()
		}
	case ExecutionArtifactDelta:
		if u.Delta == nil || u.Snapshot != nil || u.Delta.Validate() != nil {
			return invalidArtifact()
		}
	default:
		return invalidArtifact()
	}
	return boundedArtifactPublication(u)
}

type SequencedArtifactDelta struct {
	Sequence uint64        `json:"sequence"`
	Delta    ArtifactDelta `json:"delta"`
}

// Start, ordered stream observations and authoritative completion stay
// separate. Native plan completion need not extend the streamed draft. We
// preserve reasoning part indices without reconstructing absent content.
type ExecutionArtifact struct {
	Started   ArtifactSnapshot         `json:"started"`
	Completed *ArtifactSnapshot        `json:"completed,omitempty"`
	Deltas    []SequencedArtifactDelta `json:"deltas,omitempty"`
}

type PlanStep struct {
	Step   string         `json:"step"`
	Status PlanStepStatus `json:"status"`
}
type NativePlan struct {
	Explanation *string    `json:"explanation"`
	Steps       []PlanStep `json:"steps"`
}

// Turn progress has no native item identity. A diff is only an observation,
// not repository/file-review ownership or permission to read/write a path.
type NativeProgress struct {
	GrokInteraction *GrokNativeInteractionObservation `json:"grok_interaction,omitempty"`
	GrokMode        *GrokModeObservation              `json:"grok_mode,omitempty"`
	Workspace       *OpenCodeWorkspaceEvent           `json:"workspace,omitempty"`
	Changes         *OpenCodeChanges                  `json:"changes,omitempty"`
	Todo            *OpenCodeTodoProgress             `json:"todo,omitempty"`
	Kind            ProgressKind                      `json:"kind"`
	Plan            *NativePlan                       `json:"plan,omitempty"`
	Diff            *string                           `json:"diff,omitempty"`
}
type ExecutionProgressUpdate struct {
	ID       ID             `json:"id"`
	Progress NativeProgress `json:"progress"`
}

func (u ExecutionProgressUpdate) Validate() error {
	if u.ID.Validate() != nil {
		return invalidArtifact()
	}
	p := u.Progress
	if p.Kind == GrokInteractionProgress {
		if p.GrokInteraction == nil || p.GrokInteraction.Validate() != nil || p.GrokMode != nil || p.Plan != nil || p.Diff != nil || p.Todo != nil || p.Changes != nil || p.Workspace != nil {
			return invalidArtifact()
		}
		return nil
	}
	if p.GrokInteraction != nil {
		return invalidArtifact()
	}
	if p.Kind != GrokModeProgress && p.GrokMode != nil {
		return invalidArtifact()
	}
	switch p.Kind {
	case GrokModeProgress:
		if p.GrokMode == nil || Text(p.GrokMode.ToolID, "original mode tool", 256, true) != nil || !p.GrokMode.Mode.Valid() || Text(p.GrokMode.EventID, "native mode event", 128, true) != nil || p.Workspace != nil || p.Changes != nil || p.Todo != nil || p.Plan != nil || p.Diff != nil {
			return invalidArtifact()
		}
		if n, ok := grokCount(p.GrokMode.TimestampMS); !ok || n > 253402300799999 {
			return invalidArtifact()
		}
	case PlanProgress:
		if p.Workspace != nil || p.Changes != nil || p.Todo != nil || p.Plan == nil || p.Diff != nil || p.Plan.Steps == nil || len(p.Plan.Steps) > MaxArtifactParts || (p.Plan.Explanation != nil && Text(*p.Plan.Explanation, "native plan explanation", MaxMessageText, false) != nil) {
			return invalidArtifact()
		}
		for _, step := range p.Plan.Steps {
			if Text(step.Step, "native plan step", MaxMessageText, false) != nil || !slices.Contains([]PlanStepStatus{PlanPending, PlanRunning, PlanCompleted}, step.Status) {
				return invalidArtifact()
			}
		}
	case OpenCodeWorkspaceProgressKind:
		if p.Workspace == nil || p.Changes != nil || p.Todo != nil || p.Plan != nil || p.Diff != nil || p.Workspace.Validate() != nil {
			return invalidArtifact()
		}
	case OpenCodeChangesProgressKind:
		if p.Workspace != nil || p.Changes == nil || p.Todo != nil || p.Plan != nil || p.Diff != nil || p.Changes.Validate() != nil {
			return invalidArtifact()
		}
	case OpenCodeTodoProgressKind:
		if p.Workspace != nil || p.Changes != nil || p.Todo == nil || p.Plan != nil || p.Diff != nil || NativeIdentity(p.Todo.NativeEventID).Validate(OpenCode, NativeEventIdentity) != nil || ValidateOpenCodeTodos(p.Todo.Todos) != nil {
			return invalidArtifact()
		}
	case DiffProgress:
		if p.Workspace != nil || p.Changes != nil || p.Todo != nil || p.Diff == nil || p.Plan != nil || Text(*p.Diff, "native turn diff", MaxMessageText, false) != nil {
			return invalidArtifact()
		}
	default:
		return invalidArtifact()
	}
	return boundedArtifactPublication(u)
}

func boundedArtifactPublication(value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 512<<10 {
		return Fail(ResourceExhausted, "The artifact observation exceeds its publication bound.", "Retain native history for reconciliation without truncating the observation.")
	}
	return nil
}
