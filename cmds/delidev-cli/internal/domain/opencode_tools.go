package domain

import "reflect"

type OpenCodeToolTiming struct {
	Start uint64  `json:"start"`
	End   *uint64 `json:"end,omitempty"`
}

func (k ToolKind) IsOpenCode() bool { return k == OpenCodeReadTool || k == OpenCodeShellTool }

func (s ToolSnapshot) OpenCodeCallID() string {
	if s.Kind == OpenCodeReadTool && s.Read != nil {
		return s.Read.CallID
	}
	if s.Kind == OpenCodeShellTool && s.Shell != nil {
		return s.Shell.CallID
	}
	return ""
}

func ValidateOpenCodeToolTransition(prior, next ToolSnapshot) error {
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
