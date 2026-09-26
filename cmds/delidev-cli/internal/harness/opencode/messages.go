package opencode

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type MessageRole string

const (
	UserMessageRole      MessageRole = "user"
	AssistantMessageRole MessageRole = "assistant"
)

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishContentFilter FinishReason = "content-filter"
	FinishToolCalls     FinishReason = "tool-calls"
	FinishError         FinishReason = "error"
	FinishUnknown       FinishReason = "unknown"
)

type NativeErrorKind string

const (
	ProviderAuthErrorKind NativeErrorKind = "ProviderAuthError"
	UnknownErrorKind      NativeErrorKind = "UnknownError"
	OutputLengthErrorKind NativeErrorKind = "MessageOutputLengthError"
	AbortedErrorKind      NativeErrorKind = "MessageAbortedError"
	StructuredErrorKind   NativeErrorKind = "StructuredOutputError"
	ContextErrorKind      NativeErrorKind = "ContextOverflowError"
	ContentErrorKind      NativeErrorKind = "ContentFilterError"
	APIErrorKind          NativeErrorKind = "APIError"
)

// NativeError retains classification only. Native messages, response bodies,
// headers and metadata may contain paths or credentials and are never copied
// into this diagnostic surface. Retryable is an observation, not retry authority.
type NativeError struct {
	Kind       NativeErrorKind
	StatusCode *uint64
	Retryable  *bool
	Retries    *uint64
	Provider   string `json:"-"`
}

// NativeUsage is one reported snapshot. Missing total remains missing. Cache,
// reasoning, step, message and session observations can overlap; this decoder
// never sums them, invents zero for unknown fields or establishes billable cost.
type NativeUsage struct {
	Input      uint64
	Output     uint64
	Reasoning  uint64
	CacheRead  uint64
	CacheWrite uint64
	Total      *uint64
}

type NativeMessage struct {
	ID        string
	SessionID string
	Role      MessageRole
	Created   int64
	User      *NativeUserMessage      `json:"-"`
	Assistant *NativeAssistantMessage `json:"-"`
}

type NativeUserMessage struct {
	Agent    string
	Provider string
	Model    string
	Variant  *string
	System   *string         `json:"-"`
	Tools    map[string]bool `json:"-"`
	Format   json.RawMessage `json:"-"`
	Summary  json.RawMessage `json:"-"`
}

type NativeAssistantMessage struct {
	ParentID   string
	Provider   string
	Model      string
	Mode       string
	Agent      string
	Variant    *string
	Completed  *int64
	Finish     *FinishReason
	Summary    *bool
	Usage      NativeUsage
	Cost       json.Number
	Error      *NativeError
	Cwd        string          `json:"-"`
	Root       string          `json:"-"`
	Structured json.RawMessage `json:"-"`
}

func messageProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode native message does not match its typed profile.", "Retain original native state and do not infer input completion from an unhandled message.")
}

func countPointer(raw json.RawMessage) (*uint64, bool) {
	count, ok := nativeCount(raw)
	if !ok {
		return nil, false
	}
	value := uint64(count)
	return &value, true
}

func boolPointer(raw json.RawMessage) (*bool, bool) {
	var value *bool
	err := json.Unmarshal(raw, &value)
	return value, err == nil && value != nil
}

func textPointer(raw json.RawMessage, limit int, required bool) (*string, bool) {
	value, ok := boundedString(raw, limit, required)
	return &value, ok
}

func decodeUsage(raw json.RawMessage) (NativeUsage, error) {
	fields, err := shape(raw, []string{"input", "output", "reasoning", "cache"}, []string{"total"})
	if err != nil {
		return NativeUsage{}, messageProblem()
	}
	var usage NativeUsage
	for key, destination := range map[string]*uint64{"input": &usage.Input, "output": &usage.Output, "reasoning": &usage.Reasoning} {
		count, ok := nativeCount(fields[key])
		if !ok {
			return NativeUsage{}, messageProblem()
		}
		*destination = uint64(count)
	}
	cache, err := shape(fields["cache"], []string{"read", "write"}, nil)
	if err != nil {
		return NativeUsage{}, messageProblem()
	}
	for key, destination := range map[string]*uint64{"read": &usage.CacheRead, "write": &usage.CacheWrite} {
		count, ok := nativeCount(cache[key])
		if !ok {
			return NativeUsage{}, messageProblem()
		}
		*destination = uint64(count)
	}
	if value, exists := fields["total"]; exists {
		total, ok := countPointer(value)
		if !ok {
			return NativeUsage{}, messageProblem()
		}
		usage.Total = total
	}
	return usage, nil
}

func decodeNativeError(raw json.RawMessage) (*NativeError, error) {
	fields, err := shape(raw, []string{"name", "data"}, nil)
	if err != nil {
		return nil, messageProblem()
	}
	name, ok := boundedString(fields["name"], 128, true)
	if !ok {
		return nil, messageProblem()
	}
	result := &NativeError{Kind: NativeErrorKind(name)}
	var required, optional []string
	switch result.Kind {
	case ProviderAuthErrorKind:
		required = []string{"providerID", "message"}
	case UnknownErrorKind:
		required, optional = []string{"message"}, []string{"ref"}
	case OutputLengthErrorKind:
	case AbortedErrorKind, ContentErrorKind:
		required = []string{"message"}
	case StructuredErrorKind:
		required = []string{"message", "retries"}
	case ContextErrorKind:
		required, optional = []string{"message"}, []string{"responseBody"}
	case APIErrorKind:
		required, optional = []string{"message", "isRetryable"}, []string{"statusCode", "responseHeaders", "responseBody", "metadata"}
	default:
		return nil, messageProblem()
	}
	data, err := shape(fields["data"], required, optional)
	if err != nil {
		return nil, messageProblem()
	}
	for key, value := range data {
		switch key {
		case "message", "ref", "responseBody":
			if _, ok := boundedString(value, maxHTTPBody, false); !ok {
				return nil, messageProblem()
			}
		case "providerID":
			result.Provider, ok = boundedString(value, 256, true)
			if !ok {
				return nil, messageProblem()
			}
		case "isRetryable":
			result.Retryable, ok = boolPointer(value)
			if !ok {
				return nil, messageProblem()
			}
		case "statusCode", "retries":
			count, ok := countPointer(value)
			if !ok {
				return nil, messageProblem()
			}
			if key == "statusCode" {
				result.StatusCode = count
			} else {
				result.Retries = count
			}
		case "responseHeaders", "metadata":
			entries, err := object(value)
			if err != nil || len(entries) > 256 {
				return nil, messageProblem()
			}
			for key, entry := range entries {
				if domain.Text(key, "native error key", 4096, true) != nil {
					return nil, messageProblem()
				}
				if _, ok := boundedString(entry, maxHTTPBody, false); !ok {
					return nil, messageProblem()
				}
			}
		}
	}
	return result, nil
}

func decodeNativeMessage(raw []byte) (NativeMessage, error) {
	bad := func() (NativeMessage, error) { return NativeMessage{}, messageProblem() }
	fields, err := object(raw)
	if err != nil {
		return bad()
	}
	role, ok := boundedString(fields["role"], 16, true)
	if !ok {
		return bad()
	}
	required := []string{"id", "sessionID", "role", "time", "agent"}
	var optional []string
	switch MessageRole(role) {
	case UserMessageRole:
		required = append(required, "model")
		optional = []string{"system", "tools", "format", "summary"}
	case AssistantMessageRole:
		required = append(required, "parentID", "modelID", "providerID", "mode", "path", "cost", "tokens")
		optional = []string{"error", "summary", "structured", "variant", "finish"}
	default:
		return bad()
	}
	for key, value := range fields {
		// Structured output is native unknown JSON and may explicitly be null.
		// It remains opaque/private and cannot imply successful validation.
		if !slices.Contains(required, key) && !slices.Contains(optional, key) || string(value) == "null" && key != "structured" {
			return bad()
		}
	}
	for _, key := range required {
		if _, exists := fields[key]; !exists {
			return bad()
		}
	}
	id, ok := boundedString(fields["id"], 30, true)
	session, valid := boundedString(fields["sessionID"], 30, true)
	if !ok || !valid || !nativeID(id, "msg") || !nativeID(session, "ses") {
		return bad()
	}
	optionalTimes := []string(nil)
	if MessageRole(role) == AssistantMessageRole {
		optionalTimes = []string{"completed"}
	}
	times, err := shape(fields["time"], []string{"created"}, optionalTimes)
	if err != nil {
		return bad()
	}
	created, ok := nativeCount(times["created"])
	agent, valid := boundedString(fields["agent"], 128, true)
	if !ok || created == 0 || !valid {
		return bad()
	}
	message := NativeMessage{ID: id, SessionID: session, Role: MessageRole(role), Created: created}
	if message.Role == UserMessageRole {
		user, err := decodeUser(fields, agent)
		if err != nil {
			return bad()
		}
		message.User = user
		return message, nil
	}
	assistant := &NativeAssistantMessage{Agent: agent}
	message.Assistant = assistant
	for key, destination := range map[string]*string{"parentID": &assistant.ParentID, "modelID": &assistant.Model, "providerID": &assistant.Provider, "mode": &assistant.Mode} {
		value, ok := boundedString(fields[key], 256, true)
		if !ok {
			return bad()
		}
		*destination = value
	}
	if !nativeID(assistant.ParentID, "msg") || assistant.ParentID == id {
		return bad()
	}
	path, err := shape(fields["path"], []string{"cwd", "root"}, nil)
	if err != nil {
		return bad()
	}
	assistant.Cwd, ok = boundedString(path["cwd"], 32768, true)
	assistant.Root, valid = boundedString(path["root"], 32768, true)
	if !ok || !valid || !nonnegativeDecimal(fields["cost"]) {
		return bad()
	}
	assistant.Cost = json.Number(string(fields["cost"]))
	assistant.Usage, err = decodeUsage(fields["tokens"])
	if err != nil {
		return bad()
	}
	if raw, exists := times["completed"]; exists {
		completed, ok := nativeCount(raw)
		if !ok || completed < created {
			return bad()
		}
		assistant.Completed = &completed
	}
	if raw, exists := fields["finish"]; exists {
		assistant.Finish, ok = nativeFinish(raw)
		if !ok {
			return bad()
		}
	}
	if raw, exists := fields["summary"]; exists {
		assistant.Summary, ok = boolPointer(raw)
		if !ok {
			return bad()
		}
	}
	if raw, exists := fields["variant"]; exists {
		assistant.Variant, ok = textPointer(raw, 256, true)
		if !ok {
			return bad()
		}
	}
	if raw, exists := fields["error"]; exists {
		assistant.Error, err = decodeNativeError(raw)
		if err != nil {
			return bad()
		}
	}
	assistant.Structured = slices.Clone(fields["structured"])
	return message, nil
}

func decodeUser(fields map[string]json.RawMessage, agent string) (*NativeUserMessage, error) {
	model, err := shape(fields["model"], []string{"providerID", "modelID"}, []string{"variant"})
	if err != nil {
		return nil, messageProblem()
	}
	user := &NativeUserMessage{Agent: agent}
	var ok, valid bool
	user.Provider, ok = boundedString(model["providerID"], 256, true)
	user.Model, valid = boundedString(model["modelID"], 256, true)
	if !ok || !valid {
		return nil, messageProblem()
	}
	if raw, exists := model["variant"]; exists {
		user.Variant, ok = textPointer(raw, 256, true)
		if !ok {
			return nil, messageProblem()
		}
	}
	if raw, exists := fields["system"]; exists {
		user.System, ok = textPointer(raw, 256<<10, false)
		if !ok {
			return nil, messageProblem()
		}
	}
	if raw, exists := fields["tools"]; exists {
		tools, err := object(raw)
		if err != nil || len(tools) > 1024 {
			return nil, messageProblem()
		}
		user.Tools = map[string]bool{}
		for name, raw := range tools {
			value, ok := boolPointer(raw)
			if domain.Text(name, "native tool", 256, true) != nil || !ok {
				return nil, messageProblem()
			}
			user.Tools[name] = *value
		}
	}
	if raw, exists := fields["format"]; exists {
		format, err := shape(raw, []string{"type"}, []string{"schema", "retryCount"})
		if err != nil {
			return nil, messageProblem()
		}
		switch {
		case scalar(format["type"], "text"):
			if len(format) != 1 {
				return nil, messageProblem()
			}
		case scalar(format["type"], "json_schema"):
			if _, err := object(format["schema"]); err != nil {
				return nil, messageProblem()
			}
			if raw, exists := format["retryCount"]; exists {
				if _, ok := nativeCount(raw); !ok {
					return nil, messageProblem()
				}
			}
		default:
			return nil, messageProblem()
		}
		user.Format = slices.Clone(raw)
	}
	if raw, exists := fields["summary"]; exists {
		summary, err := shape(raw, []string{"diffs"}, []string{"title", "body"})
		if err != nil || !validateDiffs(summary["diffs"]) {
			return nil, messageProblem()
		}
		for _, key := range []string{"title", "body"} {
			if raw, exists := summary[key]; exists {
				if _, ok := boundedString(raw, maxHTTPBody, false); !ok {
					return nil, messageProblem()
				}
			}
		}
		user.Summary = slices.Clone(raw)
	}
	return user, nil
}
