package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ContinuationIntent string

const (
	ContinueAfterSuccess ContinuationIntent = "continue-after-success"
	ResumeAfterTerminal  ContinuationIntent = "resume-after-terminal"
)

// HistoricalInput retains only the native input identity and exact UTF-8 prompt
// digest. The original content remains in the owning coordinator's transcript.
type HistoricalInput struct {
	SkillDigest  [sha256.Size]byte `json:",omitempty"`
	ID           domain.ID
	PromptDigest [sha256.Size]byte
}

// MarshalJSON preserves the original text-only checkpoint size and spelling.
// Arrays do not honor omitempty for zero bytes, so use presence explicitly.
func (input HistoricalInput) MarshalJSON() ([]byte, error) {
	type legacy HistoricalInput
	var digest *[sha256.Size]byte
	if input.SkillDigest != ([sha256.Size]byte{}) {
		digest = &input.SkillDigest
	}
	return json.Marshal(struct {
		*legacy
		SkillDigest *[sha256.Size]byte `json:",omitempty"`
	}{(*legacy)(&input), digest})
}

// ContinuationCheckpoint must come from the preceding accepted execution, after
// its terminal publication and independent owned-process cleanup. This private
// adapter does not prove account authority, cleanup, interaction acceptance or
// the user's explicit Resume; those remain the coordinator's responsibility.
type ForkHistoryCheckpoint struct {
	TurnsCount    uint32 `json:"turns_count"`
	HistoryDigest string `json:"history_digest"`
}

func (p ForkHistoryCheckpoint) matches(turns []json.RawMessage) bool {
	return p.TurnsCount > 0 && p.TurnsCount <= maxForkTurns && contextDigest(p.HistoryDigest) && uint32(len(turns)) == p.TurnsCount && historyDigest(turns) == p.HistoryDigest
}

type ContinuationCheckpoint struct {
	ForkHistory *ForkHistoryCheckpoint         `json:",omitempty"`
	Context     *ContinuationContextCheckpoint `json:",omitempty"`
	ThreadID    domain.ID
	SessionID   domain.ID
	TurnID      domain.ID
	Status      TurnStatus
	Mode        domain.SessionMode
	Inputs      []HistoricalInput
	Effective   EffectiveSettings
}

func continuationUncertain() *domain.Error {
	return domain.Fail(domain.RecoveryRequired, "The retained native conversation requires continuation verification.", "Compare its most recent terminal turn and original inputs with the accepted execution before sending another input; never replay the preceding input.")
}

func (p ContinuationCheckpoint) validate(intent ContinuationIntent) error {
	for _, id := range []domain.ID{p.ThreadID, p.SessionID, p.TurnID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if !p.Status.terminal() || !p.Mode.Valid() || len(p.Inputs) == 0 || len(p.Inputs) >= maxTrackedTurns || (intent != ContinueAfterSuccess && intent != ResumeAfterTerminal) {
		return domain.Fail(domain.InvalidArgument, "Invalid native continuation checkpoint.", "Use the exact retained terminal turn, ordered input digests and continuation intent.")
	}
	if intent == ContinueAfterSuccess && p.Status != TurnCompleted {
		return turnConflict()
	}
	seen := make(map[domain.ID]bool, len(p.Inputs))
	for _, input := range p.Inputs {
		if input.ID.Validate() != nil || seen[input.ID] {
			return domain.Fail(domain.InvalidArgument, "Invalid retained native input identities.", "Preserve the original distinct input identities in native delivery order.")
		}
		seen[input.ID] = true
	}
	return nil
}

// VerifyContinuation inspects one complete most-recent turn, never a guessed
// history file or a replacement thread. It does not publish historical messages
// as new events or clear prior protocol uncertainty. The legacy profile supports
// turn pagination even though its separate item-pagination API is unsupported.
func (c *Client) VerifyContinuation(ctx context.Context, requestID domain.ID, checkpoint ContinuationCheckpoint, intent ContinuationIntent) (result Turn, returned error) {
	defer c.recordFailure(ctx, domain.CodexHistory, &returned)
	if err := requestID.Validate(); err != nil {
		return result, err
	}
	if err := checkpoint.validate(intent); err != nil {
		return result, err
	}
	if err := c.acquireControl(ctx); err != nil {
		return result, err
	}
	defer func() { <-c.control }()
	if err := c.eligibleTurnLocked(true); err != nil {
		return result, err
	}
	if c.problem != nil {
		return result, c.problem
	}
	state := c.execution
	if !state.continuationPending || state.paused || state.active != "" || state.interrupt != "" || len(state.turns) != 0 || len(state.inputs) != 0 || len(state.pending) != 0 || state.interactions.blocksInput() {
		return result, turnConflict()
	}
	defer func() {
		if c.logger != nil {
			code := "ok"
			if returned != nil {
				code = string(domain.SafeError(returned).Code)
			}
			c.logger.InfoContext(ctx, "Codex native continuation verification", "owner_id", c.ownerID, "request_id", requestID, "intent", intent, "code", code)
		}
	}()
	mismatch := func() (Turn, error) {
		c.problem = continuationUncertain()
		state.paused = true
		return Turn{}, c.problem
	}
	if checkpoint.ThreadID != c.thread || checkpoint.SessionID != state.thread.SessionID || !sameEffectiveSettings(checkpoint.Effective, state.settings) {
		return mismatch()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return result, err
	}
	if checkpoint.ForkHistory != nil {
		turns, err := c.forkTurnsLocked(ctx, c.thread)
		if err != nil || !c.managedForkHistory || !checkpoint.ForkHistory.matches(turns) {
			return mismatch()
		}
	}
	if checkpoint.Context != nil {
		turns, err := c.compactionTurnsLocked(ctx)
		if err != nil || contextMatchesHistory(*checkpoint.Context, turns) != nil {
			return mismatch()
		}
	}
	response, err := c.wire.Call(ctx, requestID, "thread/turns/list", struct {
		ThreadID      domain.ID `json:"threadId"`
		Limit         int       `json:"limit"`
		SortDirection string    `json:"sortDirection"`
		ItemsView     string    `json:"itemsView"`
	}{c.thread, 1, "desc", "full"})
	if err != nil {
		return result, err
	}
	if response.ErrorCode != nil {
		return result, nativeRejected(*response.ErrorCode)
	}
	turn, err := decodeContinuation(response.Result, checkpoint, c.nativeImageInput)
	if err != nil {
		return mismatch()
	}
	// Recheck native metadata after the history read. Neither call mutates the
	// thread; native execution still uses StartTurn's own fresh idle check.
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, domain.SafeError(err)
	}
	retained := trackedTurn{Turn: turn, Mode: checkpoint.Mode}
	for _, input := range checkpoint.Inputs {
		state.inputs[input.ID] = inputAttempt{Digest: input.PromptDigest, SkillDigest: input.SkillDigest, TurnID: turn.ID}
		retained.Inputs = append(retained.Inputs, input.ID)
	}
	state.turns[turn.ID] = retained
	state.continuationPending, state.paused = false, false
	state.contextBase = cloneContinuationContext(checkpoint.Context)
	return turn, nil
}

func sameEffectiveSettings(a, b EffectiveSettings) bool {
	ar, br := a.WorkspaceRoots, b.WorkspaceRoots
	if len(ar) == 0 {
		ar = []string{a.Cwd}
	}
	if len(br) == 0 {
		br = []string{b.Cwd}
	}
	if len(ar) != len(br) {
		return false
	}
	for i, root := range ar {
		if !nativePathEqual(root, br[i]) {
			return false
		}
	}

	if a.Model != b.Model || a.Provider != b.Provider || !equalOptional(a.Effort, b.Effort) || !equalOptional(a.ServiceTier, b.ServiceTier) || !nativePathEqual(a.Cwd, b.Cwd) || a.ApprovalPolicy != b.ApprovalPolicy || a.ApprovalsReviewer != b.ApprovalsReviewer || a.Sandbox.Type != b.Sandbox.Type || a.Sandbox.NetworkAccess != b.Sandbox.NetworkAccess || a.Sandbox.ExcludeSlashTmp != b.Sandbox.ExcludeSlashTmp || a.Sandbox.ExcludeTmpdirEnvVar != b.Sandbox.ExcludeTmpdirEnvVar || len(a.Sandbox.WritableRoots) != len(b.Sandbox.WritableRoots) {
		return false
	}
	for i, root := range a.Sandbox.WritableRoots {
		if !nativePathEqual(root, b.Sandbox.WritableRoots[i]) {
			return false
		}
	}
	return true
}

func decodeContinuation(raw json.RawMessage, checkpoint ContinuationCheckpoint, readers ...func([]json.RawMessage) (domain.SessionInput, error)) (Turn, error) {
	turn, inputs, err := decodeLatestTurnInputs(raw, readers...)
	if err != nil || turn.ID != checkpoint.TurnID || turn.Status != checkpoint.Status || !slices.Equal(inputs, checkpoint.Inputs) {
		return Turn{}, incompatible()
	}
	return turn, nil
}

// The complete most-recent turn is shared by continuation and Steer inspection.
// This parser returns only input identities/digests, never retained prompt text.
func decodeLatestTurnInputs(raw json.RawMessage, readers ...func([]json.RawMessage) (domain.SessionInput, error)) (Turn, []HistoricalInput, error) {
	var page struct {
		Data            []json.RawMessage `json:"data"`
		NextCursor      *string           `json:"nextCursor"`
		BackwardsCursor *string           `json:"backwardsCursor"`
	}
	if domain.Decode(raw, &page) != nil || len(page.Data) != 1 {
		return Turn{}, nil, incompatible()
	}
	for _, cursor := range []*string{page.NextCursor, page.BackwardsCursor} {
		if cursor != nil && domain.Text(*cursor, "native turn cursor", 4096, true) != nil {
			return Turn{}, nil, incompatible()
		}
	}
	turn, err := decodeTurn(page.Data[0])
	if err != nil {
		return Turn{}, nil, incompatible()
	}
	var wire turnWire
	if domain.Decode(page.Data[0], &wire) != nil || wire.ItemsView != "full" || len(wire.Items) == 0 || len(wire.Items) > maxTrackedTurns {
		return Turn{}, nil, incompatible()
	}
	var inputs []HistoricalInput
	identities := make(map[domain.ID]bool)
	items := make(map[string]bool, len(wire.Items))
	for _, rawItem := range wire.Items {
		// Non-user history is deliberately not interpreted as fresh execution
		// or interaction evidence. Only its bounded item identity is needed.
		var identity struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		}
		if json.Unmarshal(rawItem, &identity) != nil || domain.Text(identity.Type, "native item kind", 256, true) != nil || domain.Text(identity.ID, "native item identity", 1024, true) != nil || items[identity.ID] {
			return Turn{}, nil, incompatible()
		}
		items[identity.ID] = true
		if identity.Type != "userMessage" {
			continue
		}

		var item struct {
			Type     string            `json:"type"`
			ID       string            `json:"id"`
			ClientID domain.ID         `json:"clientId"`
			Content  []json.RawMessage `json:"content"`
		}
		if domain.Decode(rawItem, &item) != nil || item.ClientID.Validate() != nil || identities[item.ClientID] {
			return Turn{}, nil, incompatible()
		}
		plainParts, selected, err := nativeInputSkills(item.Content)
		if err != nil {
			return Turn{}, nil, incompatible()
		}
		var decoded domain.SessionInput
		if len(readers) > 0 {
			decoded, err = readers[0](plainParts)
		} else {
			decoded, err = decodeNativeInputParts(plainParts, nil)
		}
		if err != nil {
			return Turn{}, nil, incompatible()
		}
		identities[item.ClientID] = true
		inputs = append(inputs, HistoricalInput{ID: item.ClientID, PromptDigest: decoded.InputDigest(), SkillDigest: nativeSkillDigest(selected)})
	}
	if len(inputs) == 0 {
		return Turn{}, nil, incompatible()
	}
	return turn, inputs, nil
}
