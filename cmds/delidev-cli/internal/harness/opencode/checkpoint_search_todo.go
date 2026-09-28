package opencode

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const (
	checkpointGlobTool checkpointToolName = "glob"
	checkpointGrepTool checkpointToolName = "grep"
	checkpointTodoTool checkpointToolName = "todowrite"
)

// Original event provenance and list digest remain separate from tool owners.
// No list is rebuilt from tool output or republished during restoration.
type TodoHistoryObservation struct {
	EventID string
	Digest  string
}

func checkpointSearchOrTodo(name checkpointToolName) bool {
	return name == checkpointGlobTool || name == checkpointGrepTool || name == checkpointTodoTool
}

func checkpointSearchMetadata(raw json.RawMessage, name checkpointToolName) bool {
	field := "count"
	if name == checkpointGrepTool {
		field = "matches"
	}
	fields, err := shape(raw, []string{field, "truncated"}, nil)
	if err != nil {
		return false
	}
	count, valid := nativeCount(fields[field])
	var truncated *bool
	// These native search tools produce inline results even when truncated;
	// unlike the generic wrapper they never retain an output-file reference.
	return valid && (name != checkpointGlobTool || count <= 100) && json.Unmarshal(fields["truncated"], &truncated) == nil && truncated != nil
}

func checkpointInlineTodo(tool *NativeToolPart) bool {
	var input domain.OpenCodeTodoInput
	var metadata struct {
		Todos     []domain.OpenCodeTodo `json:"todos"`
		Truncated *bool                 `json:"truncated"`
	}
	return domain.Decode(tool.Input, &input) == nil && domain.ValidateOpenCodeTodos(input.Todos) == nil && domain.Decode(tool.Metadata, &metadata) == nil && domain.ValidateOpenCodeTodos(metadata.Todos) == nil && metadata.Truncated != nil && !*metadata.Truncated && slices.Equal(input.Todos, metadata.Todos)
}

func latestCheckpointTodo(value nativeCheckpoint) *TodoHistoryObservation {
	var latest *TodoHistoryObservation
	for _, history := range checkpointHistories(value) {
		if history.Todo != nil {
			latest = history.Todo
		}
	}
	return latest
}

// Caller holds the session gate and the original observer lock (when present).
// This closed read can compare an observed list, never grant update authority.
func (s *sessionAPI) compareTodoHistory(ctx context.Context, expected *TodoHistoryObservation) error {
	if expected == nil {
		return nil
	}
	if s.todoRead || s.creation == nil || !nativeID(expected.EventID, "evt") || !checkpointDigest(expected.Digest) {
		return sessionUncertain()
	}
	s.todoRead = true
	defer func() { s.todoRead = false }()
	raw, _, err := s.request(ctx, http.MethodGet, "/session/"+s.creation.identity.id+"/todo", nil, http.StatusOK)
	var todos []domain.OpenCodeTodo
	if err != nil || domain.Decode(raw, &todos) != nil || domain.ValidateOpenCodeTodos(todos) != nil || mutationDigest(canonicalNative(raw)) != expected.Digest {
		return sessionUncertain()
	}
	return nil
}
