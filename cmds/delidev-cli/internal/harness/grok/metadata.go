package grok

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type metadataKind string

const (
	commandMetadata  metadataKind = "commands"
	titleMetadata    metadataKind = "title"
	summaryMetadata  metadataKind = "summary"
	responseMetadata metadataKind = "response"
	retryMetadata    metadataKind = "retry"
	lastTurnMetadata metadataKind = "last-turn-summary"
)

type responseUsage struct {
	Input         uint64 `json:"input_tokens"`
	Output        uint64 `json:"output_tokens"`
	CachedRead    uint64 `json:"cache_read_input_tokens"`
	CacheCreation uint64 `json:"cache_creation_input_tokens"`
	Reasoning     uint64 `json:"reasoning_tokens"`
}

type passiveObservation struct {
	kind  metadataKind
	retry *RetryObservation
	text  string
	usage responseUsage
	event string
}

// parsePassiveObservation validates known original metadata without granting
// any advertised command/tool, rewriting product settings or inventing usage.
// Native workflow paths and command descriptions remain private and discarded.
func parsePassiveObservation(raw []byte, method string, session domain.ID, prompt string) (passiveObservation, error) {
	var envelope struct {
		Session domain.ID       `json:"sessionId"`
		Update  json.RawMessage `json:"update"`
		Meta    json.RawMessage `json:"_meta,omitempty"`
	}
	if session.Validate() != nil || decode(raw, &envelope) != nil || envelope.Session != session {
		return passiveObservation{}, incompatible()
	}
	var variant struct {
		Kind string `json:"sessionUpdate"`
	}
	if json.Unmarshal(envelope.Update, &variant) != nil {
		return passiveObservation{}, incompatible()
	}
	var observed passiveObservation
	switch variant.Kind {
	case "available_commands_update":
		if method != "session/update" {
			return observed, incompatible()
		}
		var commands struct {
			Kind     string `json:"sessionUpdate"`
			Commands []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				Input       json.RawMessage `json:"input"`
				Meta        json.RawMessage `json:"_meta,omitempty"`
			} `json:"availableCommands"`
			Meta struct {
				Tools []string `json:"tools"`
			} `json:"_meta"`
		}
		if decode(envelope.Update, &commands) != nil {
			return observed, incompatible()
		}
		names := []string{"compact", "always-approve", "context", "session-info", "feedback", "deep-research", "workflow", "goal", "loop"}
		if len(commands.Commands) != len(names) || len(commands.Meta.Tools) > 128 {
			return observed, incompatible()
		}
		seen := map[string]bool{}
		for _, command := range commands.Commands {
			if !slices.Contains(names, command.Name) || seen[command.Name] || !text(command.Description, 16<<10) {
				return observed, incompatible()
			}
			seen[command.Name] = true
			if !isNull(command.Input) {
				var input struct {
					Hint string `json:"hint"`
				}
				if decode(command.Input, &input) != nil || !text(input.Hint, 4096) {
					return observed, incompatible()
				}
			}
			if command.Name == "deep-research" {
				var meta struct {
					Source string `json:"workflowSource"`
					Path   string `json:"workflowPath"`
				}
				if decode(command.Meta, &meta) != nil || meta.Source != "builtin" || !text(meta.Path, 8192) {
					return observed, incompatible()
				}
			} else if len(command.Meta) != 0 {
				return observed, incompatible()
			}
		}
		seen = map[string]bool{}
		for _, tool := range commands.Meta.Tools {
			if !text(tool, 256) || seen[tool] {
				return observed, incompatible()
			}
			seen[tool] = true
		}
		var meta struct {
			Context   uint64 `json:"totalTokens"`
			Event     string `json:"eventId"`
			Timestamp uint64 `json:"agentTimestampMs"`
			Type      string `json:"updateType"`
			Params    struct {
				Count uint32 `json:"commandsCount"`
			} `json:"updateParams"`
			Prompt json.RawMessage `json:"promptId,omitempty"`
			Stream json.RawMessage `json:"streamStartMs,omitempty"`
			Turn   json.RawMessage `json:"turnStartMs,omitempty"`
		}
		if decode(envelope.Meta, &meta) != nil || meta.Type != "AvailableCommandsUpdate" || meta.Params.Count != uint32(len(commands.Commands)) || meta.Timestamp > 253402300799999 {
			return observed, incompatible()
		}
		if _, err := eventIndex(meta.Event, session); err != nil {
			return observed, err
		}
		if len(meta.Prompt) != 0 || len(meta.Stream) != 0 || len(meta.Turn) != 0 {
			var native string
			var stream, turn uint64
			if !nativeUUID(prompt, 4) || decode(meta.Prompt, &native) != nil || native != prompt || decode(meta.Stream, &stream) != nil || decode(meta.Turn, &turn) != nil || stream > 253402300799999 || turn > 253402300799999 {
				return observed, incompatible()
			}
		}
		observed.kind, observed.event = commandMetadata, meta.Event
	case "session_info_update":
		var value struct {
			Kind  string `json:"sessionUpdate"`
			Title string `json:"title"`
		}
		if method != "session/update" || len(envelope.Meta) != 0 || decode(envelope.Update, &value) != nil || !text(value.Title, 4096) {
			return observed, incompatible()
		}
		observed.kind, observed.text = titleMetadata, value.Title
	case "session_summary_generated":
		var value struct {
			Kind    string `json:"sessionUpdate"`
			Summary string `json:"session_summary"`
		}
		if method != "_x.ai/session_notification" || len(envelope.Meta) != 0 || decode(envelope.Update, &value) != nil || !text(value.Summary, 256<<10) {
			return observed, incompatible()
		}
		observed.kind, observed.text = summaryMetadata, value.Summary
	case "response_completed":
		var value struct {
			Kind  string        `json:"sessionUpdate"`
			Usage responseUsage `json:"usage"`
		}
		if method != "_x.ai/session_notification" || len(envelope.Meta) != 0 || decode(envelope.Update, &value) != nil {
			return observed, incompatible()
		}
		observed.kind, observed.usage = responseMetadata, value.Usage
	case "retry_state":
		if method != "_x.ai/session_notification" {
			return observed, incompatible()
		}
		retry, err := parseRetry(raw, session)
		if err != nil {
			return observed, err
		}
		observed.kind, observed.event, observed.retry = retryMetadata, retry.Event, &retry
	case "last_turn_summary":
		var value struct {
			Kind    string `json:"sessionUpdate"`
			Summary string `json:"summary"`
			Prompt  string `json:"prompt_id"`
		}
		var meta struct {
			Timestamp uint64 `json:"agentTimestampMs"`
		}
		if method != "_x.ai/session_notification" || !nativeUUID(prompt, 4) || decode(envelope.Update, &value) != nil || value.Prompt != prompt || !text(value.Summary, 256<<10) || decode(envelope.Meta, &meta) != nil || meta.Timestamp > 253402300799999 {
			return observed, incompatible()
		}
		observed.kind, observed.text = lastTurnMetadata, value.Summary
	default:
		return observed, incompatible()
	}
	return observed, nil
}

type activity string

const (
	workingActivity activity = "working"
	idleActivity    activity = "idle"
)

func parseActivity(raw []byte, session domain.ID, workspace string) (activity, error) {
	return parseProfileActivity(raw, session, workspace, apiProfile{})
}

func parseProfileActivity(raw []byte, session domain.ID, workspace string, profile apiProfile) (activity, error) {
	var value struct {
		Upserted []struct {
			Session   domain.ID       `json:"sessionId"`
			Title     json.RawMessage `json:"title"`
			Cwd       string          `json:"cwd"`
			Worktree  bool            `json:"isWorktree"`
			Model     string          `json:"modelId"`
			Yolo      bool            `json:"yolo"`
			Activity  activity        `json:"activity"`
			Resident  bool            `json:"resident"`
			Timestamp uint64          `json:"lastChangeUnixMs"`
			Origin    struct {
				Kind string `json:"kind"`
			} `json:"origin"`
		} `json:"upserted"`
		Removed json.RawMessage `json:"removed"`
	}
	if session.Validate() != nil || decode(raw, &value) != nil || len(value.Upserted) != 1 || !emptyArray(value.Removed) {
		return "", incompatible()
	}
	entry := value.Upserted[0]
	if entry.Session != session || entry.Cwd != workspace || entry.Worktree || entry.Model != profile.selector() || entry.Yolo || !entry.Resident || entry.Timestamp > 253402300799999 || entry.Origin.Kind != "local" || (entry.Activity != workingActivity && entry.Activity != idleActivity) {
		return "", incompatible()
	}
	if !isNull(entry.Title) {
		var title string
		if decode(entry.Title, &title) != nil || !text(title, 4096) {
			return "", incompatible()
		}
	}
	return entry.Activity, nil
}
