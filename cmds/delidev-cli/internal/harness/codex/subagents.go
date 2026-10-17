// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func subagentStatus(status string) (domain.SubagentStatus, error) {
	switch status {
	case "pendingInit":
		return domain.SubagentPending, nil
	case "running":
		return domain.SubagentRunning, nil
	case "completed":
		return domain.SubagentCompleted, nil
	case "errored":
		return domain.SubagentFailed, nil
	case "interrupted":
		return domain.SubagentInterrupted, nil
	case "shutdown":
		return domain.SubagentShutdown, nil
	case "notFound":
		return domain.SubagentUnavailable, nil
	}
	return "", incompatible()
}

func (c *Client) childEvent(batch []domain.SubagentObservation, staged map[string]string) (Event, error) {
	state := domain.SubagentState{}
	for id, o := range c.subagents {
		state[id] = domain.SubagentOwner{ID: o.ID, ParentID: o.ParentID, Status: o.Status}
	}
	if _, err := domain.ApplySubagents(state, string(c.thread), batch); err != nil {
		return Event{}, incompatible()
	}
	turn := c.subagentTurn
	if turn == "" {
		turn = c.execution.active
	}
	if turn == "" && len(c.execution.turns) == 1 {
		for id := range c.execution.turns {
			turn = id
		}
	}
	if turn.Validate() != nil {
		return Event{}, incompatible()
	}
	if c.subagents == nil {
		c.subagents = map[string]domain.SubagentObservation{}
	}
	for _, o := range batch {
		prior := c.subagents[o.NativeID]
		// Keep source omissions in the returned event while retaining the last
		// available native facts for future ownership comparisons.
		if o.RequestedModel == nil {
			o.RequestedModel = prior.RequestedModel
		}
		if o.ObservedModel == nil {
			o.ObservedModel = prior.ObservedModel
		}
		if o.Output == nil {
			o.Output = prior.Output
		}
		if o.Usage == nil {
			o.Usage = prior.Usage
		}
		c.subagents[o.NativeID] = o
		// Retain a staged name only now: the batch passed ownership validation
		// and the child is known with a proven parent.
		if name, ok := staged[o.NativeID]; ok {
			if c.subagentNames == nil {
				c.subagentNames = map[string]string{}
			}
			c.subagentNames[o.NativeID] = name
		}
	}
	return Event{Kind: SubagentEvent, ThreadID: c.thread, TurnID: turn, Correlated: true, Subagents: batch, SubagentNames: c.displayNames(batch)}, nil
}

// stageNickname accepts a native agentNickname only as a non-blank, well-formed
// JSON string of at most domain.MaxSubagentDisplayNameRunes characters, judged
// on the raw JSON so malformed surrogate escapes cannot become U+FFFD text.
// Missing, null, blank or invalid values are ignored without invalidating the
// rest of the child telemetry. The name is only staged: childEvent retains it
// after the child's ownership and parent are proven, so an unproven or rejected
// thread never leaves a retained name. A later valid observation of a known
// child replaces its retained name; the name is inert display text and never
// substitutes roles, task descriptions, models or identities.
func (c *Client) stageNickname(staged map[string]string, id domain.ID, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	name, ok := domain.ParseSubagentDisplayNameJSON(raw)
	if !ok {
		if c.logger != nil {
			c.logger.Debug("Codex agent nickname ignored", "owner_id", c.ownerID, "child_id", id)
		}
		return
	}
	staged[string(id)] = name
}

// displayNames reports the retained native name of each child in a batch by
// original product child ID. Children without an accepted name are omitted.
func (c *Client) displayNames(batch []domain.SubagentObservation) []domain.SubagentDisplayName {
	var names []domain.SubagentDisplayName
	for _, o := range batch {
		if name, ok := c.subagentNames[o.NativeID]; ok {
			names = append(names, domain.SubagentDisplayName{ID: o.ID, Name: name})
		}
	}
	return names
}

// Consume only original canonical collab items. Requested model is independent
// from an observed model; observing a native control tool never invokes it.
func (c *Client) observeCollaboration(item json.RawMessage, sender domain.ID, turn domain.ID) (Event, error) {
	var v struct {
		Type      string      `json:"type"`
		ID        string      `json:"id"`
		Tool      string      `json:"tool"`
		Status    string      `json:"status"`
		Sender    domain.ID   `json:"senderThreadId"`
		Receivers []domain.ID `json:"receiverThreadIds"`
		Prompt    *string     `json:"prompt"`
		Model     *string     `json:"model"`
		Effort    *string     `json:"reasoningEffort"`
		States    map[string]struct {
			Status  string  `json:"status"`
			Message *string `json:"message"`
		} `json:"agentsStates"`
	}
	if decodeCaseExact(item, &v) != nil || v.Type != "collabAgentToolCall" || v.Sender != sender || domain.Text(v.ID, "native collab item", 1024, true) != nil || !slices.Contains([]string{"spawnAgent", "sendInput", "resumeAgent", "wait", "closeAgent", "sendMessage", "followupTask", "interruptAgent", "listAgents"}, v.Tool) || !slices.Contains([]string{"inProgress", "completed", "failed", "interrupted"}, v.Status) || len(v.Receivers) > 128 {
		return Event{}, incompatible()
	}
	if sender != c.thread {
		if _, ok := c.subagents[string(sender)]; !ok {
			return Event{}, incompatible()
		}
	}
	if len(v.Receivers) == 0 {
		return Event{Kind: MetadataEvent, ThreadID: c.thread, TurnID: turn, Correlated: true, Metadata: RawSupplementDiscarded}, nil
	}
	batch := []domain.SubagentObservation{}
	seen := map[string]bool{}
	for _, receiver := range v.Receivers {
		id := string(receiver)
		if receiver.Validate() != nil || seen[id] {
			return Event{}, incompatible()
		}
		seen[id] = true
		o, exists := c.subagents[id]
		if !exists {
			if v.Tool != "spawnAgent" || v.Status != "completed" {
				return Event{}, incompatible()
			}
			o = domain.SubagentObservation{ID: domain.NewID(), NativeID: id, ParentID: string(sender), Status: domain.SubagentPending, RequestedModel: v.Model}
		} else if v.Tool == "spawnAgent" && (o.ParentID != string(sender) || o.RequestedModel != nil && v.Model != nil && *o.RequestedModel != *v.Model) {
			return Event{}, incompatible()
		}
		if v.Tool == "spawnAgent" && o.RequestedModel == nil {
			o.RequestedModel = v.Model
		}
		o.Source, o.SourceID = domain.CodexCollaborationSource, v.ID
		// This receipt owns only telemetry supplied by the collaboration item.
		// childEvent retains last available facts separately after validation.
		o.Usage, o.Output, o.ObservedModel = nil, nil, nil
		if state, ok := v.States[id]; ok {
			status, err := subagentStatus(state.Status)
			if err != nil {
				return Event{}, err
			}
			o.Status = status
			// Errored messages are provider diagnostics, never public output.
			if state.Message != nil && status == domain.SubagentCompleted {
				o.Output = &domain.SubagentOutput{NativeMessageID: v.ID, Text: *state.Message, Partial: true}
			}
		}
		batch = append(batch, o)
	}
	for id := range v.States {
		if !seen[id] {
			return Event{}, incompatible()
		}
	}
	return c.childEvent(batch, nil)
}

func (c *Client) observeSubagentActivity(item json.RawMessage, turn domain.ID) (Event, error) {
	var v struct {
		Type  string    `json:"type"`
		ID    string    `json:"id"`
		Kind  string    `json:"kind"`
		Agent domain.ID `json:"agentThreadId"`
		Path  string    `json:"agentPath"`
	}
	if decodeCaseExact(item, &v) != nil || v.Type != "subAgentActivity" || v.Agent.Validate() != nil || domain.Text(v.Path, "native agent path", 1024, true) != nil {
		return Event{}, incompatible()
	}
	if !slices.Contains([]string{"started", "interacted", "interrupted", "completed"}, v.Kind) {
		return Event{}, incompatible()
	}
	// Activity identifies an agent, not its parent. Resolve it using the separate
	// read-only descendant inventory instead of deriving ancestry from a path.
	if domain.Text(v.ID, "native activity identity", 1024, true) != nil {
		return Event{}, incompatible()
	}
	if _, known := c.subagents[string(v.Agent)]; !known {
		if c.pendingChildStatuses == nil {
			c.pendingChildStatuses = make(map[domain.ID]struct{})
		}
		if _, exists := c.pendingChildStatuses[v.Agent]; !exists && len(c.pendingChildStatuses) >= 4096 {
			return Event{}, incompatible()
		}
		c.pendingChildStatuses[v.Agent] = struct{}{}
	}
	return Event{Kind: SubagentActivityEvent, ThreadID: c.thread, TurnID: turn, AgentThreadID: v.Agent, Correlated: true, ItemID: v.ID}, nil
}

// Retain the original activity identity only after a separate descendant read
// has established its ownership. Activity paths never provide ancestry.
func (c *Client) ObserveResolvedActivity(ctx context.Context, event Event) (Event, error) {
	if err := c.acquireControl(ctx); err != nil {
		return Event{}, err
	}
	defer func() { <-c.control }()
	child, ok := c.subagents[string(event.AgentThreadID)]
	if event.Kind != SubagentActivityEvent || event.ThreadID != c.thread || !event.Correlated || !ok || domain.Text(event.ItemID, "native activity identity", 1024, true) != nil {
		return Event{}, incompatible()
	}
	child.Source, child.SourceID = domain.CodexActivitySource, event.ItemID
	child.Usage, child.Output, child.ObservedModel, child.RequestedModel = nil, nil, nil, nil
	return c.childEvent([]domain.SubagentObservation{child}, nil)
}

// InspectDescendants uses only read operations. It does not load/resume children
// or reuse their direct-input capability. State-DB-only inventory avoids the
// native list default that scans and repairs rollout metadata. All pages
// validate before publication.
func (c *Client) InspectDescendants(ctx context.Context) (Event, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.acquireControl(ctx); err != nil {
		return Event{}, err
	}
	defer func() { <-c.control }()
	if c.execution == nil || c.thread.Validate() != nil {
		return Event{}, incompatible()
	}
	for attempt := 0; attempt < 16; attempt++ {
		event, err := c.inspectDescendantSnapshotLocked(ctx)
		var pending *descendantVisibilityPending
		if !errors.As(err, &pending) {
			return event, err
		}
		if c.logger != nil && (attempt == 0 || attempt == 15) {
			c.logger.InfoContext(ctx, "Codex descendant inventory pending", "owner_id", c.ownerID, "stage", "state-db-visibility", "attempt", attempt+1, "pending_count", len(c.pendingChildStatuses))
		}
		if attempt == 15 {
			return Event{}, incompatible()
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Event{}, domain.SafeError(ctx.Err())
		case <-timer.C:
		}
	}
	return Event{}, incompatible()
}

type descendantVisibilityPending struct{}

func (*descendantVisibilityPending) Error() string { return "native descendant visibility is pending" }

// A valid state-DB snapshot can precede the asynchronous persistence of a
// separately observed child status. Re-read only successful missing snapshots;
// transport, schema, cancellation and identity failures are never retried. The
// original pending identities remain completion fences until independent
// inventory proves every one. No native input or child control is repeated.
func (c *Client) inspectDescendantSnapshotLocked(ctx context.Context) (Event, error) {
	batch := []domain.SubagentObservation{}
	cursor := ""
	seen := map[string]bool{}
	staged := map[string]string{}
	for page := 0; page < 16; page++ {
		response, err := c.call(ctx, domain.NewID(), "thread/list", struct {
			Ancestor    domain.ID `json:"ancestorThreadId"`
			Limit       int       `json:"limit"`
			Cursor      string    `json:"cursor,omitempty"`
			Sources     []string  `json:"sourceKinds"`
			StateDBOnly bool      `json:"useStateDbOnly"`
		}{c.thread, 128, cursor, []string{"subAgent", "subAgentThreadSpawn", "subAgentOther"}, true})
		if err != nil {
			return Event{}, err
		}
		if response.ErrorCode != nil {
			return Event{}, nativeRejected(*response.ErrorCode)
		}
		var result struct {
			Data []json.RawMessage `json:"data"`
			Next *string           `json:"nextCursor"`
			Back *string           `json:"backwardsCursor"`
		}
		if decodeCaseExact(response.Result, &result) != nil || result.Data == nil {
			return Event{}, incompatible()
		}
		for _, raw := range result.Data {
			var t threadWire
			if decodeCaseExact(raw, &t) != nil || t.ID.Validate() != nil || t.ParentThreadID == nil || t.ParentThreadID.Validate() != nil || t.SessionID.Validate() != nil || t.CLIVersion != c.version || t.ModelProvider != c.execution.settings.Provider || validateThreadStatus(t.Status) != nil || seen[string(t.ID)] {
				if c.logger != nil {
					c.logger.WarnContext(ctx, "Codex descendant inventory rejected", "owner_id", c.ownerID, "stage", "thread-identity")
				}
				return Event{}, incompatible()
			}
			seen[string(t.ID)] = true
			o, exists := c.subagents[string(t.ID)]
			if !exists {
				o = domain.SubagentObservation{ID: domain.NewID(), NativeID: string(t.ID), ParentID: string(*t.ParentThreadID), Status: domain.SubagentPending}
			}
			if o.ParentID != string(*t.ParentThreadID) {
				return Event{}, incompatible()
			}
			c.stageNickname(staged, t.ID, t.AgentNickname)
			o.Usage, o.Output, o.ObservedModel = nil, nil, nil
			o, err = c.readChildHistory(ctx, o, t.ID)
			if err != nil {
				if c.logger != nil {
					c.logger.WarnContext(ctx, "Codex descendant inventory rejected", "owner_id", c.ownerID, "stage", "child-history", "code", domain.SafeError(err).Code)
				}
				return Event{}, err
			}
			o.Source, o.SourceID = domain.CodexHistorySource, string(t.ID)
			batch = append(batch, o)
			if len(batch) > 128 {
				return Event{}, incompatible()
			}
		}
		if result.Next == nil {
			break
		}
		if *result.Next == "" || len(*result.Next) > 2048 || *result.Next == cursor || page == 15 {
			return Event{}, incompatible()
		}
		cursor = *result.Next
	}
	if len(batch) == 0 {
		if len(c.pendingChildStatuses) != 0 {
			return Event{}, &descendantVisibilityPending{}
		}
		return Event{Kind: MetadataEvent, ThreadID: c.thread, Correlated: true, Metadata: RawSupplementDiscarded}, nil
	}
	for id := range c.pendingChildStatuses {
		if !seen[string(id)] {
			return Event{}, &descendantVisibilityPending{}
		}
	}
	event, err := c.childEvent(batch, staged)
	if err != nil {
		return Event{}, err
	}
	clear(c.pendingChildStatuses)
	return event, nil
}

func (c *Client) observeChildNative(native nativewire.Event) (Event, bool, error) {
	if native.Method == "thread/started" {
		var v struct {
			Thread threadWire `json:"thread"`
		}
		if decodeCaseExact(native.Params, &v) == nil && v.Thread.ParentThreadID != nil {
			t := v.Thread
			if t.ID.Validate() != nil || t.ID == c.thread || t.ParentThreadID.Validate() != nil || t.SessionID.Validate() != nil || t.ModelProvider != c.execution.settings.Provider || t.CLIVersion != c.version || validateThreadStatus(t.Status) != nil {
				return Event{}, true, incompatible()
			}
			o, ok := c.subagents[string(t.ID)]
			if !ok {
				// A start notification may precede the original spawn evidence.
				// It cannot allocate product ownership; wait for the canonical
				// sender/receiver spawn or the validated descendant inventory.
				return c.childMetadata(), true, nil
			}
			if o.ParentID != string(*t.ParentThreadID) {
				return Event{}, true, incompatible()
			}
			o.Source, o.SourceID = domain.CodexHistorySource, string(t.ID)
			o.Usage, o.Output, o.ObservedModel = nil, nil, nil
			staged := map[string]string{}
			c.stageNickname(staged, t.ID, t.AgentNickname)
			e, err := c.childEvent([]domain.SubagentObservation{o}, staged)
			return e, true, err
		}
	}
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &fields) != nil {
		return Event{}, false, nil
	}
	var thread string
	_ = json.Unmarshal(fields["threadId"], &thread)
	o, known := c.subagents[thread]
	if !known {
		// Status may even precede thread/started. Retain an unresolved identity
		// until independent descendant inventory proves ancestry. This grants no
		// child ownership or settlement; unresolved identities block completion.
		if native.Method == "thread/status/changed" && domain.ID(thread).Validate() == nil && domain.ID(thread) != c.thread {
			// The exact {threadId,status} envelope is required: unknown
			// top-level parameters are rejected before any passive metadata
			// or unresolved-identity state is recorded.
			var envelope struct {
				ThreadID domain.ID    `json:"threadId"`
				Status   ThreadStatus `json:"status"`
			}
			if !requireExactKeys(native.Params, "threadId", "status") || decodeCaseExact(native.Params, &envelope) != nil || envelope.ThreadID != domain.ID(thread) || validateThreadStatus(envelope.Status) != nil {
				if c.logger != nil {
					c.logger.Warn("Codex child status envelope rejected", "owner_id", c.ownerID, "stage", "early-child-status")
				}
				return Event{}, true, incompatible()
			}
			status := envelope.Status
			if c.pendingChildStatuses == nil {
				c.pendingChildStatuses = make(map[domain.ID]struct{})
			}
			if _, exists := c.pendingChildStatuses[domain.ID(thread)]; !exists && len(c.pendingChildStatuses) >= 4096 {
				return Event{}, true, incompatible()
			}
			c.pendingChildStatuses[domain.ID(thread)] = struct{}{}
			if c.logger != nil {
				c.logger.Info("Codex child status preceded spawn evidence", "owner_id", c.ownerID, "status", status.Type)
			}
			return c.childMetadata(), true, nil
		}
		return Event{}, false, nil
	}
	o.Source = domain.CodexHistorySource
	o.Usage, o.Output, o.ObservedModel = nil, nil, nil
	switch native.Method {
	case "thread/settings/updated":
		var settings map[string]json.RawMessage
		if domain.Decode(fields["threadSettings"], &settings) != nil {
			return Event{}, true, incompatible()
		}
		var model, provider string
		if json.Unmarshal(settings["model"], &model) != nil || json.Unmarshal(settings["modelProvider"], &provider) != nil || provider != c.execution.settings.Provider || domain.Text(model, "observed child model", 256, true) != nil {
			return Event{}, true, incompatible()
		}
		o.ObservedModel = &model
		o.SourceID = thread
	case "turn/completed":
		var t struct {
			ThreadID domain.ID       `json:"threadId"`
			Turn     json.RawMessage `json:"turn"`
		}
		if decodeCaseExact(native.Params, &t) != nil {
			return Event{}, true, incompatible()
		}
		turn, err := decodeTurn(t.Turn)
		if err != nil || !turn.Status.terminal() || !childTurnKeysCanonical(t.Turn) {
			return Event{}, true, incompatible()
		}
		o.SourceID = string(turn.ID)
		switch {
		case o.Status == domain.SubagentShutdown:
			// A late turn notification cannot reopen an explicitly closed agent.
		case turn.Status == TurnCompleted:
			o.Status = domain.SubagentCompleted
		case turn.Status == TurnInterrupted:
			o.Status = domain.SubagentInterrupted
		case turn.Status == TurnFailed:
			o.Status = domain.SubagentFailed
		}
	case "item/started", "item/completed":
		var item map[string]json.RawMessage
		if domain.Decode(fields["item"], &item) != nil {
			return Event{}, true, incompatible()
		}
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		var turn domain.ID
		_ = json.Unmarshal(fields["turnId"], &turn)
		if kind == "collabAgentToolCall" {
			e, err := c.observeCollaboration(fields["item"], domain.ID(thread), turn)
			return e, true, err
		}
		if kind == "subAgentActivity" {
			e, err := c.observeSubagentActivity(fields["item"], turn)
			return e, true, err
		}
		if kind != "agentMessage" || native.Method != "item/completed" {
			return c.childMetadata(), true, nil
		}
		var v struct {
			Type      string            `json:"type"`
			ID        string            `json:"id"`
			Text      string            `json:"text"`
			Phase     *MessagePhase     `json:"phase"`
			Memory    json.RawMessage   `json:"memoryCitation,omitempty"`
			Delivery  json.RawMessage   `json:"delivery,omitempty"`
			Questions []json.RawMessage `json:"questions,omitempty"`
		}
		if decodeCaseExact(fields["item"], &v) != nil {
			return Event{}, true, incompatible()
		}
		// Omitted, null or empty questions carry no interaction metadata. Keep
		// populated questions and delivery private, as in the root decoder.
		if len(v.Questions) != 0 || (len(v.Delivery) > 0 && string(v.Delivery) != "null") {
			return privateNative(native), true, nil
		}
		o.SourceID = v.ID
		o.Output = &domain.SubagentOutput{NativeMessageID: v.ID, Text: v.Text, Partial: true}
	case "thread/tokenUsage/updated":
		var v struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
			Usage    struct {
				Total  tokenCountsWire `json:"total"`
				Last   tokenCountsWire `json:"last"`
				Window *int64          `json:"modelContextWindow"`
			} `json:"tokenUsage"`
		}
		if decodeCaseExact(native.Params, &v) != nil || (domain.NativeTokenUsage{Total: v.Usage.Total.normalized(), Last: v.Usage.Last.normalized(), ContextWindow: v.Usage.Window}).Validate() != nil {
			return Event{}, true, incompatible()
		}
		count := func(n *int64) *string {
			if n == nil {
				return nil
			}
			s := strconv.FormatInt(*n, 10)
			return &s
		}
		o.SourceID = string(v.TurnID)
		o.Usage = &domain.SubagentUsage{Scope: domain.SubagentCumulativeUsage, Total: count(v.Usage.Total.Total), Input: count(v.Usage.Total.Input), Output: count(v.Usage.Total.Output)}
		report, err := json.Marshal(v.Usage)
		if err != nil {
			return Event{}, true, incompatible()
		}
		o.Usage.NativeReport = string(report)
	default:
		// Other child event families are not projected as root content. Their
		// absence remains explicit in partial output/model/usage coverage.
		return c.childMetadata(), true, nil
	}
	e, err := c.childEvent([]domain.SubagentObservation{o}, nil)
	return e, true, err
}

func (c *Client) childMetadata() Event {
	return Event{Kind: MetadataEvent, ThreadID: c.thread, Correlated: true, Metadata: RawSupplementDiscarded}
}

func (c *Client) readChildHistory(ctx context.Context, child domain.SubagentObservation, id domain.ID) (domain.SubagentObservation, error) {
	response, err := c.call(ctx, domain.NewID(), "thread/turns/list", struct {
		Thread    domain.ID `json:"threadId"`
		Limit     int       `json:"limit"`
		Direction string    `json:"sortDirection"`
		View      string    `json:"itemsView"`
	}{id, 1, "desc", "full"})
	if err != nil {
		return child, err
	}
	if response.ErrorCode != nil {
		return child, nativeRejected(*response.ErrorCode)
	}
	var page struct {
		Data []json.RawMessage `json:"data"`
		Next *string           `json:"nextCursor"`
		Back *string           `json:"backwardsCursor"`
	}
	if decodeCaseExact(response.Result, &page) != nil || len(page.Data) > 1 {
		return child, incompatible()
	}
	if len(page.Data) == 0 {
		return child, nil
	}
	turn, err := decodeTurn(page.Data[0])
	if err != nil {
		return child, err
	}
	switch {
	case child.Status == domain.SubagentShutdown:
		// Stored turn completion predates native closeAgent lifecycle shutdown.
	case turn.Status == TurnRunning:
		if !child.Status.Terminal() {
			child.Status = domain.SubagentRunning
		}
	case turn.Status == TurnCompleted:
		child.Status = domain.SubagentCompleted
	case turn.Status == TurnFailed:
		child.Status = domain.SubagentFailed
	case turn.Status == TurnInterrupted:
		child.Status = domain.SubagentInterrupted
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(page.Data[0], &fields)
	var items []map[string]json.RawMessage
	if domain.Decode(fields["items"], &items) != nil || len(items) > 10000 {
		return child, incompatible()
	}
	for _, item := range items {
		var kind string
		_ = json.Unmarshal(item["type"], &kind)
		if kind != "agentMessage" {
			continue
		}
		var message, text string
		if json.Unmarshal(item["id"], &message) != nil || json.Unmarshal(item["text"], &text) != nil {
			return child, incompatible()
		}
		child.Output = &domain.SubagentOutput{NativeMessageID: message, Text: text, Partial: true}
	}
	return child, nil
}
