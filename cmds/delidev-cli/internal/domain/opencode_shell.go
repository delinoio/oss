package domain

// Shell snapshots retain native content only. Commands and output paths never
// become execution, filesystem or hyperlink authority through this document.
type OpenCodeShellInput struct {
	Command *string `json:"command,omitempty"`
	Workdir *string `json:"workdir,omitempty"`
	Timeout *uint64 `json:"timeout,omitempty"`
}

type OpenCodeShellMetadata struct {
	Output       *string `json:"output,omitempty"`
	Exit         *int64  `json:"exit"`
	ExitObserved bool    `json:"exit_observed"`
	Truncated    *bool   `json:"truncated,omitempty"`
	OutputPath   *string `json:"outputPath,omitempty"`
	Interrupted  *bool   `json:"interrupted,omitempty"`
}

type OpenCodeShellObservation struct {
	CallID           string                 `json:"call_id"`
	Input            OpenCodeShellInput     `json:"input"`
	Raw              *string                `json:"raw,omitempty"`
	Title            *string                `json:"title,omitempty"`
	Output           *string                `json:"output,omitempty"`
	Error            *string                `json:"error,omitempty"`
	Metadata         *OpenCodeShellMetadata `json:"metadata,omitempty"`
	Timing           *OpenCodeToolTiming    `json:"time,omitempty"`
	ProviderExecuted *bool                  `json:"provider_executed,omitempty"`
}

func (r OpenCodeShellObservation) Validate(status ToolStatus) error {
	if Text(r.CallID, "native shell call", 1024, true) != nil || r.ProviderExecuted != nil && *r.ProviderExecuted {
		return invalidTool()
	}
	for _, field := range []*string{r.Input.Command, r.Input.Workdir, r.Raw, r.Title, r.Output, r.Error} {
		if field != nil && Text(*field, "native shell content", MaxMessageText, false) != nil {
			return invalidTool()
		}
	}
	if r.Input.Timeout != nil && (*r.Input.Timeout == 0 || *r.Input.Timeout > maxNativeExactInteger) || r.Timing != nil && (r.Timing.Start > maxNativeExactInteger || r.Timing.End != nil && (*r.Timing.End < r.Timing.Start || *r.Timing.End > maxNativeExactInteger)) {
		return invalidTool()
	}
	switch status {
	case ToolPending:
		if r.Raw == nil || r.Title != nil || r.Output != nil || r.Error != nil || r.Timing != nil || r.Metadata != nil {
			return invalidTool()
		}
	case ToolRunning:
		if r.Raw != nil || r.Input.Command == nil || r.Timing == nil || r.Timing.End != nil || r.Output != nil || r.Error != nil {
			return invalidTool()
		}
	case ToolCompleted:
		if r.Raw != nil || r.Input.Command == nil || r.Timing == nil || r.Timing.End == nil || r.Title == nil || r.Output == nil || r.Error != nil || r.Metadata == nil || r.Metadata.Output == nil || r.Metadata.Truncated == nil || !r.Metadata.ExitObserved {
			return invalidTool()
		}
	case ToolFailed:
		if r.Raw != nil || r.Timing == nil || r.Timing.End == nil || r.Title != nil || r.Output != nil || r.Error == nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	if m := r.Metadata; m != nil {
		if m.Exit != nil && (!m.ExitObserved || *m.Exit < -int64(maxNativeExactInteger) || *m.Exit > int64(maxNativeExactInteger)) || m.OutputPath != nil && (m.Truncated == nil || !*m.Truncated) {
			return invalidTool()
		}
		for _, field := range []*string{m.Output, m.OutputPath} {
			if field != nil && Text(*field, "native shell metadata", MaxMessageText, false) != nil {
				return invalidTool()
			}
		}
	}
	return nil
}
