package grok

import (
	"encoding/json"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type fileToolName string
type fileToolPhase string
type fileToolKind string
type fileToolNamespace string
type fileToolMetadataKind string

const (
	readFileTool          fileToolName         = "read_file"
	writeFileTool         fileToolName         = "write"
	fileReadKind          fileToolKind         = "read"
	fileWriteKind         fileToolKind         = "write"
	fileEditKind          fileToolKind         = "edit"
	fileGrokNamespace     fileToolNamespace    = "grok_build"
	fileOpenCodeNamespace fileToolNamespace    = "opencode"
	fileCallMetadata      fileToolMetadataKind = "ToolCall"
	fileUpdateMetadata    fileToolMetadataKind = "ToolCallUpdate"

	fileToolDeclared  fileToolPhase = "declared"
	fileToolDescribed fileToolPhase = "described"
	fileToolCompleted fileToolPhase = "completed"
)

// These native facts are private observations, never permission grants or
// filesystem authority. The original input/response controller must own them.
type toolDescriptor struct {
	Version   uint32            `json:"version"`
	Name      fileToolName      `json:"name"`
	Kind      fileToolKind      `json:"kind"`
	Namespace fileToolNamespace `json:"namespace"`
	Label     string            `json:"label"`
	ReadOnly  bool              `json:"read_only"`
	Input     *struct {
		Path string `json:"path"`
	} `json:"input,omitempty"`
}

type toolDescriptorEnvelope struct {
	Tool toolDescriptor `json:"x.ai/tool"`
}

type toolObservationMeta struct {
	Context     uint64               `json:"totalTokens"`
	Event       string               `json:"eventId"`
	TimestampMS uint64               `json:"agentTimestampMs"`
	Prompt      string               `json:"promptId"`
	StreamMS    uint64               `json:"streamStartMs"`
	TurnMS      uint64               `json:"turnStartMs"`
	Type        fileToolMetadataKind `json:"updateType"`
	Params      struct {
		ID     string          `json:"toolCallId"`
		Title  *string         `json:"title,omitempty"`
		Kind   *string         `json:"kind,omitempty"`
		Status json.RawMessage `json:"status"`
	} `json:"updateParams"`
}

type fileToolInput struct {
	Name    fileToolName
	Path    string
	Content string
}

type fileToolObservation struct {
	Phase      fileToolPhase
	ID         string
	Input      fileToolInput
	Title      string
	Descriptor toolDescriptor
	Meta       toolObservationMeta
	Read       *fileReadOutput
	Write      *fileWriteOutput
}

type fileReadOutput struct {
	Content string          `json:"content"`
	Concise string          `json:"content_concise"`
	Path    string          `json:"absolute_path"`
	Offset  json.RawMessage `json:"offset"`
	Raw     string          `json:"raw_output"`
	Lines   uint64          `json:"total_lines"`
}

type fileEditDetail struct {
	Old           string `json:"old_string"`
	OldLine       uint64 `json:"old_line"`
	New           string `json:"new_string"`
	NewLine       uint64 `json:"new_line"`
	ContextBefore string `json:"context_before"`
	ContextAfter  string `json:"context_after"`
	LinePrefix    string `json:"line_prefix"`
}

type fileEdits struct {
	Details []fileEditDetail `json:"details"`
}

type fileWriteOutput struct {
	Old     string    `json:"old_string"`
	New     string    `json:"new_string"`
	Text    string    `json:"tool_output_for_prompt"`
	Concise string    `json:"tool_output_for_prompt_concise"`
	Path    string    `json:"absolute_path"`
	Edits   fileEdits `json:"edits"`
}

type fileToolDelta struct {
	Session domain.ID `json:"sessionId"`
	Update  struct {
		Kind      string        `json:"sessionUpdate"`
		ID        *string       `json:"tool_call_id,omitempty"`
		Index     uint64        `json:"tool_index"`
		Name      *fileToolName `json:"name,omitempty"`
		Arguments string        `json:"arguments_delta"`
	} `json:"update"`
}

func toolText(value string) bool {
	return domain.Text(value, "native file tool content", 256<<10, false) == nil
}

func parseFileToolDelta(raw []byte, session domain.ID) (fileToolDelta, error) {
	var value fileToolDelta
	if session.Validate() != nil || decode(raw, &value) != nil || value.Session != session || value.Update.Kind != "tool_call_delta_chunk" || value.Update.Index >= 128 || !toolText(value.Update.Arguments) || (value.Update.ID == nil) != (value.Update.Name == nil) {
		return value, incompatible()
	}
	if value.Update.ID != nil && (!text(*value.Update.ID, 256) || *value.Update.Name != readFileTool && *value.Update.Name != writeFileTool) {
		return value, incompatible()
	}
	return value, nil
}

func parseFileToolInput(raw []byte, name fileToolName, described bool) (fileToolInput, error) {
	value := fileToolInput{Name: name}
	if len(raw) > 256<<10 {
		return value, incompatible()
	}
	switch name {
	case readFileTool:
		var input struct {
			Variant *string `json:"variant,omitempty"`
			Path    string  `json:"target_file"`
		}
		if decode(raw, &input) != nil || described && (input.Variant == nil || *input.Variant != "ReadFile") || !described && input.Variant != nil {
			return value, incompatible()
		}
		value.Path = input.Path
	case writeFileTool:
		var input struct {
			Variant *string `json:"variant,omitempty"`
			Path    string  `json:"file_path"`
			Content string  `json:"content"`
		}
		if decode(raw, &input) != nil || described && (input.Variant == nil || *input.Variant != "Write") || !described && input.Variant != nil || !toolText(input.Content) {
			return value, incompatible()
		}
		value.Path, value.Content = input.Path, input.Content
	default:
		return value, incompatible()
	}
	if !text(value.Path, 8192) {
		return value, incompatible()
	}
	return value, nil
}

func (d toolDescriptor) validate(input fileToolInput, described bool) bool {
	if d.Version != 1 || d.Name != input.Name || described && (d.Input == nil || d.Input.Path != input.Path) || !described && d.Input != nil {
		return false
	}
	switch input.Name {
	case readFileTool:
		return d.Kind == fileReadKind && d.Namespace == fileGrokNamespace && d.Label == "Read" && d.ReadOnly
	case writeFileTool:
		// The pinned native Write descriptor uses this namespace and kind,
		// independently of its ACP edit kind. Do not normalize either away.
		return d.Kind == fileWriteKind && d.Namespace == fileOpenCodeNamespace && d.Label == "Write" && !d.ReadOnly
	}
	return false
}

func (m toolObservationMeta) validate(session domain.ID, prompt, id string, phase fileToolPhase, name fileToolName) bool {
	if !nativeUUID(prompt, 4) || m.Prompt != prompt || m.Params.ID != id || m.TimestampMS > 253402300799999 || m.StreamMS > 253402300799999 || m.TurnMS > 253402300799999 {
		return false
	}
	if _, err := eventIndex(m.Event, session); err != nil {
		return false
	}
	if phase == fileToolDeclared {
		var status string
		return m.Type == fileCallMetadata && m.Params.Title != nil && *m.Params.Title == string(name) && m.Params.Kind != nil && *m.Params.Kind == "Other" && decode(m.Params.Status, &status) == nil && status == "Pending"
	}
	if m.Type != fileUpdateMetadata || m.Params.Title != nil || m.Params.Kind != nil {
		return false
	}
	if phase == fileToolDescribed {
		return isNull(m.Params.Status)
	}
	var status string
	return phase == fileToolCompleted && decode(m.Params.Status, &status) == nil && status == "Completed"
}

type fileDiff struct {
	Type string     `json:"type"`
	Path string     `json:"path"`
	Old  string     `json:"oldText"`
	New  string     `json:"newText"`
	Meta *fileEdits `json:"_meta,omitempty"`
}

func parseFileToolObservation(raw []byte, session domain.ID, prompt string, original *fileToolInput) (fileToolObservation, error) {
	var envelope struct {
		Session domain.ID           `json:"sessionId"`
		Update  json.RawMessage     `json:"update"`
		Meta    toolObservationMeta `json:"_meta"`
	}
	var value fileToolObservation
	if session.Validate() != nil || decode(raw, &envelope) != nil || envelope.Session != session {
		return value, incompatible()
	}
	value.Meta = envelope.Meta
	var variant struct {
		Kind   string  `json:"sessionUpdate"`
		Status *string `json:"status"`
	}
	if json.Unmarshal(envelope.Update, &variant) != nil {
		return value, incompatible()
	}
	if variant.Kind == "tool_call" {
		var declared struct {
			Kind  string                 `json:"sessionUpdate"`
			ID    string                 `json:"toolCallId"`
			Title string                 `json:"title"`
			Input json.RawMessage        `json:"rawInput"`
			Meta  toolDescriptorEnvelope `json:"_meta"`
		}
		if decode(envelope.Update, &declared) != nil {
			return value, incompatible()
		}
		input, err := parseFileToolInput(declared.Input, declared.Meta.Tool.Name, false)
		if err != nil || declared.Title != string(input.Name) || !declared.Meta.Tool.validate(input, false) {
			return value, incompatible()
		}
		value.Phase, value.ID, value.Input, value.Title, value.Descriptor = fileToolDeclared, declared.ID, input, declared.Title, declared.Meta.Tool
	} else if variant.Kind == "tool_call_update" && variant.Status == nil {
		var details struct {
			Kind      string       `json:"sessionUpdate"`
			ID        string       `json:"toolCallId"`
			Category  fileToolKind `json:"kind"`
			Title     string       `json:"title"`
			Content   []fileDiff   `json:"content,omitempty"`
			Locations []struct {
				Path string `json:"path"`
			} `json:"locations"`
			Input json.RawMessage        `json:"rawInput"`
			Meta  toolDescriptorEnvelope `json:"_meta"`
		}
		if decode(envelope.Update, &details) != nil {
			return value, incompatible()
		}
		input, err := parseFileToolInput(details.Input, details.Meta.Tool.Name, true)
		if err != nil || original == nil || input != *original || !details.Meta.Tool.validate(input, true) || !text(details.Title, 16<<10) || len(details.Locations) != 1 || details.Locations[0].Path != input.Path {
			return value, incompatible()
		}
		if input.Name == readFileTool {
			if details.Category != fileReadKind || details.Content != nil {
				return value, incompatible()
			}
		} else if details.Category != fileEditKind || len(details.Content) != 1 || details.Content[0].Type != "diff" || details.Content[0].Path != input.Path || details.Content[0].Old != "" || details.Content[0].New != input.Content || details.Content[0].Meta != nil {
			return value, incompatible()
		}
		value.Phase, value.ID, value.Input, value.Title, value.Descriptor = fileToolDescribed, details.ID, input, details.Title, details.Meta.Tool
	} else if variant.Kind == "tool_call_update" && variant.Status != nil && *variant.Status == "completed" {
		var complete struct {
			Kind    string          `json:"sessionUpdate"`
			ID      string          `json:"toolCallId"`
			Status  string          `json:"status"`
			Content json.RawMessage `json:"content"`
			Output  json.RawMessage `json:"rawOutput"`
		}
		if original == nil || decode(envelope.Update, &complete) != nil {
			return value, incompatible()
		}
		value.Phase, value.ID, value.Input = fileToolCompleted, complete.ID, *original
		switch original.Name {
		case readFileTool:
			var output struct {
				Type string         `json:"type"`
				File fileReadOutput `json:"FileContent"`
			}
			var content []struct {
				Type    string     `json:"type"`
				Content promptText `json:"content"`
			}
			if decode(complete.Output, &output) != nil || output.Type != "ReadFile" || !isNull(output.File.Offset) || !text(output.File.Path, 8192) || !toolText(output.File.Content) || !toolText(output.File.Concise) || !toolText(output.File.Raw) || decode(complete.Content, &content) != nil || len(content) != 1 || content[0].Type != "content" || content[0].Content.Type != "text" || content[0].Content.Text != output.File.Content {
				return value, incompatible()
			}
			value.Read = &output.File
		case writeFileTool:
			var output struct {
				Type string          `json:"type"`
				File fileWriteOutput `json:"EditsApplied"`
			}
			var content []fileDiff
			if decode(complete.Output, &output) != nil || output.Type != "SearchReplace" || !text(output.File.Path, 8192) || output.File.Path != original.Path || output.File.New != original.Content || !toolText(output.File.Old) || !toolText(output.File.Text) || !toolText(output.File.Concise) || len(output.File.Edits.Details) != 1 || decode(complete.Content, &content) != nil || len(content) != 1 {
				return value, incompatible()
			}
			diff, detail := content[0], output.File.Edits.Details[0]
			if diff.Type != "diff" || diff.Path != output.File.Path || diff.Old != output.File.Old || diff.New != output.File.New || diff.Meta == nil || !reflect.DeepEqual(*diff.Meta, output.File.Edits) || detail.Old != diff.Old || detail.New != diff.New || detail.OldLine != 1 || detail.NewLine != 1 || detail.ContextBefore != "" || detail.ContextAfter != "" || detail.LinePrefix != "" {
				return value, incompatible()
			}
			value.Write = &output.File
		default:
			return value, incompatible()
		}
	} else {
		return value, incompatible()
	}
	if !text(value.ID, 256) || !value.Meta.validate(session, prompt, value.ID, value.Phase, value.Input.Name) {
		return value, incompatible()
	}
	return value, nil
}
