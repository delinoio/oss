package worker

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

// PublishUsageObservation follows the corresponding original content publication.
// It retains overlapping native reports, never additive usage or inferred costs.
// The shared durable outbox owns both paths; ReplayPending repeats no native I/O.
func (c *ClaudeContentPublisher) PublishUsageObservation(ctx context.Context, o claude.LifecycleObservation) (bool, error) {
	b := c.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := c.verify(); err != nil {
		return false, err
	}
	var value domain.ClaudeUsageObservation
	value.NativeEventID = o.NativeID
	if o.Kind == claude.ContentObserved {
		if len(o.Content) != 1 {
			return true, b.block()
		}
		native := o.Content[0]
		if native.Usage == nil {
			return false, nil
		}
		owner, ok := c.messages[native.MessageID]
		if !ok || !c.seen[o.NativeID] || owner.state != domain.MessageStreaming || native.ParentToolID != "" || native.Model != b.publisher.input.Configuration.NativeModel {
			return true, b.block()
		}
		value.MessageID, value.NativeMessageID, value.Model = owner.id, native.MessageID, native.Model
		switch native.Kind {
		case claude.ProviderMessageStarted:
			value.Source = domain.ClaudeMessageStartUsage
		case claude.ContentCompleted:
			value.Source, value.Index = domain.ClaudeBlockCompleteUsage, native.Index
		case claude.ProviderMessageUpdated:
			value.Source = domain.ClaudeMessageMetadataUsage
		default:
			return true, b.block()
		}
		if err := convertClaudeUsage(native.Usage, &value.Provider); err != nil {
			return true, b.block()
		}
	} else if o.Kind == claude.InputFinished {
		if o.Result == nil || o.Result.Usage == nil || c.active != "" || !c.toolsComplete() || !c.interactionsSettled() || c.resultUsage {
			return true, b.block()
		}
		value.Source = domain.ClaudeInputResultUsage
		if err := convertClaudeUsage(o.Result.Usage, &value.Result); err != nil {
			return true, b.block()
		}
	} else {
		return false, nil
	}
	if !c.inputPublished || o.SessionID != b.journal.SessionID || o.InputID != b.journal.InputID || o.TurnID != b.turn || !o.Accepted || c.usageSeen[o.NativeID] || len(c.usageSeen) >= 65536 || value.Validate() != nil {
		return true, b.block()
	}
	c.usageSeen[o.NativeID] = true
	if value.Source == domain.ClaudeInputResultUsage {
		c.resultUsage = true
		r := *o.Result
		r.Usage = nil
		if r.Origin != nil {
			origin := *r.Origin
			r.Origin = &origin
		}
		c.resultBoundary = &r
	}
	c.queue = []claudeContentCommit{{event: domain.ExecutionEvent{Kind: domain.ExecutionClaudeUsageObserved, ObservationID: domain.NewID(), ClaudeUsage: &value}}}
	return true, c.drain(ctx)
}

// This conversion is restricted to the independently typed native usage graph.
// UseNumber preserves every original integer and decimal spelling before the
// public string-counter schema is independently validated. No native event body,
// content, credentials or arbitrary external metadata can enter this boundary.
func convertClaudeUsage(native any, target any) error {
	raw, err := json.Marshal(native)
	if err != nil || len(raw) > 1<<20 {
		return publicationUncertain()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return publicationUncertain()
	}
	var exact func(any) any
	exact = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			return x.String()
		case map[string]any:
			for k, item := range x {
				x[k] = exact(item)
			}
			return x
		case []any:
			for i, item := range x {
				x[i] = exact(item)
			}
			return x
		default:
			return v
		}
	}
	raw, err = json.Marshal(exact(value))
	if err != nil {
		return publicationUncertain()
	}
	return domain.Decode(raw, target)
}
