package claude

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const maxSessionCheckpoint = 8 << 20

// CheckpointReference must come from independently retained original execution
// and completion evidence, never from the file being checked. It conveys no
// account, workspace lease, durable attempt claim or process-cleanup authority.
type CheckpointReference struct {
	SHA256         string    `json:"sha256"`
	SessionID      domain.ID `json:"session_id"`
	OwnerID        domain.ID `json:"owner_id"`
	InputID        domain.ID `json:"input_id"`
	InputSHA256    string    `json:"input_sha256"`
	NativeTurnID   string    `json:"native_turn_id"`
	RequiresResume bool      `json:"requires_resume"`
}

type checkpointResume struct {
	Transcript TranscriptObservation `json:"transcript"`
	Messages   int                   `json:"messages"`
	Action     domain.ID             `json:"action"`
}

type sessionCheckpoint struct {
	Version            uint32                         `json:"version"`
	Configuration      string                         `json:"configuration_sha256"`
	Session            domain.ID                      `json:"session_id"`
	Owner              domain.ID                      `json:"owner_id"`
	Input              domain.ID                      `json:"input_id"`
	InputDigest        string                         `json:"input_sha256"`
	Turn               string                         `json:"native_turn_id"`
	Command            CommandState                   `json:"command_state"`
	Kind               ResultKind                     `json:"result_kind"`
	Reason             TerminalReason                 `json:"terminal_reason"`
	Error              bool                           `json:"is_error"`
	ContinuationFailed bool                           `json:"continuation_failed"`
	LastAction         CompactResult                  `json:"last_compaction_status,omitempty"`
	Applied            AppliedSettings                `json:"applied"`
	Transcript         TranscriptObservation          `json:"transcript"`
	Messages           []HistoryMessageProof          `json:"messages"`
	Compactions        []HistoryCompactionProof       `json:"compactions"`
	Actions            []HistoryCompactionActionProof `json:"actions"`
	Resumes            []checkpointResume             `json:"resumes"`
	Inputs             []domain.ID                    `json:"input_identities"`
	NativeIDs          []string                       `json:"native_identities"`
	ProviderIDs        []string                       `json:"provider_identities"`
	Owners             []domain.ID                    `json:"process_owners"`
	Authorities        []string                       `json:"credential_sha256"`
	ReadTools          []checkpointInlineTool         `json:"inline_read_tools,omitempty"`
	BashTools          []checkpointInlineTool         `json:"inline_bash_tools,omitempty"`
	WriteTools         []checkpointInlineTool         `json:"inline_write_tools,omitempty"`
	EditTools          []checkpointInlineTool         `json:"inline_edit_tools,omitempty"`
	ToolApprovals      []checkpointToolApproval       `json:"tool_approvals,omitempty"`
}

// RetainCheckpoint consumes an original closed handoff and returns bounded
// canonical private bytes plus a descriptor for separate durable retention.
// The Worker must atomically persist/pin these bytes and original completion,
// account and cleanup evidence before attempting any later restoration.
func (closed *ClosedAPISession) RetainCheckpoint(ctx context.Context) ([]byte, CheckpointReference, error) {
	if closed == nil || closed.previous == nil {
		return nil, CheckpointReference{}, historyUncertain()
	}
	closed.mu.Lock()
	defer closed.mu.Unlock()
	if closed.used || !closed.previous.closed.Load() || closed.previous.problem != nil {
		return nil, CheckpointReference{}, sessionBusy()
	}
	s := closed.previous
	observed, err := s.readRetainedTranscript(ctx)
	if err != nil {
		return nil, CheckpointReference{}, err
	}
	if observed != closed.transcript {
		return nil, CheckpointReference{}, historyUncertain()
	}
	b, h := s.current, s.history
	if b == nil || b.terminal == nil || len(b.content.snapshots) != 0 || b.content.bufferedBytes != 0 {
		return nil, CheckpointReference{}, continuationUnavailable()
	}
	cp := sessionCheckpoint{Version: 1, Configuration: checkpointConfiguration(s.config, s.serverOrigin), Session: s.config.SessionID, Owner: s.config.Process.OwnerID, Input: b.input, InputDigest: hex.EncodeToString(b.digest[:]), Turn: b.turnID, Command: b.command, Kind: b.terminal.Kind, Reason: b.terminal.Reason, Error: b.terminal.Error, ContinuationFailed: b.continuationFailed, Applied: s.initial, Transcript: closed.transcript, Messages: h.messages, Compactions: h.compactions, Actions: h.actions}
	tools, err := b.closedInlineTools()
	if err != nil {
		return nil, CheckpointReference{}, err
	}
	cp.ReadTools, cp.BashTools, cp.WriteTools, cp.EditTools = tools.Read, tools.Bash, tools.Write, tools.Edit
	cp.ToolApprovals, err = b.closedToolApprovals()
	if err != nil {
		return nil, CheckpointReference{}, err
	}
	if s.compaction != nil {
		cp.LastAction = s.compaction.status
	}
	for _, resume := range h.resumes {
		cp.Resumes = append(cp.Resumes, checkpointResume{resume.transcript, resume.messages, resume.action})
	}
	for id := range s.inputs {
		cp.Inputs = append(cp.Inputs, id)
	}
	for id := range b.seen {
		cp.NativeIDs = append(cp.NativeIDs, id)
	}
	for key := range b.content.seen {
		// Only root provider messages are eligible; a child lineage cannot be
		// collapsed into a root identity while serializing retained ownership.
		id, ok := strings.CutPrefix(key, "\x00")
		if !ok || strings.ContainsRune(id, 0) {
			return nil, CheckpointReference{}, continuationUnavailable()
		}
		cp.ProviderIDs = append(cp.ProviderIDs, id)
	}
	for owner := range s.owners {
		cp.Owners = append(cp.Owners, owner)
	}
	for digest := range s.authorities {
		cp.Authorities = append(cp.Authorities, hex.EncodeToString(digest[:]))
	}
	slices.Sort(cp.Inputs)
	slices.Sort(cp.NativeIDs)
	slices.Sort(cp.ProviderIDs)
	slices.Sort(cp.Owners)
	slices.Sort(cp.Authorities)
	raw, err := json.Marshal(cp)
	if err != nil || len(raw) > maxSessionCheckpoint {
		return nil, CheckpointReference{}, historyUncertain()
	}
	ref := CheckpointReference{checkpointDigest(raw), cp.Session, cp.Owner, cp.Input, cp.InputDigest, cp.Turn, closed.requiresResume}
	if cp.validate(s.config, s.serverOrigin, ref) != nil {
		return nil, CheckpointReference{}, historyUncertain()
	}
	closed.used = true
	if s.config.Process.Logger != nil {
		s.config.Process.Logger.InfoContext(ctx, "Claude Code closed checkpoint prepared for independent durable retention", "owner_id", cp.Owner, "session_id", cp.Session, "input_id", cp.Input)
	}
	return raw, ref, nil
}

// RestoreCheckpoint validates independently pinned private bytes and original
// files without launching a process. The caller must already have verified the
// preceding Worker completion/cleanup, immutable account configuration and an
// exclusive runtime/workspace claim. Each new execution needs a durable claim:
// parsing the same bytes again cannot itself prevent a cross-process retry.
// The returned capability still requires ContinueAPISession with a fresh owner
// and credential, and sends no input until a separately authorized SendInput.
func RestoreCheckpoint(ctx context.Context, config APIStreamConfig, raw []byte, ref CheckpointReference) (*ClosedAPISession, error) {
	if err := ctx.Err(); err != nil {
		return nil, domain.SafeError(err)
	}
	if len(raw) == 0 || len(raw) > maxSessionCheckpoint || checkpointDigest(raw) != ref.SHA256 {
		return nil, historyUncertain()
	}
	var cp sessionCheckpoint
	// This bounded private format can exceed the public 1 MiB JSON limit. Exact
	// canonical re-encoding below rejects duplicate/unknown fields, malformed
	// UTF-8 normalization, omitted fields and every alternate representation.
	if json.Unmarshal(raw, &cp) != nil || cp.validate(config, config.API.ServerOrigin, ref) != nil {
		return nil, historyUncertain()
	}
	canonical, err := json.Marshal(cp)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, historyUncertain()
	}
	origin := config.API.ServerOrigin
	config.API, config.Process.Args = APIConfig{}, nil
	config.Process.Env = retainedLookupEnvironment(config.Process.Env)
	h := &sessionHistory{messages: cp.Messages, compactions: cp.Compactions, actions: cp.Actions}
	for _, resume := range cp.Resumes {
		h.resumes = append(h.resumes, historyResumeProof{resume.Transcript, resume.Messages, resume.Action})
	}
	b := &ExecutionBinding{session: cp.Session, input: cp.Input, model: config.Model, workspace: config.Workspace, home: config.Home, permission: config.Permission, command: cp.Command, initialized: true, accepted: true, finished: true, terminal: &NativeResult{Kind: cp.Kind, Reason: cp.Reason, Error: cp.Error}, owner: cp.Owner, logger: config.Process.Logger, seen: map[string]bool{}, runState: RunIdle, turnID: cp.Turn, continuationFailed: cp.ContinuationFailed}
	digest, _ := hex.DecodeString(cp.InputDigest)
	copy(b.digest[:], digest)
	b.content.seen = map[string]bool{}
	for _, id := range cp.NativeIDs {
		b.seen[id] = true
	}
	for _, id := range cp.ProviderIDs {
		b.content.seen["\x00"+id] = true
	}
	if cp.inlineTools().count() != 0 {
		b.content.tools = map[string]nativeToolState{}
	}
	for _, tool := range cp.inlineTools().all() {
		input, _ := hex.DecodeString(tool.InputDigest)
		metadata, _ := hex.DecodeString(tool.MetadataDigest)
		state := nativeToolState{ownerInput: tool.Input, ownerTurn: tool.Turn, name: string(tool.Kind), message: tool.Message, index: tool.Index, finished: true, streamed: true, caller: NativeToolCaller{Kind: tool.Caller}, inline: &inlineToolEvidence{Error: tool.Error, NativeID: tool.Result}}
		copy(state.input[:], input)
		copy(state.inline.Metadata[:], metadata)
		b.content.tools[tool.ID] = state
	}
	if err := restoreToolApprovals(b, cp.ToolApprovals); err != nil {
		return nil, err
	}
	s := &APISession{config: config, initial: cp.Applied, current: b, inputs: map[domain.ID]bool{}, history: h, authorities: map[[sha256.Size]byte]bool{}, owners: map[domain.ID]bool{}, serverOrigin: origin}
	for _, id := range cp.Inputs {
		s.inputs[id] = true
	}
	for _, owner := range cp.Owners {
		s.owners[owner] = true
	}
	for _, encoded := range cp.Authorities {
		var digest [sha256.Size]byte
		raw, _ := hex.DecodeString(encoded)
		copy(digest[:], raw)
		s.authorities[digest] = true
	}
	if cp.LastAction != "" {
		s.compaction = &manualCompactionBinding{settled: true, status: cp.LastAction}
	}
	s.closed.Store(true)
	observed, err := s.readRetainedTranscript(ctx)
	if err != nil {
		return nil, err
	}
	if observed != cp.Transcript {
		return nil, historyUncertain()
	}
	if config.Process.Logger != nil {
		config.Process.Logger.InfoContext(ctx, "Claude Code retained checkpoint verified without native launch", "owner_id", cp.Owner, "session_id", cp.Session, "input_id", cp.Input)
	}
	return &ClosedAPISession{previous: s, transcript: observed, requiresResume: ref.RequiresResume}, nil
}

func checkpointDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func checkpointConfiguration(config APIStreamConfig, origin string) string {
	// Pin original private paths/instructions by digest, never by publishing
	// their contents in the checkpoint or allowing the file to supply them.
	value := struct {
		Version, Executable, Directory, Runtime, Home, Workspace, Model, Effort, Permission, Instructions, Origin string
	}{config.Version, config.Process.Executable, config.Process.Directory, config.Process.Cwd, config.Home, config.Workspace, config.Model, string(config.Effort), string(config.Permission), checkpointDigest([]byte(config.Instructions)), origin}
	raw, _ := json.Marshal(value)
	return checkpointDigest(raw)
}

func (cp sessionCheckpoint) validate(config APIStreamConfig, origin string, ref CheckpointReference) error {
	if cp.Version != 1 || config.Version != SupportedVersion || !validHistoryDigest(ref.SHA256) || cp.Configuration != checkpointConfiguration(config, origin) || cp.Session != ref.SessionID || cp.Session != config.SessionID || cp.Owner != ref.OwnerID || cp.Owner != config.Process.OwnerID || cp.Input != ref.InputID || cp.InputDigest != ref.InputSHA256 || !validHistoryDigest(cp.InputDigest) || cp.Turn != ref.NativeTurnID || !nativeUUID(cp.Turn) || cp.Applied.Model != config.Model || (cp.Applied.Effort != nil && !validNativeEffort(*cp.Applied.Effort, false)) || (config.Effort != "" && (cp.Applied.Effort == nil || *cp.Applied.Effort != config.Effort)) || !validNativePermission(config.Permission) {
		return historyUncertain()
	}
	for _, id := range []domain.ID{cp.Session, cp.Owner, cp.Input} {
		if id.Validate() != nil {
			return historyUncertain()
		}
	}
	switch cp.Kind {
	case ResultSuccess, ResultExecutionError, ResultMaxTurns, ResultMaxBudget, ResultStructuredOutput:
	default:
		return historyUncertain()
	}
	if isError, known := terminalError(cp.Reason); !known || (isError && !cp.Error) || (cp.Kind != ResultSuccess && !cp.Error) || (cp.Kind == ResultMaxTurns && cp.Reason != MaxTurns) || (cp.Kind == ResultMaxBudget && cp.Reason != BudgetExhausted) || (cp.Kind == ResultStructuredOutput && cp.Reason != StructuredOutputRetryExhausted && cp.Reason != AbortedStreaming && cp.Reason != AbortedTools) {
		return historyUncertain()
	}
	if cp.LastAction != "" && (len(cp.Actions) == 0 || cp.Actions[len(cp.Actions)-1].Status != cp.LastAction || (cp.LastAction != CompactSucceeded && cp.LastAction != CompactFailed)) {
		return historyUncertain()
	}
	terminal := NativeResult{Kind: cp.Kind, Reason: cp.Reason, Error: cp.Error}
	if (cp.Command != CommandCompleted && cp.Command != CommandCancelled) || (cp.Command == CommandCancelled) != terminal.cancelsCommand() {
		return historyUncertain()
	}
	if ref.RequiresResume != (!terminal.Successful() || cp.ContinuationFailed || cp.LastAction == CompactFailed) || !checkpointIDs(cp.Inputs, maxStreamIdentities) || !slices.Contains(cp.Inputs, cp.Input) || !checkpointIDs(cp.Owners, maxStreamIdentities) || !slices.Contains(cp.Owners, cp.Owner) || !checkpointStrings(cp.NativeIDs, domain.MaxExecutionEvents, nativeUUID) || !slices.Contains(cp.NativeIDs, cp.Turn) || !slices.Contains(cp.NativeIDs, string(cp.Input)) || !checkpointStrings(cp.ProviderIDs, maxStreamIdentities, func(id string) bool { return domain.Text(id, "native provider message", 1024, true) == nil }) || len(cp.Authorities) == 0 || !checkpointStrings(cp.Authorities, maxStreamIdentities, validHistoryDigest) || len(cp.Messages) == 0 || len(cp.Messages) > maxStreamIdentities || len(cp.Compactions) > maxStreamIdentities || len(cp.Actions) > maxStreamIdentities || len(cp.Resumes) > maxStreamIdentities {
		return historyUncertain()
	}
	if cp.inlineTools().count() > 4096 {
		return historyUncertain()
	}
	results, ids := map[string]bool{}, map[string]bool{}
	for _, group := range cp.inlineTools().groups() {
		tools := group.Items
		for i, tool := range tools {
			if tool.Error && group.Kind != inlineReadTool || domain.Text(tool.ID, "native inline tool identity", 1024, true) != nil || (i > 0 && tools[i-1].ID >= tool.ID) || ids[tool.ID] || !slices.Contains(cp.Inputs, tool.Input) || !checkpointHasIdentity(cp.NativeIDs, tool.Turn) || !slices.Contains(cp.ProviderIDs, tool.Message) || !validHistoryDigest(tool.InputDigest) || !validHistoryDigest(tool.MetadataDigest) || !checkpointHasIdentity(cp.NativeIDs, tool.Result) || results[tool.Result] || (tool.Caller != "" && tool.Caller != DirectCaller) {
				return historyUncertain()
			}
			results[tool.Result], ids[tool.ID] = true, true
		}
	}
	if err := cp.validateToolApprovals(); err != nil {
		return err
	}
	for _, message := range cp.Messages {
		if !checkpointHasIdentity(cp.NativeIDs, message.NativeID) {
			return historyUncertain()
		}
	}
	for _, action := range cp.Actions {
		if !slices.Contains(cp.Inputs, action.ActionID) || !checkpointHasIdentity(cp.NativeIDs, action.Echo.NativeID) || !checkpointHasIdentity(cp.NativeIDs, action.Output.NativeID) {
			return historyUncertain()
		}
	}
	return nil
}

func checkpointHasIdentity(values []string, id string) bool {
	_, present := slices.BinarySearch(values, id)
	return present
}

func checkpointIDs(values []domain.ID, bound int) bool {
	if len(values) == 0 || len(values) > bound {
		return false
	}
	for i, value := range values {
		if value.Validate() != nil || (i != 0 && values[i-1] >= value) {
			return false
		}
	}
	return true
}

func checkpointStrings(values []string, bound int, valid func(string) bool) bool {
	if len(values) > bound {
		return false
	}
	for i, value := range values {
		if !valid(value) || (i != 0 && values[i-1] >= value) {
			return false
		}
	}
	return true
}

func (cp sessionCheckpoint) inlineTools() inlineToolProofs {
	return inlineToolProofs{Read: cp.ReadTools, Bash: cp.BashTools, Write: cp.WriteTools, Edit: cp.EditTools}
}
