package opencode

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type PartKind string

const (
	TextPartKind       PartKind = "text"
	ReasoningPartKind  PartKind = "reasoning"
	ToolPartKind       PartKind = "tool"
	FilePartKind       PartKind = "file"
	StepStartPartKind  PartKind = "step-start"
	StepFinishPartKind PartKind = "step-finish"
	SnapshotPartKind   PartKind = "snapshot"
	PatchPartKind      PartKind = "patch"
	AgentPartKind      PartKind = "agent"
	RetryPartKind      PartKind = "retry"
	CompactionPartKind PartKind = "compaction"
	SubtaskPartKind    PartKind = "subtask"
)

type ToolState string

const (
	ToolPending   ToolState = "pending"
	ToolRunning   ToolState = "running"
	ToolCompleted ToolState = "completed"
	ToolError     ToolState = "error"
)

// NativePart is a typed private observation. Parsing a file/reference/snapshot
// never opens it, and parsing a tool result never executes it or grants approval.
// Ordered lifecycle and original call ownership are separate validation steps.
type NativePart struct {
	ID         string
	SessionID  string
	MessageID  string
	Kind       PartKind
	Text       *NativeTextPart       `json:"-"`
	Tool       *NativeToolPart       `json:"-"`
	File       *NativeFilePart       `json:"-"`
	Step       *NativeStepPart       `json:"-"`
	Snapshot   *string               `json:"-"`
	Patch      *NativePatchPart      `json:"-"`
	Agent      *NativeAgentPart      `json:"-"`
	Retry      *NativeRetryPart      `json:"-"`
	Compaction *NativeCompactionPart `json:"-"`
	Subtask    *NativeSubtaskPart    `json:"-"`
}

type NativeTiming struct {
	Start     uint64
	End       *uint64
	Compacted *uint64
}

type NativeTextPart struct {
	Text      string `json:"-"`
	Timing    *NativeTiming
	Synthetic *bool
	Ignored   *bool
	Metadata  json.RawMessage `json:"-"`
}

type NativeToolPart struct {
	CallID       string
	Name         string
	State        ToolState
	Input        json.RawMessage `json:"-"`
	Raw          *string         `json:"-"`
	Title        *string         `json:"-"`
	Output       *string         `json:"-"`
	Error        *string         `json:"-"`
	Metadata     json.RawMessage `json:"-"`
	PartMetadata json.RawMessage `json:"-"`
	Timing       *NativeTiming
	Attachments  []NativePart `json:"-"`
}

type NativeFilePart struct {
	MIME     string
	Filename *string         `json:"-"`
	URL      string          `json:"-"`
	Source   json.RawMessage `json:"-"`
}

type NativeStepPart struct {
	Reason   *FinishReason
	Snapshot *string `json:"-"`
	Usage    *NativeUsage
	Cost     *json.Number
}

type NativePatchPart struct {
	Hash  string   `json:"-"`
	Files []string `json:"-"`
}

type NativeAgentPart struct {
	Name   string
	Source json.RawMessage `json:"-"`
}

type NativeRetryPart struct {
	Attempt uint64
	Created uint64
	Error   *NativeError
}

type NativeCompactionPart struct {
	Auto      bool
	Overflow  *bool
	TailStart *string
}

type NativeSubtaskPart struct {
	Prompt      string `json:"-"`
	Description string `json:"-"`
	Agent       string
	Model       *inputModel
	Command     *string `json:"-"`
}

func partProblem() *domain.Error {
	return domain.Fail(domain.Unsupported, "OpenCode native part does not match its typed profile.", "Retain original native content and reconcile its owned message/tool lifecycle before publication.")
}

func nativeFinish(raw json.RawMessage) (*FinishReason, bool) {
	value, ok := boundedString(raw, 32, true)
	finish := FinishReason(value)
	return &finish, ok && slices.Contains([]FinishReason{FinishStop, FinishLength, FinishContentFilter, FinishToolCalls, FinishError, FinishUnknown}, finish)
}

func decodeTiming(raw json.RawMessage, complete, compacted bool) (*NativeTiming, error) {
	required, optional := []string{"start"}, []string{}
	if complete {
		required = append(required, "end")
	} else {
		optional = append(optional, "end")
	}
	if compacted {
		optional = append(optional, "compacted")
	}
	fields, err := shape(raw, required, optional)
	if err != nil {
		return nil, partProblem()
	}
	start, ok := countPointer(fields["start"])
	if !ok {
		return nil, partProblem()
	}
	timing := &NativeTiming{Start: *start}
	if raw, exists := fields["end"]; exists {
		timing.End, ok = countPointer(raw)
		if !ok || *timing.End < timing.Start {
			return nil, partProblem()
		}
	}
	if raw, exists := fields["compacted"]; exists {
		timing.Compacted, ok = countPointer(raw)
		if !ok || timing.End == nil || *timing.Compacted < *timing.End {
			return nil, partProblem()
		}
	}
	return timing, nil
}

func privateObject(raw json.RawMessage) (json.RawMessage, bool) {
	_, err := object(raw)
	return slices.Clone(raw), err == nil
}

func decodeNativePart(raw []byte) (NativePart, error) {
	bad := func() (NativePart, error) { return NativePart{}, partProblem() }
	fields, err := object(raw)
	if err != nil {
		return bad()
	}
	typeName, ok := boundedString(fields["type"], 32, true)
	if !ok {
		return bad()
	}
	kind := PartKind(typeName)
	required := []string{"id", "sessionID", "messageID", "type"}
	var optional []string
	switch kind {
	case TextPartKind:
		required, optional = append(required, "text"), []string{"synthetic", "ignored", "time", "metadata"}
	case ReasoningPartKind:
		required, optional = append(required, "text", "time"), []string{"metadata"}
	case ToolPartKind:
		required, optional = append(required, "callID", "tool", "state"), []string{"metadata"}
	case FilePartKind:
		required, optional = append(required, "mime", "url"), []string{"filename", "source"}
	case StepStartPartKind:
		optional = []string{"snapshot"}
	case StepFinishPartKind:
		required, optional = append(required, "reason", "cost", "tokens"), []string{"snapshot"}
	case SnapshotPartKind:
		required = append(required, "snapshot")
	case PatchPartKind:
		required = append(required, "hash", "files")
	case AgentPartKind:
		required, optional = append(required, "name"), []string{"source"}
	case RetryPartKind:
		required = append(required, "attempt", "error", "time")
	case CompactionPartKind:
		required, optional = append(required, "auto"), []string{"overflow", "tail_start_id"}
	case SubtaskPartKind:
		required, optional = append(required, "prompt", "description", "agent"), []string{"model", "command"}
	default:
		return bad()
	}
	fields, err = shape(raw, required, optional)
	if err != nil {
		return bad()
	}
	part := NativePart{Kind: kind}
	for key, destination := range map[string]*string{"id": &part.ID, "sessionID": &part.SessionID, "messageID": &part.MessageID} {
		*destination, ok = boundedString(fields[key], 30, true)
		if !ok {
			return bad()
		}
	}
	if !nativeID(part.ID, "prt") || !nativeID(part.SessionID, "ses") || !nativeID(part.MessageID, "msg") {
		return bad()
	}
	switch kind {
	case TextPartKind, ReasoningPartKind:
		text := &NativeTextPart{}
		text.Text, ok = boundedString(fields["text"], maxHTTPBody, false)
		if !ok {
			return bad()
		}
		if raw, exists := fields["time"]; exists {
			text.Timing, err = decodeTiming(raw, false, false)
			if err != nil {
				return bad()
			}
		}
		for key, destination := range map[string]**bool{"synthetic": &text.Synthetic, "ignored": &text.Ignored} {
			if raw, exists := fields[key]; exists {
				*destination, ok = boolPointer(raw)
				if !ok {
					return bad()
				}
			}
		}
		if raw, exists := fields["metadata"]; exists {
			text.Metadata, ok = privateObject(raw)
			if !ok {
				return bad()
			}
		}
		part.Text = text
	case ToolPartKind:
		part.Tool, err = decodeTool(fields, part)
		if err != nil {
			return bad()
		}
	case FilePartKind:
		file := &NativeFilePart{}
		file.MIME, ok = boundedString(fields["mime"], 256, true)
		if !ok {
			return bad()
		}
		file.URL, ok = boundedString(fields["url"], maxHTTPBody, true)
		if !ok {
			return bad()
		}
		if raw, exists := fields["filename"]; exists {
			file.Filename, ok = textPointer(raw, 32768, false)
			if !ok {
				return bad()
			}
		}
		if raw, exists := fields["source"]; exists {
			if !validFileSource(raw) {
				return bad()
			}
			file.Source = slices.Clone(raw)
		}
		part.File = file
	case StepStartPartKind, StepFinishPartKind:
		step := &NativeStepPart{}
		if raw, exists := fields["snapshot"]; exists {
			step.Snapshot, ok = textPointer(raw, 4096, true)
			if !ok {
				return bad()
			}
		}
		if kind == StepFinishPartKind {
			step.Reason, ok = nativeFinish(fields["reason"])
			if !ok || !nonnegativeDecimal(fields["cost"]) {
				return bad()
			}
			cost := json.Number(string(fields["cost"]))
			step.Cost = &cost
			usage, err := decodeUsage(fields["tokens"])
			if err != nil {
				return bad()
			}
			step.Usage = &usage
		}
		part.Step = step
	case SnapshotPartKind:
		part.Snapshot, ok = textPointer(fields["snapshot"], 4096, true)
		if !ok {
			return bad()
		}
	case PatchPartKind:
		patch := &NativePatchPart{}
		patch.Hash, ok = boundedString(fields["hash"], 4096, true)
		if !ok || json.Unmarshal(fields["files"], &patch.Files) != nil || patch.Files == nil || len(patch.Files) > 4096 {
			return bad()
		}
		for _, file := range patch.Files {
			if domain.Text(file, "native file reference", 32768, true) != nil {
				return bad()
			}
		}
		part.Patch = patch
	case AgentPartKind:
		agent := &NativeAgentPart{}
		agent.Name, ok = boundedString(fields["name"], 128, true)
		if !ok {
			return bad()
		}
		if raw, exists := fields["source"]; exists {
			if !validSourceText(raw) {
				return bad()
			}
			agent.Source = slices.Clone(raw)
		}
		part.Agent = agent
	case RetryPartKind:
		attempt, ok := countPointer(fields["attempt"])
		times, err := shape(fields["time"], []string{"created"}, nil)
		if !ok || err != nil {
			return bad()
		}
		created, ok := countPointer(times["created"])
		problem, err := decodeNativeError(fields["error"])
		if !ok || err != nil || problem.Kind != APIErrorKind {
			return bad()
		}
		part.Retry = &NativeRetryPart{Attempt: *attempt, Created: *created, Error: problem}
	case CompactionPartKind:
		auto, ok := boolPointer(fields["auto"])
		if !ok {
			return bad()
		}
		compaction := &NativeCompactionPart{Auto: *auto}
		if raw, exists := fields["overflow"]; exists {
			compaction.Overflow, ok = boolPointer(raw)
			if !ok {
				return bad()
			}
		}
		if raw, exists := fields["tail_start_id"]; exists {
			compaction.TailStart, ok = textPointer(raw, 30, true)
			if !ok || !nativeID(*compaction.TailStart, "msg") {
				return bad()
			}
		}
		part.Compaction = compaction
	case SubtaskPartKind:
		subtask := &NativeSubtaskPart{}
		for key, destination := range map[string]*string{"prompt": &subtask.Prompt, "description": &subtask.Description, "agent": &subtask.Agent} {
			limit := maxHTTPBody
			if key == "agent" {
				limit = 128
			}
			*destination, ok = boundedString(fields[key], limit, key == "agent")
			if !ok {
				return bad()
			}
		}
		if raw, exists := fields["model"]; exists {
			model, err := shape(raw, []string{"providerID", "modelID"}, nil)
			if err != nil {
				return bad()
			}
			provider, ok := boundedString(model["providerID"], 256, true)
			name, valid := boundedString(model["modelID"], 256, true)
			if !ok || !valid {
				return bad()
			}
			subtask.Model = &inputModel{Provider: provider, Model: name}
		}
		if raw, exists := fields["command"]; exists {
			subtask.Command, ok = textPointer(raw, maxHTTPBody, false)
			if !ok {
				return bad()
			}
		}
		part.Subtask = subtask
	}
	return part, nil
}

func decodeTool(fields map[string]json.RawMessage, owner NativePart) (*NativeToolPart, error) {
	tool := &NativeToolPart{}
	var ok bool
	tool.CallID, ok = boundedString(fields["callID"], 1024, true)
	if !ok {
		return nil, partProblem()
	}
	tool.Name, ok = boundedString(fields["tool"], 256, true)
	if !ok {
		return nil, partProblem()
	}
	if raw, exists := fields["metadata"]; exists {
		tool.PartMetadata, ok = privateObject(raw)
		if !ok {
			return nil, partProblem()
		}
	}
	state, err := object(fields["state"])
	if err != nil {
		return nil, partProblem()
	}
	name, ok := boundedString(state["status"], 16, true)
	if !ok {
		return nil, partProblem()
	}
	tool.State = ToolState(name)
	required := []string{"status", "input"}
	var optional []string
	switch tool.State {
	case ToolPending:
		required = append(required, "raw")
	case ToolRunning:
		required, optional = append(required, "time"), []string{"title", "metadata"}
	case ToolCompleted:
		required, optional = append(required, "time", "output", "title", "metadata"), []string{"attachments"}
	case ToolError:
		required, optional = append(required, "time", "error"), []string{"metadata"}
	default:
		return nil, partProblem()
	}
	state, err = shape(fields["state"], required, optional)
	if err != nil {
		return nil, partProblem()
	}
	tool.Input, ok = privateObject(state["input"])
	if !ok {
		return nil, partProblem()
	}
	for key, destination := range map[string]**string{"raw": &tool.Raw, "title": &tool.Title, "output": &tool.Output, "error": &tool.Error} {
		if raw, exists := state[key]; exists {
			*destination, ok = textPointer(raw, maxHTTPBody, false)
			if !ok {
				return nil, partProblem()
			}
		}
	}
	if raw, exists := state["metadata"]; exists {
		tool.Metadata, ok = privateObject(raw)
		if !ok {
			return nil, partProblem()
		}
	}
	if raw, exists := state["time"]; exists {
		tool.Timing, err = decodeTiming(raw, tool.State == ToolCompleted || tool.State == ToolError, tool.State == ToolCompleted)
		if err != nil || tool.State == ToolRunning && tool.Timing.End != nil {
			return nil, partProblem()
		}
	}
	if raw, exists := state["attachments"]; exists {
		var attachments []json.RawMessage
		if json.Unmarshal(raw, &attachments) != nil || attachments == nil || len(attachments) > 128 {
			return nil, partProblem()
		}
		tool.Attachments = make([]NativePart, 0, len(attachments))
		seen := map[string]bool{owner.ID: true}
		for _, raw := range attachments {
			// Inspect type before recursion: attachments may contain files only,
			// never nested tools with their own recursively expanding attachments.
			fields, err := object(raw)
			if err != nil || !scalar(fields["type"], string(FilePartKind)) {
				return nil, partProblem()
			}
			part, err := decodeNativePart(raw)
			if err != nil || part.SessionID != owner.SessionID || part.MessageID != owner.MessageID || seen[part.ID] {
				return nil, partProblem()
			}
			seen[part.ID] = true
			tool.Attachments = append(tool.Attachments, part)
		}
	}
	return tool, nil
}

func validSourceText(raw json.RawMessage) bool {
	fields, err := shape(raw, []string{"value", "start", "end"}, nil)
	if err != nil {
		return false
	}
	_, valid := boundedString(fields["value"], maxHTTPBody, false)
	start, ok := nativeCount(fields["start"])
	end, last := nativeCount(fields["end"])
	// Native offsets belong to their original source encoding; do not reinterpret
	// them as Go byte indices or slice the text using an invented character unit.
	return valid && ok && last && end >= start
}

func validFileSource(raw json.RawMessage) bool {
	fields, err := object(raw)
	if err != nil {
		return false
	}
	var required []string
	switch {
	case scalar(fields["type"], "file"):
		required = []string{"type", "text", "path"}
	case scalar(fields["type"], "symbol"):
		required = []string{"type", "text", "path", "range", "name", "kind"}
	case scalar(fields["type"], "resource"):
		required = []string{"type", "text", "clientName", "uri"}
	default:
		return false
	}
	fields, err = shape(raw, required, nil)
	if err != nil || !validSourceText(fields["text"]) {
		return false
	}
	for _, key := range []string{"path", "name", "clientName", "uri"} {
		if raw, exists := fields[key]; exists {
			if _, ok := boundedString(raw, 32768, true); !ok {
				return false
			}
		}
	}
	if raw, exists := fields["kind"]; exists {
		if _, ok := nativeCount(raw); !ok {
			return false
		}
	}
	if raw, exists := fields["range"]; exists {
		positions, err := shape(raw, []string{"start", "end"}, nil)
		if err != nil {
			return false
		}
		var coordinates [2][2]int64
		for i, key := range []string{"start", "end"} {
			position, err := shape(positions[key], []string{"line", "character"}, nil)
			if err != nil {
				return false
			}
			for j, key := range []string{"line", "character"} {
				count, ok := nativeCount(position[key])
				if !ok {
					return false
				}
				coordinates[i][j] = count
			}
		}
		if coordinates[1][0] < coordinates[0][0] || coordinates[1][0] == coordinates[0][0] && coordinates[1][1] < coordinates[0][1] {
			return false
		}
	}
	return true
}
