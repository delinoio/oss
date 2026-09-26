package domain

import (
	"encoding/json"
	"reflect"
)

// The pinned schema uses strings, including values outside its suggested
// statuses/priorities. Preserve those values and list order without interpreting
// a completed list as completion of the tool, turn, or execution.
type OpenCodeTodoStatus string
type OpenCodeTodoPriority string

const (
	OpenCodeTodoPending   OpenCodeTodoStatus   = "pending"
	OpenCodeTodoRunning   OpenCodeTodoStatus   = "in_progress"
	OpenCodeTodoCompleted OpenCodeTodoStatus   = "completed"
	OpenCodeTodoCancelled OpenCodeTodoStatus   = "cancelled"
	OpenCodeTodoHigh      OpenCodeTodoPriority = "high"
	OpenCodeTodoMedium    OpenCodeTodoPriority = "medium"
	OpenCodeTodoLow       OpenCodeTodoPriority = "low"
)

type OpenCodeTodo struct {
	Content  string               `json:"content"`
	Status   OpenCodeTodoStatus   `json:"status"`
	Priority OpenCodeTodoPriority `json:"priority"`
}

func (t *OpenCodeTodo) UnmarshalJSON(raw []byte) error {
	var keys map[string]json.RawMessage
	if Decode(raw, &keys) != nil || len(keys) != 3 || keys["content"] == nil || keys["status"] == nil || keys["priority"] == nil {
		return invalidTool()
	}
	var wire struct {
		Content  *string               `json:"content"`
		Status   *OpenCodeTodoStatus   `json:"status"`
		Priority *OpenCodeTodoPriority `json:"priority"`
	}
	if Decode(raw, &wire) != nil || wire.Content == nil || wire.Status == nil || wire.Priority == nil {
		return invalidTool()
	}
	*t = OpenCodeTodo{Content: *wire.Content, Status: *wire.Status, Priority: *wire.Priority}
	return nil
}

func ValidateOpenCodeTodos(todos []OpenCodeTodo) error {
	if todos == nil || len(todos) > MaxArtifactParts {
		return invalidTool()
	}
	for _, todo := range todos {
		for _, value := range []string{todo.Content, string(todo.Status), string(todo.Priority)} {
			if Text(value, "native todo field", MaxMessageText, false) != nil {
				return invalidTool()
			}
		}
	}
	return nil
}

type OpenCodeTodoInput struct {
	Todos []OpenCodeTodo `json:"todos,omitempty"`
}

func (i *OpenCodeTodoInput) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if Decode(raw, &fields) != nil || fields == nil || len(fields) > 1 {
		return invalidTool()
	}
	i.Todos = nil
	if len(fields) == 0 {
		return nil
	}
	todos, ok := fields["todos"]
	if !ok || Decode(todos, &i.Todos) != nil || ValidateOpenCodeTodos(i.Todos) != nil {
		return invalidTool()
	}
	return nil
}

// A nil slice is an absent proposal field; an explicit empty array is a clear.
// Do not use omitempty when writing present empty arrays.
func (i OpenCodeTodoInput) MarshalJSON() ([]byte, error) {
	if i.Todos == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(struct {
		Todos []OpenCodeTodo `json:"todos"`
	}{i.Todos})
}

type OpenCodeTodoMetadata struct {
	Todos       []OpenCodeTodo `json:"todos"`
	Truncated   *bool          `json:"truncated,omitempty"`
	OutputPath  *string        `json:"outputPath,omitempty"`
	Interrupted *bool          `json:"interrupted,omitempty"`
}

// Running/interrupted metadata may omit the list. Preserve that absence and
// an explicit empty result independently in retained publication documents.
func (m OpenCodeTodoMetadata) MarshalJSON() ([]byte, error) {
	var todos *[]OpenCodeTodo
	if m.Todos != nil {
		todos = &m.Todos
	}
	return json.Marshal(struct {
		Todos       *[]OpenCodeTodo `json:"todos,omitempty"`
		Truncated   *bool           `json:"truncated,omitempty"`
		OutputPath  *string         `json:"outputPath,omitempty"`
		Interrupted *bool           `json:"interrupted,omitempty"`
	}{todos, m.Truncated, m.OutputPath, m.Interrupted})
}

func (m *OpenCodeTodoMetadata) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Todos       json.RawMessage `json:"todos"`
		Truncated   *bool           `json:"truncated,omitempty"`
		OutputPath  *string         `json:"outputPath,omitempty"`
		Interrupted *bool           `json:"interrupted,omitempty"`
	}
	if Decode(raw, &wire) != nil {
		return invalidTool()
	}
	*m = OpenCodeTodoMetadata{Truncated: wire.Truncated, OutputPath: wire.OutputPath, Interrupted: wire.Interrupted}
	if wire.Todos != nil && (Decode(wire.Todos, &m.Todos) != nil || ValidateOpenCodeTodos(m.Todos) != nil) {
		return invalidTool()
	}
	return nil
}

type OpenCodeTodoObservation struct {
	CallID           string                `json:"call_id"`
	Input            OpenCodeTodoInput     `json:"input"`
	Raw              *string               `json:"raw,omitempty"`
	Title            *string               `json:"title,omitempty"`
	Output           *string               `json:"output,omitempty"`
	Error            *string               `json:"error,omitempty"`
	Metadata         *OpenCodeTodoMetadata `json:"metadata,omitempty"`
	Timing           *OpenCodeToolTiming   `json:"time,omitempty"`
	ProviderExecuted *bool                 `json:"provider_executed,omitempty"`
}

func (r OpenCodeTodoObservation) Validate(status ToolStatus) error {
	if Text(r.CallID, "native todo call", 1024, true) != nil || r.ProviderExecuted != nil && *r.ProviderExecuted {
		return invalidTool()
	}
	for _, v := range []*string{r.Raw, r.Title, r.Output, r.Error} {
		if v != nil && Text(*v, "native todo content", MaxMessageText, false) != nil {
			return invalidTool()
		}
	}
	if r.Input.Todos != nil && ValidateOpenCodeTodos(r.Input.Todos) != nil {
		return invalidTool()
	}
	if t := r.Timing; t != nil && (t.Start > maxNativeExactInteger || t.End != nil && (*t.End < t.Start || *t.End > maxNativeExactInteger)) {
		return invalidTool()
	}
	if m := r.Metadata; m != nil {
		if m.Todos != nil && ValidateOpenCodeTodos(m.Todos) != nil || m.OutputPath != nil && (m.Truncated == nil || !*m.Truncated || Text(*m.OutputPath, "native todo output path", MaxMessageText, false) != nil) {
			return invalidTool()
		}
	}
	switch status {
	case ToolPending:
		if r.Raw == nil || r.Title != nil || r.Output != nil || r.Error != nil || r.Timing != nil || r.Metadata != nil {
			return invalidTool()
		}
	case ToolRunning:
		if r.Raw != nil || r.Input.Todos == nil || r.Timing == nil || r.Timing.End != nil || r.Output != nil || r.Error != nil {
			return invalidTool()
		}
	case ToolCompleted:
		if r.Raw != nil || r.Input.Todos == nil || r.Timing == nil || r.Timing.End == nil || r.Title == nil || r.Output == nil || r.Error != nil || r.Metadata == nil || r.Metadata.Truncated == nil || !reflect.DeepEqual(r.Input.Todos, r.Metadata.Todos) {
			return invalidTool()
		}
	case ToolFailed:
		if r.Raw != nil || r.Timing == nil || r.Timing.End == nil || r.Title != nil || r.Output != nil || r.Error == nil {
			return invalidTool()
		}
	default:
		return invalidTool()
	}
	return nil
}

func ValidateOpenCodeTodoTransition(prior, next ToolSnapshot) error {
	if prior.Kind != OpenCodeTodoTool || next.Kind != prior.Kind || prior.Validate() != nil || next.Validate() != nil || prior.Todo.CallID != next.Todo.CallID || !reflect.DeepEqual(prior.Todo.ProviderExecuted, next.Todo.ProviderExecuted) {
		return invalidTool()
	}
	if prior.Status == ToolPending {
		if next.Status == ToolCompleted {
			return invalidTool()
		}
	} else if prior.Status != ToolRunning || next.Status == ToolPending || !reflect.DeepEqual(prior.Todo.Input, next.Todo.Input) || prior.Todo.Timing.Start != next.Todo.Timing.Start {
		return invalidTool()
	}
	return nil
}

// This session-level event has no native message/part/call owner. Keep its
// original event identity for deduplication without fabricating one.
type OpenCodeTodoProgress struct {
	NativeEventID string         `json:"native_event_id"`
	Todos         []OpenCodeTodo `json:"todos"`
}
