package worker

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

// Only the pinned local Read shape crosses this presentation boundary. Native
// image/PDF attachments, provider-executed calls and unknown metadata need their
// own adapters; never omit those fields to make a tool appear supported.
func openCodeReadSnapshot(native *opencode.NativeToolPart) (domain.ToolSnapshot, error) {
	bad := func() (domain.ToolSnapshot, error) {
		return domain.ToolSnapshot{}, domain.Fail(domain.Unsupported, "This OpenCode tool needs an additional presentation profile.", "Retain the original tool and its complete content without omission or reinterpretation.")
	}
	if native == nil || native.Name != "read" || len(native.Attachments) != 0 || native.Timing != nil && native.Timing.Compacted != nil {
		return bad()
	}
	r := &domain.OpenCodeReadObservation{CallID: native.CallID, Raw: native.Raw, Title: native.Title, Output: native.Output, Error: native.Error}
	if len(native.Input) == 0 || domain.Decode(native.Input, &r.Input) != nil {
		return bad()
	}
	if native.Metadata != nil {
		r.Metadata = &domain.OpenCodeReadMetadata{}
		if domain.Decode(native.Metadata, r.Metadata) != nil {
			return bad()
		}
	}
	if native.PartMetadata != nil {
		var metadata struct {
			ProviderExecuted *bool `json:"providerExecuted,omitempty"`
		}
		if domain.Decode(native.PartMetadata, &metadata) != nil {
			return bad()
		}
		r.ProviderExecuted = metadata.ProviderExecuted
	}
	if native.Timing != nil {
		r.Timing = &domain.OpenCodeToolTiming{Start: native.Timing.Start, End: native.Timing.End}
	}
	s := domain.ToolSnapshot{Kind: domain.OpenCodeReadTool, Read: r}
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
