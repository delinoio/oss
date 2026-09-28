package worker

import (
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// The pinned shell implementation intentionally retains the native tool ID
// "bash". Its rolling metadata output and final clipped output are independent
// snapshots, not append-only deltas or separate stdout/stderr streams.
func openCodeShellSnapshot(native *opencode.NativeToolPart) (domain.ToolSnapshot, error) {
	bad := func() (domain.ToolSnapshot, error) { return domain.ToolSnapshot{}, unsupportedOpenCodeEvent() }
	if native == nil || native.Name != "bash" || len(native.Attachments) != 0 || native.Timing != nil && native.Timing.Compacted != nil {
		return bad()
	}
	r := &domain.OpenCodeShellObservation{CallID: native.CallID, Raw: native.Raw, Title: native.Title, Output: native.Output, Error: native.Error}
	if len(native.Input) == 0 || domain.Decode(native.Input, &r.Input) != nil {
		return bad()
	}
	if native.Metadata != nil {
		var m struct {
			Output      *string         `json:"output"`
			Exit        json.RawMessage `json:"exit"`
			Truncated   *bool           `json:"truncated"`
			OutputPath  *string         `json:"outputPath"`
			Interrupted *bool           `json:"interrupted"`
		}
		if domain.Decode(native.Metadata, &m) != nil {
			return bad()
		}
		r.Metadata = &domain.OpenCodeShellMetadata{Output: m.Output, Truncated: m.Truncated, OutputPath: m.OutputPath, Interrupted: m.Interrupted, ExitObserved: len(m.Exit) != 0}
		if len(m.Exit) != 0 && json.Unmarshal(m.Exit, &r.Metadata.Exit) != nil {
			return bad()
		}
	}
	if native.PartMetadata != nil {
		var m struct {
			ProviderExecuted *bool `json:"providerExecuted"`
		}
		if domain.Decode(native.PartMetadata, &m) != nil {
			return bad()
		}
		r.ProviderExecuted = m.ProviderExecuted
	}
	if native.Timing != nil {
		r.Timing = &domain.OpenCodeToolTiming{Start: native.Timing.Start, End: native.Timing.End}
	}
	s := domain.ToolSnapshot{Kind: domain.OpenCodeShellTool, Shell: r}
	switch native.State {
	case opencode.ToolPending:
		s.Status = domain.ToolPending
	case opencode.ToolRunning:
		s.Status = domain.ToolRunning
	case opencode.ToolCompleted:
		s.Status = domain.ToolCompleted
	case opencode.ToolError:
		s.Status = domain.ToolFailed
	default:
		return bad()
	}
	if s.Validate() != nil {
		return bad()
	}
	return s, nil
}
