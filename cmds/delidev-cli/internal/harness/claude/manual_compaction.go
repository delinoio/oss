package claude

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type CompactionCommandKind string

const (
	CompactionCommandEcho       CompactionCommandKind = "command-echo"
	CompactionCommandOutput     CompactionCommandKind = "command-output"
	CompactionCommandDiagnostic CompactionCommandKind = "command-diagnostic"
)

type NativeCompactionCommand struct {
	Kind       CompactionCommandKind
	Text       string                      `json:"-"`
	Diagnostic *NativeCompactionDiagnostic `json:"-"`
}

type NativeCompactionDiagnostic struct {
	MessageID string
	Blocks    []NativeContentBlock
	Usage     *ProviderUsage
}

// A local-command result has no native user_message_uuid or terminal_reason.
// Keep that original result shape separate from conversation turn outcomes.
type NativeCompactionResult struct {
	Kind   ResultKind
	Error  bool
	Usage  *ResultUsage
	Status CompactResult
	Text   string `json:"-"`
}

type manualCompactionBinding struct {
	core             *ExecutionBinding
	started          bool
	status           CompactResult
	boundary         bool
	summary          bool
	echo             bool
	output           bool
	diagnostic       bool
	diagnosticDigest [sha256.Size]byte
	result           bool
	settled          bool
	problem          *domain.Error
}

func (b *manualCompactionBinding) observe(event StreamEvent) (observed LifecycleObservation, returned error) {
	if b.problem != nil {
		return LifecycleObservation{}, b.problem
	}
	phase := envelopeValidation
	defer func() {
		if returned != nil {
			b.problem = lifecycleUncertain()
			returned = b.problem
			if b.core.logger != nil {
				b.core.logger.Warn("Claude Code manual compaction requires reconciliation", "owner_id", b.core.owner, "action_id", b.core.input, "phase", phase, "code", b.problem.Code)
			}
		}
	}()
	var header struct {
		Type    string    `json:"type"`
		Subtype string    `json:"subtype"`
		Session domain.ID `json:"session_id"`
		ID      string    `json:"uuid"`
		State   string    `json:"state"`
	}
	var fields map[string]json.RawMessage
	if b.settled || event.Kind != NativeMessage || domain.Decode(event.Body, &fields) != nil || json.Unmarshal(event.Body, &header) != nil || header.Type != event.Type || header.Session != b.core.session || !nativeUUID(header.ID) || b.core.seen[header.ID] || len(b.core.seen) >= domain.MaxExecutionEvents {
		return LifecycleObservation{}, lifecycleUncertain()
	}
	observed = LifecycleObservation{SessionID: b.core.session, NativeID: header.ID, TurnID: b.core.turnID, Native: &event}
	delegated := false
	switch event.Type {
	case "command_lifecycle":
		phase = commandValidation
		if header.State == string(CommandCompleted) && !b.result {
			return LifecycleObservation{}, lifecycleUncertain()
		}
		if header.State != string(CommandQueued) && header.State != string(CommandStarted) && header.State != string(CommandCompleted) {
			return LifecycleObservation{}, lifecycleUncertain()
		}
		delegated = true
	case "system":
		switch header.Subtype {
		case "status":
			phase = progressValidation
			value, err := decodeSessionProgress(event.Body)
			if err != nil || b.core.command != CommandStarted || b.core.initialized || value.Permission != nil {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			if value.Status != nil {
				if *value.Status != SessionCompacting || b.started || value.CompactResult != nil || value.CompactError != nil {
					return LifecycleObservation{}, lifecycleUncertain()
				}
				b.started = true
			} else {
				if !b.started || b.status != "" || value.CompactResult == nil || (*value.CompactResult == CompactSucceeded && value.CompactError != nil) {
					return LifecycleObservation{}, lifecycleUncertain()
				}
				b.status = *value.CompactResult
			}
			observed.Kind, observed.Progress = ProgressObserved, value
		case "init":
			phase = initValidation
			if !b.started || b.status == "" || b.core.initialized {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			delegated = true
		case "compact_boundary":
			phase = contentValidation
			if b.boundary || b.status != CompactSucceeded {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			delegated = true
		case "session_state_changed":
			phase = runValidation
			if header.State != string(RunRunning) && header.State != string(RunIdle) {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			if header.State == string(RunIdle) && (!b.result || b.core.command != CommandCompleted) {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			delegated = true
		default:
			return LifecycleObservation{}, lifecycleUncertain()
		}
	case "user":
		phase = replayValidation
		if b.core.pendingCompaction != nil {
			if header.ID != b.core.pendingCompaction.anchor {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			delegated = true
		} else {
			value, err := b.observeCommandReplay(event.Body)
			if err != nil {
				return LifecycleObservation{}, err
			}
			observed.Kind, observed.CompactCommand = CompactionCommandObserved, value
		}
	case "assistant":
		phase = contentValidation
		value, err := b.observeDiagnostic(event.Body)
		if err != nil {
			return LifecycleObservation{}, err
		}
		observed.Kind, observed.CompactCommand = CompactionCommandObserved, &NativeCompactionCommand{Kind: CompactionCommandDiagnostic, Diagnostic: value}
	case "result":
		phase = resultValidation
		value, err := b.observeResult(event.Body)
		if err != nil {
			return LifecycleObservation{}, err
		}
		observed.Kind, observed.CompactResult = CompactionResultObserved, value
	default:
		return LifecycleObservation{}, lifecycleUncertain()
	}
	if delegated {
		value, err := b.core.Observe(event)
		if err != nil {
			return LifecycleObservation{}, err
		}
		observed = value
		if event.Type == "user" && observed.Kind != CompactionSummaryObserved {
			return LifecycleObservation{}, lifecycleUncertain()
		}
		switch observed.Kind {
		case CompactionObserved:
			if observed.Compaction.Trigger != ManualCompaction || b.core.pendingCompaction == nil {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			b.boundary = true
		case CompactionSummaryObserved:
			if !b.boundary || b.summary {
				return LifecycleObservation{}, lifecycleUncertain()
			}
			b.summary = true
		case RunStateObserved:
			b.settled = observed.Run.State == RunIdle
		}
	} else {
		b.core.seen[header.ID] = true
	}
	observed.ActionID, observed.InputID, observed.Accepted = b.core.input, "", false
	return observed, nil
}

func (b *manualCompactionBinding) observeCommandReplay(raw json.RawMessage) (*NativeCompactionCommand, error) {
	var envelope struct {
		Type      string          `json:"type"`
		Session   domain.ID       `json:"session_id"`
		ID        string          `json:"uuid"`
		Parent    json.RawMessage `json:"parent_tool_use_id"`
		Timestamp string          `json:"timestamp"`
		Replay    bool            `json:"isReplay"`
		Message   json.RawMessage `json:"message"`
	}
	var message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if !b.core.initialized || b.result || b.core.command != CommandStarted || (b.status == CompactSucceeded && !b.summary) || (b.status == CompactFailed && !b.diagnostic) || decodeNativeObject(raw, &envelope) != nil || !envelope.Replay || !bytes.Equal(bytes.TrimSpace(envelope.Parent), []byte("null")) || decodeNativeObject(envelope.Message, &message) != nil || message.Role != "user" || domain.Text(message.Content, "native command replay", domain.MaxMessageText, true) != nil {
		return nil, lifecycleUncertain()
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err != nil {
		return nil, lifecycleUncertain()
	}
	value := &NativeCompactionCommand{Text: message.Content}
	if envelope.ID == string(b.core.input) {
		parts, err := nativeCommandElements(message.Content, []string{"command-name", "command-message", "command-args"})
		if b.echo || err != nil || parts[0] != "/compact" || parts[1] != "compact" || parts[2] != "" {
			return nil, lifecycleUncertain()
		}
		b.echo, value.Kind = true, CompactionCommandEcho
	} else {
		if b.output || b.status != CompactSucceeded {
			return nil, lifecycleUncertain()
		}
		if _, err := nativeCommandElements(message.Content, []string{"local-command-stdout"}); err != nil {
			return nil, err
		}
		b.output, value.Kind = true, CompactionCommandOutput
	}
	return value, nil
}

// These are structured native stream message fragments, not rendered terminal
// output. Validate the closed XML vocabulary without deriving success from its
// human-readable text. Native status/boundary/result/lifecycle prove outcome.
func nativeCommandElements(text string, names []string) ([]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(text))
	values := make([]string, 0, len(names))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, lifecycleUncertain()
		}
		switch v := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(v)) != "" {
				return nil, lifecycleUncertain()
			}
		case xml.StartElement:
			if len(values) >= len(names) || v.Name.Space != "" || v.Name.Local != names[len(values)] || len(v.Attr) != 0 {
				return nil, lifecycleUncertain()
			}
			var value strings.Builder
			for {
				next, err := decoder.Token()
				if err != nil {
					return nil, lifecycleUncertain()
				}
				if chars, ok := next.(xml.CharData); ok {
					value.Write(chars)
					continue
				}
				if end, ok := next.(xml.EndElement); !ok || end.Name != v.Name {
					return nil, lifecycleUncertain()
				}
				break
			}
			values = append(values, value.String())
		default:
			return nil, lifecycleUncertain()
		}
	}
	if len(values) != len(names) {
		return nil, lifecycleUncertain()
	}
	return values, nil
}

func (b *manualCompactionBinding) observeResult(raw json.RawMessage) (*NativeCompactionResult, error) {
	var result struct {
		Type           string            `json:"type"`
		Session        domain.ID         `json:"session_id"`
		ID             string            `json:"uuid"`
		Kind           ResultKind        `json:"subtype"`
		Error          *bool             `json:"is_error"`
		Duration       *uint64           `json:"duration_ms"`
		APIDuration    *uint64           `json:"duration_api_ms"`
		Turns          *uint64           `json:"num_turns"`
		Stop           json.RawMessage   `json:"stop_reason"`
		Text           *string           `json:"result"`
		Cost           json.RawMessage   `json:"total_cost_usd"`
		Usage          json.RawMessage   `json:"usage"`
		Models         json.RawMessage   `json:"modelUsage"`
		Denials        []json.RawMessage `json:"permission_denials"`
		FastMode       string            `json:"fast_mode_state"`
		FastModeReason string            `json:"fast_mode_disabled_reason"`
	}
	success := b.status == CompactSucceeded && b.output && b.boundary && b.summary && !b.diagnostic
	failed := b.status == CompactFailed && b.diagnostic && !b.output && !b.boundary && !b.summary
	if b.result || !b.core.initialized || b.core.pendingCompaction != nil || !b.echo || (!success && !failed) || decodeNativeObject(raw, &result) != nil || result.Kind != ResultSuccess || result.Error == nil || *result.Error || result.Duration == nil || result.APIDuration == nil || *result.APIDuration != 0 || result.Turns == nil || *result.Turns != 0 || !bytes.Equal(bytes.TrimSpace(result.Stop), []byte("null")) || result.Text == nil || domain.Text(*result.Text, "native compaction result", domain.MaxMessageText, false) != nil || (success && *result.Text != "") || (failed && sha256.Sum256([]byte(*result.Text)) != b.diagnosticDigest) || result.Denials == nil || len(result.Denials) != 0 || result.FastMode != "off" || result.FastModeReason != "sdk_opt_in_required" {
		return nil, lifecycleUncertain()
	}
	usage, err := decodeResultUsage(result.Usage, result.Models, result.Cost)
	if err != nil {
		return nil, err
	}
	b.result, b.core.finished = true, true
	return &NativeCompactionResult{Kind: result.Kind, Error: *result.Error, Usage: usage, Status: b.status, Text: *result.Text}, nil
}

func (b *manualCompactionBinding) observeDiagnostic(raw json.RawMessage) (*NativeCompactionDiagnostic, error) {
	var envelope struct {
		Type      string          `json:"type"`
		ID        string          `json:"uuid"`
		Session   domain.ID       `json:"session_id"`
		Parent    json.RawMessage `json:"parent_tool_use_id"`
		Timestamp string          `json:"timestamp"`
		Message   json.RawMessage `json:"message"`
	}
	if !b.core.initialized || b.result || b.diagnostic || b.status != CompactFailed || b.boundary || b.summary || decodeNativeObject(raw, &envelope) != nil || !bytes.Equal(bytes.TrimSpace(envelope.Parent), []byte("null")) {
		return nil, lifecycleUncertain()
	}
	if _, err := time.Parse(time.RFC3339Nano, envelope.Timestamp); err != nil {
		return nil, lifecycleUncertain()
	}
	message, err := decodeProviderMessage(envelope.Message)
	if err != nil || message.Model != "<synthetic>" || !nativeUUID(message.ID) || len(message.Content) != 1 {
		return nil, lifecycleUncertain()
	}
	value := &NativeCompactionDiagnostic{MessageID: message.ID, Usage: message.Usage}
	for _, raw := range message.Content {
		block, err := decodeContentBlock(raw)
		if err != nil || block.Kind != TextBlock {
			return nil, lifecycleUncertain()
		}
		value.Blocks = append(value.Blocks, block)
		b.diagnosticDigest = sha256.Sum256([]byte(*block.Text))
	}
	b.diagnostic = true
	return value, nil
}
