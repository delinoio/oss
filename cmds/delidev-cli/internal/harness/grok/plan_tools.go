package grok

import (
	"encoding/json"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	enterPlanTool fileToolName = "enter_plan_mode"
	exitPlanTool  fileToolName = "exit_plan_mode"
)

type PlanOutcome string

const (
	PlanApproved  PlanOutcome = "approved"
	PlanCancelled PlanOutcome = "cancelled"
	PlanAbandoned PlanOutcome = "abandoned"
)

func (o PlanOutcome) valid() bool {
	return o == PlanApproved || o == PlanCancelled || o == PlanAbandoned
}

type planEntered struct {
	Message string `json:"message"`
	Path    string `json:"plan_file_path"`
	Hints   struct {
		Question string `json:"ask_user"`
		Exit     string `json:"exit_plan"`
		Task     string `json:"task"`
	} `json:"tool_hints"`
	Seed string `json:"plan_file_seed"`
}

type planReady struct {
	Message string `json:"message"`
	Content string `json:"plan_content"`
	Path    string `json:"plan_file_path"`
}

type planObservation struct {
	ID         string
	Name       fileToolName
	Phase      fileToolPhase
	Title      string
	Descriptor toolDescriptor
	Meta       toolObservationMeta
	Message    string
	Entered    *planEntered
	Ready      *planReady
}

type planRequest struct {
	Session domain.ID `json:"sessionId"`
	ID      string    `json:"toolCallId"`
	Content string    `json:"planContent"`
}

func parsePlanRequest(raw []byte, session domain.ID) (planRequest, error) {
	var request planRequest
	if len(raw) > 256<<10 || session.Validate() != nil || decode(raw, &request) != nil || request.Session != session || !text(request.ID, 256) || !text(request.Content, 256<<10) {
		return request, incompatible()
	}
	return request, nil
}

func planDescriptor(d toolDescriptor, name fileToolName) bool {
	if d.Version != 1 || d.Name != name || d.Namespace != fileGrokNamespace || !d.ReadOnly || d.Input != nil {
		return false
	}
	return name == enterPlanTool && d.Kind == "enter_plan" && d.Label == "Enter Plan Mode" || name == exitPlanTool && d.Kind == "exit_plan" && d.Label == "Exit Plan Mode"
}

func planPath(path string) bool {
	return text(path, 8192) && filepath.IsAbs(path) && filepath.Clean(path) == path
}

func parsePlanObservation(raw []byte, session domain.ID, prompt string, name fileToolName) (planObservation, error) {
	value := planObservation{Name: name}
	var envelope struct {
		Session domain.ID           `json:"sessionId"`
		Update  json.RawMessage     `json:"update"`
		Meta    toolObservationMeta `json:"_meta"`
	}
	if name != enterPlanTool && name != exitPlanTool || session.Validate() != nil || len(raw) > 1<<20 || decode(raw, &envelope) != nil || envelope.Session != session {
		return value, incompatible()
	}
	var variant struct {
		Kind   string  `json:"sessionUpdate"`
		Status *string `json:"status"`
	}
	if json.Unmarshal(envelope.Update, &variant) != nil {
		return value, incompatible()
	}
	value.Meta = envelope.Meta
	switch {
	case variant.Kind == "tool_call":
		var declared struct {
			Kind  string                 `json:"sessionUpdate"`
			ID    string                 `json:"toolCallId"`
			Title string                 `json:"title"`
			Input struct{}               `json:"rawInput"`
			Meta  toolDescriptorEnvelope `json:"_meta"`
		}
		if decode(envelope.Update, &declared) != nil || declared.Title != string(name) || !planDescriptor(declared.Meta.Tool, name) {
			return value, incompatible()
		}
		value.ID, value.Phase, value.Title, value.Descriptor = declared.ID, fileToolDeclared, declared.Title, declared.Meta.Tool
	case variant.Kind == "tool_call_update" && variant.Status == nil:
		var detailed struct {
			Kind      string            `json:"sessionUpdate"`
			ID        string            `json:"toolCallId"`
			Category  string            `json:"kind"`
			Title     string            `json:"title"`
			Locations []json.RawMessage `json:"locations"`
			Input     struct {
				Variant string `json:"variant"`
			} `json:"rawInput"`
			Meta toolDescriptorEnvelope `json:"_meta"`
		}
		title, variant := "Plan: Enter", "EnterPlanMode"
		if name == exitPlanTool {
			title, variant = "Plan: Exit", "ExitPlanMode"
		}
		if decode(envelope.Update, &detailed) != nil || detailed.Category != "other" || detailed.Title != title || len(detailed.Locations) != 0 || detailed.Input.Variant != variant || !planDescriptor(detailed.Meta.Tool, name) {
			return value, incompatible()
		}
		value.ID, value.Phase, value.Title, value.Descriptor = detailed.ID, fileToolDescribed, detailed.Title, detailed.Meta.Tool
	case variant.Kind == "tool_call_update" && variant.Status != nil && *variant.Status == "completed":
		var completed struct {
			Kind    string  `json:"sessionUpdate"`
			ID      string  `json:"toolCallId"`
			Status  string  `json:"status"`
			Title   *string `json:"title,omitempty"`
			Content []struct {
				Type    string     `json:"type"`
				Content promptText `json:"content"`
			} `json:"content"`
			Output json.RawMessage `json:"rawOutput,omitempty"`
		}
		if decode(envelope.Update, &completed) != nil || len(completed.Content) != 1 || completed.Content[0].Type != "content" || completed.Content[0].Content.Type != "text" || !text(completed.Content[0].Content.Text, 256<<10) {
			return value, incompatible()
		}
		value.ID, value.Phase, value.Message = completed.ID, fileToolCompleted, completed.Content[0].Content.Text
		if name == enterPlanTool {
			var output struct {
				Type    string      `json:"type"`
				Entered planEntered `json:"Entered"`
			}
			if decode(completed.Output, &output) != nil || output.Type != "EnterPlanMode" || completed.Title == nil || *completed.Title != "Plan mode entered" || !text(output.Entered.Message, 256<<10) || !planPath(output.Entered.Path) || output.Entered.Hints.Question != string(askQuestionTool) || output.Entered.Hints.Exit != string(exitPlanTool) || output.Entered.Hints.Task != "spawn_subagent" || output.Entered.Seed != "empty" {
				return value, incompatible()
			}
			value.Entered = &output.Entered
		} else if len(completed.Output) != 0 {
			var output struct {
				Type  string    `json:"type"`
				Ready planReady `json:"PlanReady"`
			}
			if decode(completed.Output, &output) != nil || output.Type != "ExitPlanMode" || completed.Title == nil || *completed.Title != "Plan mode exited" || !text(output.Ready.Message, 256<<10) || !text(output.Ready.Content, 256<<10) || !planPath(output.Ready.Path) {
				return value, incompatible()
			}
			value.Ready = &output.Ready
		} else if completed.Title != nil {
			return value, incompatible()
		}
		if completed.Title != nil {
			value.Title = *completed.Title
		}
		// Cancelled and abandoned exits both have text-only native completion.
		// Never parse that prose into a decision: the original durable response
		// and independently observed mode transition supply that authority.
	default:
		return value, incompatible()
	}
	if !text(value.ID, 256) || !value.Meta.validate(session, prompt, value.ID, value.Phase, name) {
		return value, incompatible()
	}
	return value, nil
}
