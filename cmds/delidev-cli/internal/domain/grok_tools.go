package domain

import (
	"reflect"
	"slices"
)

type GrokToolName string
type GrokToolPhase string

const (
	GrokRead                GrokToolName  = "read_file"
	GrokWrite               GrokToolName  = "write"
	GrokAsk                 GrokToolName  = "ask_user_question"
	GrokEnterPlan           GrokToolName  = "enter_plan_mode"
	GrokExitPlan            GrokToolName  = "exit_plan_mode"
	GrokArguments           GrokToolPhase = "arguments"
	GrokDeclared            GrokToolPhase = "declared"
	GrokDescribed           GrokToolPhase = "described"
	GrokCompleted           GrokToolPhase = "completed"
	GrokFailed              GrokToolPhase = "failed"
	GrokNativeTool          ToolKind      = "grok-native"
	GrokModeProgress        ProgressKind  = "grok-mode"
	GrokInteractionProgress ProgressKind  = "grok-interaction"
)

type GrokToolMetadata struct {
	EventID       string `json:"event_id"`
	ContextTokens string `json:"context_tokens"`
	TimestampMS   string `json:"timestamp_ms"`
	StreamStartMS string `json:"stream_start_ms"`
	TurnStartMS   string `json:"turn_start_ms"`
}

type GrokArgumentChunk struct {
	Index uint64        `json:"index"`
	ID    *string       `json:"id"`
	Name  *GrokToolName `json:"name"`
	Text  string        `json:"text"`
}

// Native descriptions/results are retained separately from response authority.
// Plan file actions carry revision provenance instead of a filesystem path.
// Original argument chunks remain native content, never filesystem authority.
type GrokPlanFileOrigin struct {
	EntryToolID  string `json:"entry_tool_id"`
	EntryEventID string `json:"entry_event_id"`
	Revision     uint64 `json:"revision"`
}

type GrokNativeInteractionStage string

const (
	GrokPermissionPending  GrokNativeInteractionStage = "permission-pending"
	GrokPermissionResolved GrokNativeInteractionStage = "permission-resolved"
	GrokAnswerPending      GrokNativeInteractionStage = "question-pending"
	GrokAnswerResolved     GrokNativeInteractionStage = "question-resolved"
	GrokPlanPending        GrokNativeInteractionStage = "plan-approval-pending"
	GrokPlanResolved       GrokNativeInteractionStage = "plan-approval-resolved"
)

type GrokNativeInteractionObservation struct {
	ToolID string                     `json:"tool_id"`
	Stage  GrokNativeInteractionStage `json:"stage"`
}

func (v GrokNativeInteractionObservation) Validate() error {
	if Text(v.ToolID, "original native tool", 256, true) != nil || !slices.Contains([]GrokNativeInteractionStage{GrokPermissionPending, GrokPermissionResolved, GrokAnswerPending, GrokAnswerResolved, GrokPlanPending, GrokPlanResolved}, v.Stage) {
		return invalidInteraction()
	}
	return nil
}

type GrokToolObservation struct {
	PlanFile            *GrokPlanFileOrigin `json:"plan_file,omitempty"`
	Name                GrokToolName        `json:"name"`
	Phase               GrokToolPhase       `json:"phase"`
	Metadata            *GrokToolMetadata   `json:"metadata,omitempty"`
	Arguments           *GrokArgumentChunk  `json:"arguments,omitempty"`
	Path                string              `json:"path,omitempty"`
	Content             string              `json:"content,omitempty"`
	Output              *string             `json:"output,omitempty"`
	Old                 *string             `json:"old,omitempty"`
	Questions           []GrokQuestion      `json:"questions,omitempty"`
	InheritedPermission ID                  `json:"inherited_permission,omitempty"`
}

func (g GrokToolObservation) Validate(status ToolStatus) error {
	if !slices.Contains([]GrokToolName{GrokRead, GrokWrite, GrokAsk, GrokEnterPlan, GrokExitPlan}, g.Name) || Text(g.Path, "native path", 8192, false) != nil || Text(g.Content, "native tool content", 256<<10, false) != nil || g.Output != nil && Text(*g.Output, "native output", 256<<10, false) != nil || g.Old != nil && Text(*g.Old, "native prior content", 256<<10, false) != nil || g.InheritedPermission != "" && g.InheritedPermission.Validate() != nil {
		return invalidTool()
	}
	if g.Phase == GrokArguments {
		if g.Path != "" || g.Content != "" || g.Output != nil || g.Old != nil || g.Questions != nil || g.PlanFile != nil || g.InheritedPermission != "" || g.Arguments == nil || g.Metadata != nil || status != ToolPending || g.Arguments.Index >= 128 || (g.Arguments.ID == nil) != (g.Arguments.Name == nil) || Text(g.Arguments.Text, "native arguments", 256<<10, false) != nil {
			return invalidTool()
		}
		if g.Arguments.Name != nil && (*g.Arguments.Name != g.Name || Text(*g.Arguments.ID, "native argument identity", 256, true) != nil) {
			return invalidTool()
		}
		return nil
	}
	if g.Arguments != nil || g.Metadata == nil || len(g.Questions) > 32 || (g.Phase != GrokCompleted && g.Phase != GrokFailed) && (g.Output != nil || g.Old != nil) {
		return invalidTool()
	}
	if (g.Name != GrokWrite && g.Name != GrokRead) && (g.Path != "" || g.Content != "" || g.Old != nil || g.PlanFile != nil || g.InheritedPermission != "") || g.Name != GrokAsk && g.Questions != nil || g.Name != GrokWrite && (g.Old != nil || g.InheritedPermission != "") {
		return invalidTool()
	}
	if g.PlanFile != nil && (g.PlanFile.Revision >= 128 || Text(g.PlanFile.EntryToolID, "native Plan entry", 256, true) != nil || Text(g.PlanFile.EntryEventID, "native Plan entry event", 128, true) != nil || g.InheritedPermission != "" || g.Path != "") {
		return invalidTool()
	}
	if g.Metadata.Validate() != nil {
		return invalidTool()
	}
	switch g.Phase {
	case GrokDeclared, GrokDescribed:
		if status != ToolPending {
			return invalidTool()
		}
	case GrokCompleted:
		if status != ToolCompleted {
			return invalidTool()
		}
	case GrokFailed:
		if status != ToolFailed || g.Name != GrokWrite {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	return nil
}

func (m GrokToolMetadata) Validate() error {
	for _, value := range []string{m.ContextTokens, m.TimestampMS, m.StreamStartMS, m.TurnStartMS} {
		if _, ok := grokCount(value); !ok {
			return invalidTool()
		}
	}
	for _, value := range []string{m.TimestampMS, m.StreamStartMS, m.TurnStartMS} {
		n, _ := grokCount(value)
		if n > 253402300799999 {
			return invalidTool()
		}
	}
	if Text(m.EventID, "native event", 128, true) != nil {
		return invalidTool()
	}
	return nil
}

type GrokModeObservation struct {
	ToolID      string   `json:"tool_id"`
	Mode        GrokMode `json:"mode"`
	EventID     string   `json:"event_id"`
	TimestampMS string   `json:"timestamp_ms"`
}

func ValidateGrokToolTransition(prior, next ToolSnapshot, thread string) error {
	if prior.Kind != GrokNativeTool || next.Kind != GrokNativeTool || prior.Grok == nil || next.Grok == nil || prior.Grok.Name != next.Grok.Name {
		return invalidTool()
	}
	a, b := prior.Grok, next.Grok
	allowed := a.Phase == GrokArguments && (b.Phase == GrokArguments || b.Phase == GrokDeclared) || a.Phase == GrokDeclared && b.Phase == GrokDescribed || a.Phase == GrokDescribed && (b.Phase == GrokCompleted || b.Phase == GrokFailed)
	if !allowed {
		return invalidTool()
	}
	if a.Phase != GrokArguments && (a.Path != b.Path || a.Content != b.Content || !reflect.DeepEqual(a.PlanFile, b.PlanFile) || a.InheritedPermission != b.InheritedPermission || !reflect.DeepEqual(a.Questions, b.Questions)) {
		return invalidTool()
	}
	if a.Metadata != nil && b.Metadata != nil {
		i, e1 := GrokEventIndex(a.Metadata.EventID, thread)
		j, e2 := GrokEventIndex(b.Metadata.EventID, thread)
		if e1 != nil || e2 != nil || j <= i {
			return invalidTool()
		}
	}
	return nil
}
