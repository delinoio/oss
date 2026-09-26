package domain

import "reflect"

// This is a read-only presentation of the pinned native Read tool. Paths and
// output are original content, never authority to open a local file or URL.
type OpenCodeReadInput struct {
	FilePath *string `json:"filePath,omitempty"`
	Offset   *uint64 `json:"offset,omitempty"`
	Limit    *uint64 `json:"limit,omitempty"`
}

type OpenCodeReadDisplayKind string

const (
	OpenCodeReadFile      OpenCodeReadDisplayKind = "file"
	OpenCodeReadDirectory OpenCodeReadDisplayKind = "directory"
)

type OpenCodeReadDisplay struct {
	Kind         OpenCodeReadDisplayKind `json:"type"`
	Path         string                  `json:"path"`
	Text         *string                 `json:"text,omitempty"`
	Entries      []string                `json:"entries"`
	LineStart    *uint64                 `json:"lineStart,omitempty"`
	LineEnd      *uint64                 `json:"lineEnd,omitempty"`
	TotalLines   *uint64                 `json:"totalLines,omitempty"`
	Offset       *uint64                 `json:"offset,omitempty"`
	TotalEntries *uint64                 `json:"totalEntries,omitempty"`
	Truncated    *bool                   `json:"truncated"`
}

type OpenCodeReadMetadata struct {
	Preview     *string              `json:"preview,omitempty"`
	Truncated   *bool                `json:"truncated,omitempty"`
	Loaded      []string             `json:"loaded"`
	Display     *OpenCodeReadDisplay `json:"display,omitempty"`
	Interrupted *bool                `json:"interrupted,omitempty"`
}

type OpenCodeToolTiming struct {
	Start uint64  `json:"start"`
	End   *uint64 `json:"end,omitempty"`
}

type OpenCodeReadObservation struct {
	CallID           string                `json:"call_id"`
	Input            OpenCodeReadInput     `json:"input"`
	Raw              *string               `json:"raw,omitempty"`
	Title            *string               `json:"title,omitempty"`
	Output           *string               `json:"output,omitempty"`
	Error            *string               `json:"error,omitempty"`
	Metadata         *OpenCodeReadMetadata `json:"metadata,omitempty"`
	Timing           *OpenCodeToolTiming   `json:"time,omitempty"`
	ProviderExecuted *bool                 `json:"provider_executed,omitempty"`
}

const maxNativeExactInteger = uint64(1<<53 - 1)

func (r OpenCodeReadObservation) Validate(status ToolStatus) error {
	if Text(r.CallID, "native read call", 1024, true) != nil || r.ProviderExecuted != nil && *r.ProviderExecuted {
		return invalidTool()
	}
	for _, field := range []*string{r.Input.FilePath, r.Raw, r.Title, r.Output, r.Error} {
		if field != nil && Text(*field, "native read content", MaxMessageText, false) != nil {
			return invalidTool()
		}
	}
	for _, count := range []*uint64{r.Input.Offset, r.Input.Limit} {
		if count != nil && *count > maxNativeExactInteger {
			return invalidTool()
		}
	}
	if r.Timing != nil && (r.Timing.Start > maxNativeExactInteger || r.Timing.End != nil && (*r.Timing.End < r.Timing.Start || *r.Timing.End > maxNativeExactInteger)) {
		return invalidTool()
	}
	switch status {
	case ToolPending:
		if r.Raw == nil || r.Title != nil || r.Output != nil || r.Error != nil || r.Timing != nil || r.Metadata != nil {
			return invalidTool()
		}
	case ToolRunning:
		if r.Raw != nil || r.Input.FilePath == nil || r.Timing == nil || r.Timing.End != nil || r.Output != nil || r.Error != nil {
			return invalidTool()
		}
	case ToolCompleted:
		if r.Raw != nil || r.Input.FilePath == nil || r.Timing == nil || r.Timing.End == nil || r.Title == nil || r.Output == nil || r.Error != nil || r.Metadata == nil || r.Metadata.Preview == nil || r.Metadata.Truncated == nil || r.Metadata.Loaded == nil {
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
		if m.Preview != nil && Text(*m.Preview, "native read preview", MaxMessageText, false) != nil || len(m.Loaded) > 1024 {
			return invalidTool()
		}
		for _, path := range m.Loaded {
			if Text(path, "native loaded instruction", 32768, false) != nil {
				return invalidTool()
			}
		}
		if m.Display != nil && m.Display.Validate() != nil {
			return invalidTool()
		}
	}
	return nil
}

func (d OpenCodeReadDisplay) Validate() error {
	if Text(d.Path, "native read path", 32768, false) != nil || d.Truncated == nil {
		return invalidTool()
	}
	for _, n := range []*uint64{d.LineStart, d.LineEnd, d.TotalLines, d.Offset, d.TotalEntries} {
		if n != nil && *n > maxNativeExactInteger {
			return invalidTool()
		}
	}
	switch d.Kind {
	case OpenCodeReadFile:
		// Empty files have a native line end of zero. Do not invent a range.
		if d.Text == nil || Text(*d.Text, "native read display", MaxMessageText, false) != nil || d.LineStart == nil || d.LineEnd == nil || d.TotalLines == nil || d.Entries != nil || d.Offset != nil || d.TotalEntries != nil {
			return invalidTool()
		}
	case OpenCodeReadDirectory:
		if d.Entries == nil || len(d.Entries) > 10000 || d.Offset == nil || d.TotalEntries == nil || d.Text != nil || d.LineStart != nil || d.LineEnd != nil || d.TotalLines != nil {
			return invalidTool()
		}
		for _, entry := range d.Entries {
			if Text(entry, "native directory entry", 32768, false) != nil {
				return invalidTool()
			}
		}
	default:
		return invalidTool()
	}
	return nil
}

// A native pending proposal may change before execution. Once running, its
// applied input and original start time are immutable; failure can also close a
// pending proposal without inventing a running observation.
func ValidateOpenCodeReadTransition(prior, next ToolSnapshot) error {
	if prior.Kind != OpenCodeReadTool || next.Kind != prior.Kind || prior.Validate() != nil || next.Validate() != nil || prior.Read.CallID != next.Read.CallID || !reflect.DeepEqual(prior.Read.ProviderExecuted, next.Read.ProviderExecuted) {
		return invalidTool()
	}
	if prior.Status == ToolPending {
		if next.Status == ToolCompleted {
			return invalidTool()
		}
	} else if prior.Status != ToolRunning || next.Status == ToolPending || !reflect.DeepEqual(prior.Read.Input, next.Read.Input) || prior.Read.Timing.Start != next.Read.Timing.Start {
		return invalidTool()
	}
	return nil
}
