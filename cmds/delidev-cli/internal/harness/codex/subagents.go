package codex

import (
	"context"
	"encoding/json"
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

func (c *Client) childEvent(batch []domain.SubagentObservation) (Event, error) {
	state := domain.SubagentState{}
	for id, o := range c.subagents {
		state[id] = domain.SubagentOwner{ID: o.ID, ParentID: o.ParentID, Status: o.Status}
	}
	if _, err := domain.ApplySubagents(state, string(c.thread), batch); err != nil {
		return Event{}, incompatible()
	}
	if c.subagents == nil {
		c.subagents = map[string]domain.SubagentObservation{}
	}
	for _, o := range batch {
		c.subagents[o.NativeID] = o
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
	return Event{Kind: SubagentEvent, ThreadID: c.thread, TurnID: turn, Correlated: true, Subagents: batch}, nil
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
	if domain.Decode(item, &v) != nil || v.Type != "collabAgentToolCall" || v.Sender != sender || domain.Text(v.ID, "native collab item", 1024, true) != nil || !slices.Contains([]string{"spawnAgent", "sendInput", "resumeAgent", "wait", "closeAgent", "sendMessage", "followupTask", "interruptAgent", "listAgents"}, v.Tool) || !slices.Contains([]string{"inProgress", "completed", "failed", "interrupted"}, v.Status) || len(v.Receivers) > 128 {
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
		} else if v.Tool == "spawnAgent" && o.ParentID != string(sender) {
			return Event{}, incompatible()
		}
		if v.Tool == "spawnAgent" && o.RequestedModel == nil {
			o.RequestedModel = v.Model
		}
		o.Source, o.SourceID = domain.CodexCollaborationSource, v.ID
		o.Usage = nil
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
	return c.childEvent(batch)
}

func (c *Client) observeSubagentActivity(item json.RawMessage, turn domain.ID) (Event, error) {
	var v struct {
		Type  string    `json:"type"`
		ID    string    `json:"id"`
		Kind  string    `json:"kind"`
		Agent domain.ID `json:"agentThreadId"`
		Path  string    `json:"agentPath"`
	}
	if domain.Decode(item, &v) != nil || v.Type != "subAgentActivity" || v.Agent.Validate() != nil || domain.Text(v.Path, "native agent path", 1024, true) != nil {
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
	return c.childEvent([]domain.SubagentObservation{child})
}

// InspectDescendants uses only read operations. It does not load/resume children
// or reuse their direct-input capability. All pages validate before publication.
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
	batch := []domain.SubagentObservation{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 16; page++ {
		response, err := c.wire.Call(ctx, domain.NewID(), "thread/list", struct {
			Ancestor domain.ID `json:"ancestorThreadId"`
			Limit    int       `json:"limit"`
			Cursor   string    `json:"cursor,omitempty"`
			Sources  []string  `json:"sourceKinds"`
		}{c.thread, 128, cursor, []string{"subAgent", "subAgentThreadSpawn", "subAgentOther"}})
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
		if domain.Decode(response.Result, &result) != nil || result.Data == nil {
			return Event{}, incompatible()
		}
		for _, raw := range result.Data {
			var t threadWire
			if domain.Decode(raw, &t) != nil || t.ID.Validate() != nil || t.ParentThreadID == nil || t.ParentThreadID.Validate() != nil || t.SessionID != c.execution.thread.SessionID || t.CLIVersion != SupportedVersion || t.ModelProvider != c.execution.settings.Provider || validateThreadStatus(t.Status) != nil || seen[string(t.ID)] {
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
			o.Usage = nil
			o, err = c.readChildHistory(ctx, o, t.ID)
			if err != nil {
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
		return Event{Kind: MetadataEvent, ThreadID: c.thread, Correlated: true, Metadata: RawSupplementDiscarded}, nil
	}
	return c.childEvent(batch)
}

func (c *Client) observeChildNative(native nativewire.Event) (Event, bool, error) {
	if native.Method == "thread/started" {
		var v struct {
			Thread threadWire `json:"thread"`
		}
		if domain.Decode(native.Params, &v) == nil && v.Thread.ParentThreadID != nil {
			t := v.Thread
			if t.ID.Validate() != nil || t.ParentThreadID.Validate() != nil || t.SessionID != c.execution.thread.SessionID || t.ModelProvider != c.execution.settings.Provider || t.CLIVersion != SupportedVersion || validateThreadStatus(t.Status) != nil {
				return Event{}, true, incompatible()
			}
			if *t.ParentThreadID != c.thread {
				if _, ok := c.subagents[string(*t.ParentThreadID)]; !ok {
					return Event{}, true, incompatible()
				}
			}
			o, ok := c.subagents[string(t.ID)]
			if ok && o.ParentID != string(*t.ParentThreadID) {
				return Event{}, true, incompatible()
			}
			if !ok {
				o = domain.SubagentObservation{ID: domain.NewID(), NativeID: string(t.ID), ParentID: string(*t.ParentThreadID), Status: domain.SubagentPending, Source: domain.CodexHistorySource, SourceID: string(t.ID)}
			}
			o.Source, o.SourceID, o.Usage = domain.CodexHistorySource, string(t.ID), nil
			e, err := c.childEvent([]domain.SubagentObservation{o})
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
		return Event{}, false, nil
	}
	o.Source = domain.CodexHistorySource
	o.Usage = nil
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
		if domain.Decode(native.Params, &t) != nil {
			return Event{}, true, incompatible()
		}
		turn, err := decodeTurn(t.Turn)
		if err != nil || !turn.Status.terminal() {
			return Event{}, true, incompatible()
		}
		o.SourceID = string(turn.ID)
		switch turn.Status {
		case TurnCompleted:
			o.Status = domain.SubagentCompleted
		case TurnInterrupted:
			o.Status = domain.SubagentInterrupted
		case TurnFailed:
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
			Type   string          `json:"type"`
			ID     string          `json:"id"`
			Text   string          `json:"text"`
			Phase  *MessagePhase   `json:"phase"`
			Memory json.RawMessage `json:"memoryCitation,omitempty"`
		}
		if domain.Decode(fields["item"], &v) != nil {
			return Event{}, true, incompatible()
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
		if domain.Decode(native.Params, &v) != nil || (domain.NativeTokenUsage{Total: v.Usage.Total.normalized(), Last: v.Usage.Last.normalized(), ContextWindow: v.Usage.Window}).Validate() != nil {
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
	e, err := c.childEvent([]domain.SubagentObservation{o})
	return e, true, err
}

func (c *Client) childMetadata() Event {
	return Event{Kind: MetadataEvent, ThreadID: c.thread, Correlated: true, Metadata: RawSupplementDiscarded}
}

func (c *Client) readChildHistory(ctx context.Context, child domain.SubagentObservation, id domain.ID) (domain.SubagentObservation, error) {
	response, err := c.wire.Call(ctx, domain.NewID(), "thread/turns/list", struct {
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
	if domain.Decode(response.Result, &page) != nil || len(page.Data) > 1 {
		return child, incompatible()
	}
	if len(page.Data) == 0 {
		return child, nil
	}
	turn, err := decodeTurn(page.Data[0])
	if err != nil {
		return child, err
	}
	switch turn.Status {
	case TurnRunning:
		if !child.Status.Terminal() {
			child.Status = domain.SubagentRunning
		}
	case TurnCompleted:
		child.Status = domain.SubagentCompleted
	case TurnFailed:
		child.Status = domain.SubagentFailed
	case TurnInterrupted:
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
