package domain

import "reflect"

type OpenCodeToolTiming struct {
	Start uint64  `json:"start"`
	End   *uint64 `json:"end,omitempty"`
}

func (k ToolKind) IsOpenCode() bool {
	return k == OpenCodeReadTool || k == OpenCodeShellTool || k == OpenCodeTodoTool || k == OpenCodeBuiltinTool
}

func (s ToolSnapshot) OpenCodeCallID() string {
	if s.Kind == OpenCodeReadTool && s.Read != nil {
		return s.Read.CallID
	}
	if s.Kind == OpenCodeShellTool && s.Shell != nil {
		return s.Shell.CallID
	}
	if s.Kind == OpenCodeTodoTool && s.Todo != nil {
		return s.Todo.CallID
	}
	if s.Kind == OpenCodeBuiltinTool && s.Builtin != nil {
		return s.Builtin.CallID
	}
	return ""
}

func ValidateOpenCodeToolTransition(prior, next ToolSnapshot) error {
	if prior.Kind == OpenCodeBuiltinTool {
		return ValidateOpenCodeBuiltinTransition(prior, next)
	}
	if prior.Kind == OpenCodeTodoTool {
		return ValidateOpenCodeTodoTransition(prior, next)
	}
	if prior.Kind == OpenCodeReadTool {
		return ValidateOpenCodeReadTransition(prior, next)
	}
	if prior.Kind != OpenCodeShellTool || next.Kind != prior.Kind || prior.Validate() != nil || next.Validate() != nil || prior.Shell.CallID != next.Shell.CallID || !reflect.DeepEqual(prior.Shell.ProviderExecuted, next.Shell.ProviderExecuted) {
		return invalidTool()
	}
	if prior.Status == ToolPending {
		if next.Status == ToolCompleted {
			return invalidTool()
		}
	} else if prior.Status != ToolRunning || next.Status == ToolPending || !reflect.DeepEqual(prior.Shell.Input, next.Shell.Input) || prior.Shell.Timing.Start != next.Shell.Timing.Start {
		return invalidTool()
	}
	return nil
}
