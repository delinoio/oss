package grok

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// TextHistory is a content-free comparison of the closed first text turn. It
// grants no process adoption, continuation, public completion or replay rights.
// Native instructions/tools/context still require their own continuation proof.
type TextHistory struct {
	User            domain.GrokUserHistory `json:"user"`
	InputID         domain.ID              `json:"input_id"`
	ClosureID       domain.ID              `json:"closure_id"`
	NativeSessionID domain.ID              `json:"native_session_id"`
	NativePromptID  string                 `json:"native_prompt_id"`
	FilesDigest     string                 `json:"files_digest"`
	TextChunks      uint64                 `json:"text_chunks"`
}

type historyStage string

const (
	historyOwnership historyStage = "ownership"
	historyFilesRead historyStage = "files"
	historyUpdates   historyStage = "updates"
	historyChat      historyStage = "chat"
	historyUsage     historyStage = "usage"
	historySummary   historyStage = "summary"
	historyRecheck   historyStage = "recheck"
)

func historyValueDigest(value any) [32]byte {
	raw, _ := json.Marshal(value)
	// Normalize validated RawMessage map order/whitespace without converting
	// native uint64 counters to lossy float64 values.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	_ = decoder.Decode(&normalized)
	raw, _ = json.Marshal(normalized)
	return sha256.Sum256(raw)
}

func historyTextEnvelopeDigest(chunk TextChunk) [32]byte {
	chunk.Update.Content.Text = ""
	return historyValueDigest(chunk)
}

func historyLines(raw []byte, count int) ([][]byte, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' || bytes.Contains(raw, []byte("\r")) {
		return nil, historyUncertain()
	}
	// A newline-only file must not allocate one slice per byte before the
	// expected record count rejects it.
	rows := bytes.SplitN(raw[:len(raw)-1], []byte("\n"), count+1)
	if len(rows) != count {
		return nil, historyUncertain()
	}
	for _, row := range rows {
		if len(row) == 0 || len(row) > 8<<20 {
			return nil, historyUncertain()
		}
	}
	return rows, nil
}

type storedUser struct {
	Session domain.ID `json:"sessionId"`
	Update  struct {
		Kind    string     `json:"sessionUpdate"`
		Content promptText `json:"content"`
		Meta    struct {
			Model string  `json:"modelId"`
			Index *uint64 `json:"promptIndex"`
		} `json:"_meta"`
	} `json:"update"`
	Meta struct {
		Event       string  `json:"eventId"`
		TimestampMS *uint64 `json:"agentTimestampMs"`
	} `json:"_meta"`
}

func verifyTextUpdates(raw []byte, session domain.ID, model string, completed *completedText) (string, domain.GrokUserHistory, error) {
	// The pinned native writer coalesces all text into one retained chunk,
	// preserving the last live chunk's metadata. Individual live chunks remain
	// separate observations; persisted text cannot reconstruct their boundaries.
	rows, err := historyLines(raw, 3)
	if err != nil {
		return "", domain.GrokUserHistory{}, err
	}
	var input string
	var observed domain.GrokUserHistory
	var last uint64
	for index, raw := range rows {
		var row struct {
			Timestamp uint64          `json:"timestamp"`
			Method    string          `json:"method"`
			Params    json.RawMessage `json:"params"`
		}
		if decode(raw, &row) != nil || row.Timestamp > 253402300799 {
			return "", domain.GrokUserHistory{}, historyUncertain()
		}
		var event string
		var timestamp uint64
		switch {
		case index == 0:
			var user storedUser
			if decode(row.Params, &user) != nil || row.Method != "session/update" || user.Session != session || user.Update.Kind != "user_message_chunk" || user.Update.Content.Type != "text" || user.Update.Meta.Model != model || (user.Update.Meta.Index == nil || *user.Update.Meta.Index != 0) || user.Meta.TimestampMS == nil {
				return "", domain.GrokUserHistory{}, historyUncertain()
			}
			input = user.Update.Content.Text
			digest, err := TextInputClaimDigest(session, input)
			if err != nil || digest != completed.bodyDigest {
				return "", domain.GrokUserHistory{}, historyUncertain()
			}
			event, timestamp = user.Meta.Event, *user.Meta.TimestampMS
			observed = domain.GrokUserHistory{Source: domain.GrokClosedFirstText, NativeEventID: event, TimestampMS: strconv.FormatUint(timestamp, 10), PromptIndex: "0", Model: model, InputDigest: domain.GrokUserInputDigest(input)}
		case index == len(rows)-1:
			turn, err := parseTurnCompleted(row.Params, session, completed.prompt, model)
			if err != nil || row.Method != "_x.ai/session/update" || historyValueDigest(turn) != completed.terminal {
				return "", domain.GrokUserHistory{}, historyUncertain()
			}
			event, timestamp = turn.Meta.Event, turn.Meta.TimestampMS
		default:
			chunk, err := parseTextChunkBound(row.Params, session, completed.prompt, 4<<20)
			if err != nil || row.Method != "session/update" || historyTextEnvelopeDigest(chunk) != completed.lastChunk || sha256.Sum256([]byte(chunk.Update.Content.Text)) != completed.output {
				return "", domain.GrokUserHistory{}, historyUncertain()
			}
			event, timestamp = chunk.Meta.Event, chunk.Meta.TimestampMS
		}
		current, err := eventIndex(event, session)
		if err != nil || index > 0 && current <= last || timestamp > 253402300799999 {
			return "", domain.GrokUserHistory{}, historyUncertain()
		}
		last = current
	}
	first, e1 := eventIndex(completed.firstTextEvent, session)
	user, e2 := eventIndex(observed.NativeEventID, session)
	if e1 != nil || e2 != nil || user >= first {
		return "", domain.GrokUserHistory{}, historyUncertain()
	}
	return input, observed, nil
}

func verifyTextChat(raw, system []byte, input, model string, instructions instructionProfile, completed *completedText) error {
	rows, err := historyLines(raw, 5)
	if err != nil {
		return err
	}
	var first struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	if decode(rows[0], &first) != nil || first.Type != "system" || !text(first.Content, 1<<20) || first.Content != string(system) {
		return historyUncertain()
	}
	for index := 1; index < 4; index++ {
		var row struct {
			Type    string       `json:"type"`
			Content []promptText `json:"content"`
			Reason  *string      `json:"synthetic_reason,omitempty"`
			Index   *uint64      `json:"prompt_index,omitempty"`
		}
		if decode(rows[index], &row) != nil || row.Type != "user" || len(row.Content) != 1 || row.Content[0].Type != "text" || !text(row.Content[0].Text, 1<<20) {
			return historyUncertain()
		}
		switch index {
		case 1:
			if row.Reason != nil || row.Index != nil || !instructions.verifyContext(row.Content[0].Text) {
				return historyUncertain()
			}
		case 2:
			if (row.Reason == nil || *row.Reason != "system_reminder") || row.Index != nil {
				return historyUncertain()
			}
		case 3:
			if row.Reason != nil || row.Index == nil || *row.Index != 0 || row.Content[0].Text != "<user_query>\n"+input+"\n</user_query>" {
				return historyUncertain()
			}
		}
	}
	var assistant struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Model   string `json:"model_id"`
	}
	if decode(rows[4], &assistant) != nil || assistant.Type != "assistant" || assistant.Model != model || domain.Text(assistant.Content, "native retained text", 4<<20, false) != nil || sha256.Sum256([]byte(assistant.Content)) != completed.output {
		return historyUncertain()
	}
	return nil
}

type storedCounters struct {
	Input         uint64 `json:"inputTokens"`
	Output        uint64 `json:"outputTokens"`
	CachedRead    uint64 `json:"cachedReadTokens"`
	CacheCreation uint64 `json:"cacheCreationTokens"`
	Reasoning     uint64 `json:"reasoningTokens"`
	Total         uint64 `json:"totalTokens"`
	Calls         uint64 `json:"modelCalls"`
}

type storedUsage struct {
	Input         uint64          `json:"inputTokens"`
	Output        uint64          `json:"outputTokens"`
	CachedRead    uint64          `json:"cachedReadTokens"`
	CacheCreation uint64          `json:"cacheCreationTokens"`
	Reasoning     uint64          `json:"reasoningTokens"`
	Total         uint64          `json:"totalTokens"`
	Calls         uint64          `json:"modelCalls"`
	Turns         uint64          `json:"turnCount"`
	Model         string          `json:"primaryModelId"`
	Models        json.RawMessage `json:"modelUsage"`
	Number        *uint64         `json:"turnNumber,omitempty"`
	Ended         *string         `json:"endedAt,omitempty"`
}

func historyTime(value string) bool {
	stamp, err := time.Parse(time.RFC3339Nano, value)
	_, offset := stamp.Zone()
	return err == nil && offset == 0 && stamp.Year() >= 1970 && stamp.Year() <= 9999
}

func verifyTextUsage(raw []byte, session domain.ID, model string, observed ModelUsage) error {
	var file struct {
		Session domain.ID     `json:"sessionId"`
		Updated string        `json:"updatedAt"`
		Usage   storedUsage   `json:"session"`
		Turns   []storedUsage `json:"turns"`
	}
	if decode(raw, &file) != nil || file.Session != session || !historyTime(file.Updated) || len(file.Turns) != 1 {
		return historyUncertain()
	}
	expected := storedCounters{observed.Input, observed.Output, observed.CachedRead, observed.CacheCreation, observed.Reasoning, observed.Total, observed.Calls}
	for index, usage := range []storedUsage{file.Usage, file.Turns[0]} {
		counters := storedCounters{usage.Input, usage.Output, usage.CachedRead, usage.CacheCreation, usage.Reasoning, usage.Total, usage.Calls}
		var models map[string]json.RawMessage
		if counters != expected || usage.Turns != 1 || usage.Model != model || json.Unmarshal(usage.Models, &models) != nil || len(models) != 1 {
			return historyUncertain()
		}
		var selected storedCounters
		if decode(models[model], &selected) != nil || selected != expected {
			return historyUncertain()
		}
		if index == 0 && (usage.Number != nil || usage.Ended != nil) || index == 1 && (usage.Number == nil || *usage.Number != 1 || (usage.Ended == nil || *usage.Ended != file.Updated)) {
			return historyUncertain()
		}
	}
	return nil
}

type textSummary struct {
	Info struct {
		Session domain.ID `json:"id"`
		Cwd     string    `json:"cwd"`
	} `json:"info"`
	Agent        string `json:"agent_id"`
	Attempt      string `json:"attempt_id"`
	Summary      string `json:"session_summary"`
	Created      string `json:"created_at"`
	Updated      string `json:"updated_at"`
	Messages     uint64 `json:"num_messages"`
	ChatMessages uint64 `json:"num_chat_messages"`
	Model        string `json:"current_model_id"`
	NextTurn     uint64 `json:"next_trace_turn"`
	Format       uint64 `json:"chat_format_version"`
	Request      string `json:"request_id"`
	Home         string `json:"grok_home"`
	Active       string `json:"last_active_at"`
	Title        string `json:"generated_title"`
	AgentName    string `json:"agent_name"`
	Sandbox      string `json:"sandbox_profile"`
	LastSummary  string `json:"last_turn_summary"`
	LastPrompt   string `json:"last_turn_summary_prompt_id"`
}

func nativeTraceID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	// Native ag1/at1 values format a UUID-v4 as an unpadded lowercase u128.
	// A leading zero nibble is absent, so a valid native ID can have 31 (or
	// fewer) digits. Reconstruct only for validation; never replace its spelling.
	if len(suffix) == 0 || len(suffix) > 32 || suffix[0] == '0' {
		return false
	}
	padded := strings.Repeat("0", 32-len(suffix)) + suffix
	return nativeUUID(padded[:8]+"-"+padded[8:12]+"-"+padded[12:16]+"-"+padded[16:20]+"-"+padded[20:], 4)
}

func verifyTextSummary(raw []byte, home, workspace string, session domain.ID, completed *completedText) error {
	var value textSummary
	if decode(raw, &value) != nil || value.Info.Session != session || value.Info.Cwd != workspace || value.Home != home || value.Model != "delidev-selected" || value.Request != completed.prompt || value.LastPrompt != completed.prompt || value.LastSummary != completed.summary || value.AgentName != "grok-build-plan" || value.Sandbox != "off" || value.Messages != 3 || value.ChatMessages != 5 || value.NextTurn != 1 || value.Format != 1 || !nativeTraceID(value.Agent, "ag1.") || !nativeTraceID(value.Attempt, "at1.") || !text(value.Summary, 64<<10) || !text(value.Title, 64<<10) || !historyTime(value.Created) || !historyTime(value.Updated) || !historyTime(value.Active) {
		return historyUncertain()
	}
	return nil
}

// VerifyClosedText reads only the original process-owned history after successful
// native closure and joined cleanup. No caller-supplied closure can authorize it.
func (a *OwnedAPI) VerifyClosedText(ctx context.Context) (TextHistory, error) {
	if a == nil || a.connection == nil {
		return TextHistory{}, apiConfigurationError()
	}
	return a.connection.verifyClosedText(ctx)
}

func (a *apiConnection) verifyClosedText(ctx context.Context) (result TextHistory, returned error) {
	stage := historyOwnership
	defer func() {
		if returned != nil && a.inspection.Logger != nil {
			a.inspection.Logger.WarnContext(ctx, "Grok Build closed text history verification failed", "owner_id", a.inspection.OwnerID, "stage", stage, "code", domain.SafeError(returned).Code)
		}
	}()
	select {
	case a.gate <- struct{}{}:
		defer func() { <-a.gate }()
	case <-ctx.Done():
		return result, historyUncertain()
	}
	completed := a.completedText
	if completed == nil || completed.closed.Validate() != nil || len(completed.chunks) == 0 || a.profile.instructions.check() != nil {
		return result, historyUncertain()
	}
	home := filepath.Dir(a.profile.path)
	scope, err := openHistoryFiles(ctx, home, a.inspection.Logger)
	if err != nil {
		return result, err
	}
	defer scope.Close()
	if !sameHistoryFile(completed.home, scope.entries["."]) {
		return result, historyUncertain()
	}
	base, err := historySessionPath(a.workspace, a.session)
	if err != nil {
		return result, err
	}
	names := []string{"updates.jsonl", "chat_history.jsonl", "usage.json", "summary.json", "system_prompt.txt"}
	limits := []int64{maxHistoryFileBytes, 8 << 20, 64 << 10, 256 << 10, 1 << 20}
	files := make([][]byte, len(names))
	hashes := make([][32]byte, len(names))
	stage = historyFilesRead
	for i, name := range names {
		files[i], err = scope.read(ctx, filepath.Join(base, name), limits[i])
		if err != nil {
			return result, err
		}
		hashes[i] = sha256.Sum256(files[i])
	}
	stage = historyUpdates
	input, user, err := verifyTextUpdates(files[0], a.session, a.profile.model, completed)
	if err != nil {
		return result, historyUncertain()
	}
	stage = historyChat
	if verifyTextChat(files[1], files[4], input, a.profile.model, a.profile.instructions, completed) != nil {
		return result, historyUncertain()
	}
	stage = historyUsage
	if verifyTextUsage(files[2], a.session, a.profile.model, completed.usage) != nil {
		return result, historyUncertain()
	}
	stage = historySummary
	if verifyTextSummary(files[3], home, a.workspace, a.session, completed) != nil {
		return result, historyUncertain()
	}
	// Re-read the fixed set after cross-file validation. Identity/size/mtime alone
	// cannot detect a same-inode edit whose timestamp was restored by a writer.
	stage = historyRecheck
	for i, name := range names {
		raw, err := scope.read(ctx, filepath.Join(base, name), limits[i])
		if err != nil || sha256.Sum256(raw) != hashes[i] {
			return result, historyUncertain()
		}
	}
	if err := a.profile.instructions.check(); err != nil {
		return result, historyUncertain()
	}
	if err := scope.check(ctx); err != nil {
		return result, err
	}
	digest := historyValueDigest(hashes)
	result = TextHistory{User: user, InputID: completed.request, ClosureID: completed.closed, NativeSessionID: a.session, NativePromptID: completed.prompt, FilesDigest: hex.EncodeToString(digest[:]), TextChunks: uint64(len(completed.chunks))}
	if completed.history != nil && *completed.history != result {
		return TextHistory{}, historyUncertain()
	}
	retained := result
	completed.history = &retained
	if logger := a.inspection.Logger; logger != nil {
		logger.InfoContext(ctx, "Grok Build closed text history verified", "owner_id", a.inspection.OwnerID, "session_id", a.product, "input_id", completed.request, "text_chunks", result.TextChunks)
	}
	return result, nil
}
