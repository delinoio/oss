package domain

import (
	"encoding/json"
	"reflect"
	"slices"
)

type OpenCodeBuiltinName string

const (
	OpenCodeTask         OpenCodeBuiltinName = "task"
	OpenCodeWrite        OpenCodeBuiltinName = "write"
	OpenCodeEdit         OpenCodeBuiltinName = "edit"
	OpenCodeApplyPatch   OpenCodeBuiltinName = "apply_patch"
	OpenCodeGlob         OpenCodeBuiltinName = "glob"
	OpenCodeGrep         OpenCodeBuiltinName = "grep"
	OpenCodeQuestionTool OpenCodeBuiltinName = "question"
)

func (n OpenCodeBuiltinName) Valid() bool {
	return slices.Contains([]OpenCodeBuiltinName{OpenCodeWrite, OpenCodeEdit, OpenCodeApplyPatch, OpenCodeGlob, OpenCodeGrep, OpenCodeQuestionTool, OpenCodeTask}, n)
}

// This closed builtin family retains complete original input/metadata JSON as
// text, including exact native number spellings. Reading it never executes a
// tool, applies a reported edit, or promotes native LSP metadata to authority.
type OpenCodeBuiltinObservation struct {
	Name             OpenCodeBuiltinName `json:"name"`
	CallID           string              `json:"call_id"`
	InputJSON        string              `json:"input_json"`
	MetadataJSON     *string             `json:"metadata_json,omitempty"`
	Raw              *string             `json:"raw,omitempty"`
	Title            *string             `json:"title,omitempty"`
	Output           *string             `json:"output,omitempty"`
	Error            *string             `json:"error,omitempty"`
	Timing           *OpenCodeToolTiming `json:"time,omitempty"`
	ProviderExecuted *bool               `json:"provider_executed,omitempty"`
}

func ValidateOpenCodeObjectJSON(value string) error {
	if Text(value, "native structured tool content", MaxMessageText, false) != nil {
		return invalidTool()
	}
	var object map[string]json.RawMessage
	if Decode([]byte(value), &object) != nil || object == nil {
		return invalidTool()
	}
	return nil
}

func (b OpenCodeBuiltinObservation) Validate(status ToolStatus) error {
	if !b.Name.Valid() || Text(b.CallID, "native builtin call", 1024, true) != nil || ValidateOpenCodeObjectJSON(b.InputJSON) != nil || b.MetadataJSON != nil && ValidateOpenCodeObjectJSON(*b.MetadataJSON) != nil || b.ProviderExecuted != nil && *b.ProviderExecuted {
		return invalidTool()
	}
	for _, value := range []*string{b.Raw, b.Title, b.Output, b.Error} {
		if value != nil && Text(*value, "native builtin content", MaxMessageText, false) != nil {
			return invalidTool()
		}
	}
	if t := b.Timing; t != nil && (t.Start > maxNativeExactInteger || t.End != nil && (*t.End < t.Start || *t.End > maxNativeExactInteger)) {
		return invalidTool()
	}
	if b.Name == OpenCodeTask && status != ToolPending {
		if _, err := DecodeOpenCodeForegroundTask([]byte(b.InputJSON)); err != nil {
			return err
		}
		if b.MetadataJSON != nil && *b.MetadataJSON != "{}" {
			if _, err := DecodeOpenCodeTaskMetadata([]byte(*b.MetadataJSON)); err != nil {
				return err
			}
		}
	}
	switch status {
	case ToolPending:
		if b.Raw == nil || b.Timing != nil || b.MetadataJSON != nil || b.Title != nil || b.Output != nil || b.Error != nil {
			return invalidTool()
		}
	case ToolRunning:
		if b.Raw != nil || b.Timing == nil || b.Timing.End != nil || b.Output != nil || b.Error != nil {
			return invalidTool()
		}
	case ToolCompleted:
		if b.Raw != nil || b.Timing == nil || b.Timing.End == nil || b.MetadataJSON == nil || b.Title == nil || b.Output == nil || b.Error != nil {
			return invalidTool()
		}
	case ToolFailed:
		if b.Raw != nil || b.Timing == nil || b.Timing.End == nil || b.Error == nil || b.Output != nil {
			return invalidTool()
		}
		if b.Title != nil {
			if b.Name != OpenCodeTask || b.MetadataJSON == nil {
				return invalidTool()
			}
			metadata, err := DecodeOpenCodeTaskMetadata([]byte(*b.MetadataJSON))
			if err != nil || metadata.Interrupted == nil || !*metadata.Interrupted {
				return invalidTool()
			}
		}
	default:
		return invalidTool()
	}
	return nil
}

func ValidateOpenCodeBuiltinTransition(prior, next ToolSnapshot) error {
	if prior.Kind != OpenCodeBuiltinTool || next.Kind != prior.Kind || prior.Validate() != nil || next.Validate() != nil || prior.Builtin.Name != next.Builtin.Name || prior.Builtin.CallID != next.Builtin.CallID || !reflect.DeepEqual(prior.Builtin.ProviderExecuted, next.Builtin.ProviderExecuted) {
		return invalidTool()
	}
	if next.Status == ToolFailed && next.Builtin.Title != nil && !reflect.DeepEqual(prior.Builtin.Title, next.Builtin.Title) {
		return invalidTool()
	}
	if prior.Status == ToolPending {
		if next.Status == ToolCompleted {
			return invalidTool()
		}
	} else if prior.Status != ToolRunning || next.Status == ToolPending || prior.Builtin.InputJSON != next.Builtin.InputJSON || prior.Builtin.Timing.Start != next.Builtin.Timing.Start {
		return invalidTool()
	}
	return nil
}
