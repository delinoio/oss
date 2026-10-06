// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type CompactionTrigger string
type CompactionStage string

const (
	AutomaticCompaction CompactionTrigger = "automatic"
	ManualCompaction    CompactionTrigger = "manual"
	CompactionStarted   CompactionStage   = "started"
	CompactionCompleted CompactionStage   = "completed"
)

// The native context item describes native context only. It never creates a
// DeliDev input, assistant message or additive usage source.
type CompactionObservation struct {
	Trigger  CompactionTrigger `json:"trigger"`
	Stage    CompactionStage   `json:"stage"`
	ItemID   string            `json:"item_id"`
	ActionID domain.ID         `json:"action_id,omitempty"`
}

type CompactionRecord struct {
	ActionID      domain.ID `json:"action_id"`
	TurnID        domain.ID `json:"turn_id"`
	ItemID        string    `json:"item_id"`
	HistoryItemID string    `json:"history_item_id"`
}

// CompactedCheckpoint is private, complete-history evidence. Original input
// digests remain attached to their original source turn across repeated actions.
// Paths and native bytes must never enter RPC, product logs or public results.
type CompactedCheckpoint struct {
	Version       uint32                 `json:"version"`
	Source        ContinuationCheckpoint `json:"source"`
	Records       []CompactionRecord     `json:"records"`
	TurnsCount    uint32                 `json:"turns_count"`
	HistoryDigest string                 `json:"history_digest"`
	RolloutPath   string                 `json:"rollout_path"`
	RolloutDigest string                 `json:"rollout_digest"`
}

type compactionItem struct {
	observation CompactionObservation
	startedAt   int64
	completedAt *int64
}
type manualCompaction struct {
	actionID     domain.ID
	source       ContinuationCheckpoint
	previous     *CompactedCheckpoint
	before       []json.RawMessage
	turnID       domain.ID
	itemID       string
	acknowledged bool
	terminal     bool
}

func compactionUncertain() *domain.Error { return domain.CompactionUncertain() }

func (c *Client) StartCompaction(ctx context.Context, action domain.ID, source ContinuationCheckpoint, previous *CompactedCheckpoint) (returned error) {
	defer c.recordFailure(ctx, domain.CodexExecution, &returned)
	if action.Validate() != nil || source.validate(ContinueAfterSuccess) != nil {
		return compactionUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	defer func() { <-c.control }()
	if c.eligibleTurnLocked(false) != nil || c.problem != nil || c.execution.paused || c.execution.active != "" || c.execution.interrupt != "" || c.execution.compaction != nil || len(c.execution.pending) != 0 || len(c.subagents) != 0 || source.ThreadID != c.thread || source.SessionID != c.execution.thread.SessionID || !sameEffectiveSettings(source.Effective, c.execution.settings) {
		return compactionUncertain()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return err
	}
	if err := c.noForkWorkLocked(ctx, c.thread); err != nil {
		return err
	}
	before, err := c.compactionTurnsLocked(ctx)
	if err != nil {
		return err
	}
	if previous == nil {
		if source.Context != nil && contextMatchesHistory(*source.Context, before) != nil {
			return compactionUncertain()
		}
		turn, inputs, e := decodeLatestTurnInputs(marshalForkPage(before[len(before)-1:]))
		if e != nil || turn.ID != source.TurnID || turn.Status != TurnCompleted || !slices.Equal(inputs, source.Inputs) {
			return compactionUncertain()
		}
	} else if c.verifyCompactedHistoryLocked(source, *previous, before) != nil {
		return compactionUncertain()
	}
	if err := c.verifySidechat(ctx, c.execution.settings.Cwd, c.thread); err != nil {
		return err
	}
	// Claim before Call. A rejected, partial or lost response cannot erase this
	// attempt or grant another native send, even under a new request identity.
	attempt := &manualCompaction{actionID: action, source: cloneCompactionSource(source), previous: cloneCompactedCheckpoint(previous), before: before}
	c.execution.compaction = attempt
	defer func() {
		if returned != nil {
			c.problem = compactionUncertain()
			c.execution.paused = true
		}
	}()
	response, err := c.wire.Call(ctx, action, "thread/compact/start", struct {
		ThreadID domain.ID `json:"threadId"`
	}{c.thread})
	var ack map[string]json.RawMessage
	if err != nil || response.ErrorCode != nil || domain.Decode(response.Result, &ack) != nil || ack == nil || len(ack) != 0 {
		return compactionUncertain()
	}
	attempt.acknowledged = true
	if c.logger != nil {
		c.logger.InfoContext(ctx, "codex_compaction_acknowledged", "owner_id", c.ownerID, "action_id", action)
	}
	return nil
}

func cloneCompactionSource(p ContinuationCheckpoint) ContinuationCheckpoint {
	p.Context = cloneContinuationContext(p.Context)
	p.Inputs = slices.Clone(p.Inputs)
	p.Effective.WorkspaceRoots = slices.Clone(p.Effective.WorkspaceRoots)
	p.Effective.Sandbox.WritableRoots = slices.Clone(p.Effective.Sandbox.WritableRoots)
	p.Effective.Effort = copyString(p.Effective.Effort)
	p.Effective.ServiceTier = copyString(p.Effective.ServiceTier)
	return p
}

func cloneCompactedCheckpoint(p *CompactedCheckpoint) *CompactedCheckpoint {
	if p == nil {
		return nil
	}
	v := *p
	v.Records = slices.Clone(p.Records)
	v.Source = cloneCompactionSource(p.Source)
	return &v
}

func (c *Client) observeCompactionLocked(native nativewire.Event, turn domain.ID, raw json.RawMessage, startedAt, completedAt *int64) (Event, error) {
	var item struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if domain.Decode(raw, &item) != nil || item.Type != "contextCompaction" || domain.Text(item.ID, "native compaction item", 1024, true) != nil {
		return Event{}, incompatible()
	}
	tracked, known := c.execution.turns[turn]
	if !known || tracked.Turn.Status.terminal() || c.execution.active != turn {
		return Event{}, incompatible()
	}
	observation := CompactionObservation{Trigger: AutomaticCompaction, Stage: CompactionStarted, ItemID: item.ID}
	if a := c.execution.compaction; a != nil && a.turnID == turn {
		observation.Trigger, observation.ActionID = ManualCompaction, a.actionID
		if a.itemID != "" && a.itemID != item.ID {
			return Event{}, incompatible()
		}
		a.itemID = item.ID
	}
	if c.execution.compactionItems == nil {
		c.execution.compactionItems = map[string]compactionItem{}
	}
	key := string(turn) + "/" + item.ID
	prior, exists := c.execution.compactionItems[key]
	if native.Method == "item/started" {
		if startedAt == nil || completedAt != nil || exists && (prior.startedAt != *startedAt || prior.completedAt != nil || prior.observation != observation) {
			return Event{}, incompatible()
		}
		if !exists {
			if len(c.execution.compactionItems) >= maxTrackedTurns {
				return Event{}, compactionUncertain()
			}
			c.execution.compactionItems[key] = compactionItem{observation: observation, startedAt: *startedAt}
			c.execution.contextOrder = append(c.execution.contextOrder, ContextRecord{Trigger: observation.Trigger, ActionID: observation.ActionID, TurnID: turn, LiveItemID: item.ID})
		}
	} else {
		observation.Stage = CompactionCompleted
		if completedAt == nil || startedAt != nil || !exists || *completedAt < prior.startedAt || prior.observation.Trigger != observation.Trigger || prior.observation.ActionID != observation.ActionID || prior.completedAt != nil && *prior.completedAt != *completedAt {
			return Event{}, incompatible()
		}
		stamp := *completedAt
		prior.completedAt = &stamp
		prior.observation = observation
		c.execution.compactionItems[key] = prior
	}
	copy := observation
	return Event{Kind: CompactionEvent, ThreadID: c.thread, TurnID: turn, ItemID: item.ID, Compaction: &copy, Correlated: true}, nil
}

func (c *Client) RetainCompactedCheckpoint(ctx context.Context) (diagnosticResult CompactedCheckpoint, returned error) {
	defer c.recordFailure(ctx, domain.CodexHistory, &returned)
	if err := c.acquireControl(ctx); err != nil {
		return CompactedCheckpoint{}, err
	}
	defer func() { <-c.control }()
	if c.execution == nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	a := c.execution.compaction
	if c.problem != nil || a == nil || !a.acknowledged || !a.terminal || a.turnID == "" || a.itemID == "" || c.execution.active != "" || c.execution.paused || c.execution.interactions.blocksInput() || len(c.execution.pending) != 0 || len(c.subagents) != 0 {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	item, ok := c.execution.compactionItems[string(a.turnID)+"/"+a.itemID]
	if !ok || item.completedAt == nil || item.observation.ActionID != a.actionID {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return CompactedCheckpoint{}, err
	}
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil {
		return CompactedCheckpoint{}, err
	}
	if len(turns) != len(a.before)+1 || historyDigest(turns[:len(a.before)]) != historyDigest(a.before) {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	// Pinned thread_history.rs assigns a replay-position ID for the durable
	// ContextCompacted event and excludes the live ContextCompaction item from
	// upsert. Preserve both original identities; they are not interchangeable.
	// The complete unchanged prefix and one new owned compaction-only turn
	// establish their join. Remove this dual projection only if a pinned native
	// profile actually preserves the live item ID in complete history.
	var last turnWire
	var durable struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if domain.Decode(turns[len(turns)-1], &last) != nil || len(last.Items) != 1 || domain.Decode(last.Items[0], &durable) != nil || durable.Type != "contextCompaction" || domain.Text(durable.ID, "native durable context item", 1024, true) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	record := CompactionRecord{ActionID: a.actionID, TurnID: a.turnID, ItemID: a.itemID, HistoryItemID: durable.ID}
	if verifyCompactionTurn(turns[len(turns)-1], record) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	records := []CompactionRecord{}
	if a.previous != nil {
		records = slices.Clone(a.previous.Records)
	}
	records = append(records, record)
	wire, err := c.readThreadLocked(ctx, domain.NewID(), c.thread)
	var path string
	if err != nil || !forkableMetadata(wire, a.source) || json.Unmarshal(wire.Path, &path) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	digest, err := forkRolloutDigest(ctx, c.home, path)
	if err != nil {
		return CompactedCheckpoint{}, err
	}
	p := CompactedCheckpoint{Version: 1, Source: cloneCompactionSource(a.source), Records: records, TurnsCount: uint32(len(turns)), HistoryDigest: historyDigest(turns), RolloutPath: path, RolloutDigest: hex.EncodeToString(digest[:])}
	if c.verifyCompactedHistoryLocked(a.source, p, turns) != nil {
		return CompactedCheckpoint{}, compactionUncertain()
	}
	return p, nil
}

// VerifyCompactionRollout runs before launching a replacement process. Resume
// may append native settings metadata; afterward, independent complete history
// verification proves context without pretending that metadata is a new input.
func VerifyCompactionRollout(ctx context.Context, home string, p CompactedCheckpoint) error {
	if p.Version != 1 || p.Source.validate(ContinueAfterSuccess) != nil || len(p.Records) == 0 || len(p.Records) > maxForkTurns {
		return compactionUncertain()
	}
	digest, err := forkRolloutDigest(ctx, home, p.RolloutPath)
	if err != nil || hex.EncodeToString(digest[:]) != p.RolloutDigest {
		return compactionUncertain()
	}
	return nil
}

func (c *Client) VerifyCompactedContinuation(ctx context.Context, request domain.ID, p CompactedCheckpoint) (diagnosticResult Turn, returned error) {
	defer c.recordFailure(ctx, domain.CodexHistory, &returned)
	if request.Validate() != nil || p.Source.validate(ContinueAfterSuccess) != nil {
		return Turn{}, compactionUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return Turn{}, err
	}
	defer func() { <-c.control }()
	s := c.execution
	if s == nil || !s.continuationPending || s.paused || s.active != "" || len(s.turns) != 0 || len(s.inputs) != 0 || len(s.pending) != 0 || s.interactions.blocksInput() || c.problem != nil {
		return Turn{}, compactionUncertain()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return Turn{}, err
	}
	if err := c.noForkWorkLocked(ctx, c.thread); err != nil {
		return Turn{}, err
	}
	turns, err := c.compactionTurnsLocked(ctx)
	if err != nil || c.verifyCompactedHistoryLocked(p.Source, p, turns) != nil {
		c.problem = compactionUncertain()
		s.paused = true
		return Turn{}, compactionUncertain()
	}
	if err := c.checkNativeStateLocked(ctx, true); err != nil {
		return Turn{}, err
	}
	if ctx.Err() != nil {
		return Turn{}, domain.SafeError(ctx.Err())
	}
	turn := Turn{ID: p.Source.TurnID, Status: TurnCompleted}
	tracked := trackedTurn{Turn: turn, Mode: p.Source.Mode}
	for _, input := range p.Source.Inputs {
		s.inputs[input.ID] = inputAttempt{Digest: input.PromptDigest, TurnID: turn.ID}
		tracked.Inputs = append(tracked.Inputs, input.ID)
	}
	s.turns[turn.ID] = tracked
	for _, r := range p.Records {
		s.turns[r.TurnID] = trackedTurn{Turn: Turn{ID: r.TurnID, Status: TurnCompleted}, Mode: p.Source.Mode}
	}
	s.continuationPending, s.paused = false, false
	base := &ContinuationContextCheckpoint{Version: 1, TurnsCount: p.TurnsCount, HistoryDigest: p.HistoryDigest, RolloutPath: p.RolloutPath, RolloutDigest: p.RolloutDigest, Records: []ContextRecord{}}
	if p.Source.Context != nil {
		base.Records = slices.Clone(p.Source.Context.Records)
	}
	for _, record := range p.Records {
		base.Records = append(base.Records, ContextRecord{Trigger: ManualCompaction, ActionID: record.ActionID, TurnID: record.TurnID, LiveItemID: record.ItemID, HistoryItemID: record.HistoryItemID})
	}
	if contextMatchesHistory(*base, turns) != nil {
		c.problem, s.paused = compactionUncertain(), true
		return Turn{}, c.problem
	}
	s.contextBase = base
	return turn, nil
}

func historyDigest(turns []json.RawMessage) string {
	raw, _ := json.Marshal(turns)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func verifyCompactionTurn(raw json.RawMessage, r CompactionRecord) error {
	var turn turnWire
	if domain.Decode(raw, &turn) != nil || turn.ID != r.TurnID || turn.Status != TurnCompleted || turn.ItemsView != "full" || len(turn.Items) != 1 || len(turn.Error) > 0 && string(turn.Error) != "null" {
		return compactionUncertain()
	}
	var item struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if domain.Decode(turn.Items[0], &item) != nil || item.Type != "contextCompaction" || item.ID != r.HistoryItemID {
		return compactionUncertain()
	}
	return nil
}
func (c *Client) verifyCompactedHistoryLocked(source ContinuationCheckpoint, p CompactedCheckpoint, turns []json.RawMessage) error {
	a, b := source, p.Source
	ac, _ := json.Marshal(a.Context)
	bc, _ := json.Marshal(b.Context)
	if string(ac) != string(bc) {
		return compactionUncertain()
	}
	if p.Version != 1 || a.ThreadID != b.ThreadID || a.SessionID != b.SessionID || a.TurnID != b.TurnID || a.Status != b.Status || a.Mode != b.Mode || !slices.Equal(a.Inputs, b.Inputs) || !sameEffectiveSettings(a.Effective, b.Effective) || b.ThreadID != c.thread || b.SessionID != c.execution.thread.SessionID || !sameEffectiveSettings(b.Effective, c.execution.settings) || len(p.Records) == 0 || len(p.Records) > maxForkTurns || uint32(len(turns)) != p.TurnsCount || p.TurnsCount <= uint32(len(p.Records)) || historyDigest(turns) != p.HistoryDigest {
		return compactionUncertain()
	}
	sourceIndex := len(turns) - len(p.Records) - 1
	turn, inputs, err := decodeLatestTurnInputs(marshalForkPage(turns[sourceIndex : sourceIndex+1]))
	if err != nil || turn.ID != source.TurnID || turn.Status != TurnCompleted || !slices.Equal(inputs, source.Inputs) {
		return compactionUncertain()
	}
	seen := map[string]bool{}
	for n, r := range p.Records {
		if domain.UniqueIDs([]domain.ID{r.ActionID, r.TurnID, source.ThreadID, source.TurnID}) != nil || domain.Text(r.ItemID, "native compaction item", 1024, true) != nil || domain.Text(r.HistoryItemID, "native durable compaction item", 1024, true) != nil || seen[string(r.ActionID)] || seen[string(r.TurnID)] || seen[r.ItemID] || seen["history:"+r.HistoryItemID] || verifyCompactionTurn(turns[sourceIndex+n+1], r) != nil {
			return compactionUncertain()
		}
		seen[string(r.ActionID)], seen[string(r.TurnID)], seen[r.ItemID], seen["history:"+r.HistoryItemID] = true, true, true, true
	}
	return nil
}

func (c *Client) compactionTurnsLocked(ctx context.Context) ([]json.RawMessage, error) {
	var turns []json.RawMessage
	var cursor *string
	seen := map[string]bool{}
	size := 0
	for {
		reply, err := c.wire.Call(ctx, domain.NewID(), "thread/turns/list", struct {
			Thread    domain.ID `json:"threadId"`
			Limit     int       `json:"limit"`
			Direction string    `json:"sortDirection"`
			View      string    `json:"itemsView"`
			Cursor    *string   `json:"cursor,omitempty"`
		}{c.thread, 50, "asc", "full", cursor})
		var page struct {
			Data []json.RawMessage `json:"data"`
			Next *string           `json:"nextCursor"`
			Back *string           `json:"backwardsCursor"`
		}
		if err != nil || reply.ErrorCode != nil || domain.Decode(reply.Result, &page) != nil || page.Data == nil || len(page.Data) > 50 {
			return nil, compactionUncertain()
		}
		for _, raw := range page.Data {
			turn, err := decodeTurn(raw)
			var wire turnWire
			if err != nil || !turn.Status.terminal() || domain.Decode(raw, &wire) != nil || wire.ItemsView != "full" || len(wire.Items) == 0 || seen[string(turn.ID)] {
				return nil, compactionUncertain()
			}
			seen[string(turn.ID)] = true
			items := map[string]bool{}
			for _, rawItem := range wire.Items {
				var item struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				}
				if json.Unmarshal(rawItem, &item) != nil || domain.Text(item.ID, "native history item", 1024, true) != nil || items[item.ID] {
					return nil, compactionUncertain()
				}
				items[item.ID] = true
				switch item.Type {
				case "userMessage", "agentMessage", "reasoning", "plan", "contextCompaction":
				case "commandExecution", "fileChange":
					if _, err := decodeTool(rawItem, item.Type, true); err != nil {
						return nil, compactionUncertain()
					}
				default:
					return nil, compactionUncertain()
				}
			}
			size += len(raw)
			if len(turns) >= maxForkTurns || size > 4<<20 {
				return nil, compactionUncertain()
			}
			var value any
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			if decoder.Decode(&value) != nil {
				return nil, compactionUncertain()
			}
			normal, _ := json.Marshal(value)
			turns = append(turns, normal)
		}
		if page.Next == nil {
			break
		}
		if len(page.Data) == 0 || domain.Text(*page.Next, "native compaction cursor", 4096, true) != nil || seen["cursor:"+*page.Next] {
			return nil, compactionUncertain()
		}
		seen["cursor:"+*page.Next] = true
		cursor = page.Next
	}
	if len(turns) == 0 {
		return nil, compactionUncertain()
	}
	return turns, nil
}
