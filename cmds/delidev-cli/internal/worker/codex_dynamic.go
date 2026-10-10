package worker

import (
	"context"
	"path/filepath"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type dynamicReplyJournal struct {
	Version          uint32                  `json:"version"`
	JobID            domain.ID               `json:"job_id"`
	ServerID         domain.ID               `json:"server_id"`
	MachineID        domain.ID               `json:"machine_id"`
	DeviceID         domain.ID               `json:"device_id"`
	InstanceID       domain.ID               `json:"instance_id"`
	ExecutionID      domain.ID               `json:"execution_id"`
	AssignmentDigest string                  `json:"assignment_digest"`
	Reply            codex.DynamicReplyState `json:"reply"`
}
type nativeDynamicResponder interface {
	ReplyDynamicUnavailable(context.Context, domain.ID, func(codex.DynamicReplyState) error) (codex.DynamicReplyState, error)
}

// A private original request journal precedes the native reply. The public
// lifecycle uses the original content outbox, whose replay has no responder.
func (c *CodexEventPublisher) handleDynamic(ctx context.Context, event codex.Event, client nativeDynamicResponder) (returned error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	defer func() {
		if returned != nil {
			c.blocked = true
		}
	}()
	if c.publisher == nil || c.publisher.input.Configuration.SidechatPolicy != "" || c.blocked || c.finished && event.Kind != codex.DynamicResolvedEvent || !event.Correlated || event.Late && event.Kind != codex.DynamicResolvedEvent || event.ThreadID != c.thread || event.TurnID != c.turn {
		return publicationUncertain()
	}
	config := c.publisher.config
	if event.Kind == codex.DynamicResolvedEvent {
		if event.DynamicReply == nil || event.DynamicRequest != nil {
			return publicationUncertain()
		}
		state := *event.DynamicReply
		journal, ok := c.dynamicReplies[state.Request.ID]
		if !ok || !reflect.DeepEqual(journal.Reply.Request, state.Request) || journal.Reply.Stage != state.Stage || state.Closure != codex.InteractionNativeClosed {
			return publicationUncertain()
		}
		raw, readErr := security.ReadPrivate(c.dynamicPath(state.Request.ID), 64<<10)
		var retained dynamicReplyJournal
		if readErr != nil || domain.Decode(raw, &retained) != nil || !reflect.DeepEqual(retained, journal) {
			return publicationUncertain()
		}
		journal.Reply = state
		if err := writeJSON(c.dynamicPath(state.Request.ID), journal); err != nil {
			return publicationUncertain()
		}
		c.dynamicReplies[state.Request.ID] = journal
		return nil
	}
	request := event.DynamicRequest
	if event.Kind != codex.DynamicRequestedEvent || request == nil || request.Validate() != nil || request.ThreadID != c.thread || request.TurnID != c.turn || request.CallID != event.ItemID || request.Namespace != nil || client == nil || len(c.dynamicReplies) >= domain.MaxExecutionInteractions {
		return publicationUncertain()
	}
	if _, exists := c.dynamicReplies[request.ID]; exists {
		return publicationUncertain()
	}
	// Exclusive directory claim is synchronized by security before the journal;
	// a crash at either boundary stays inspection-only, never recreated for send.
	directory := filepath.Dir(c.dynamicPath(request.ID))
	if err := security.PrivateDir(filepath.Dir(directory)); err != nil {
		return publicationUncertain()
	}
	if err := security.CreatePrivateDirExclusive(directory); err != nil {
		return publicationUncertain()
	}
	journal := dynamicReplyJournal{Version: 1, JobID: c.publisher.job, ServerID: config.Credential.ServerID, MachineID: config.Credential.MachineID, DeviceID: config.Credential.DeviceID, InstanceID: config.Instance, ExecutionID: c.publisher.execution, AssignmentDigest: c.publisher.state.AssignmentDigest, Reply: codex.DynamicReplyState{Request: *request, Stage: codex.DynamicObserved, Closure: codex.InteractionOpen}}
	path := c.dynamicPath(request.ID)
	if err := writeJSON(path, journal); err != nil {
		return publicationUncertain()
	}
	c.dynamicReplies[request.ID] = journal
	_, err := client.ReplyDynamicUnavailable(ctx, request.ID, func(state codex.DynamicReplyState) error {
		if !reflect.DeepEqual(state.Request, journal.Reply.Request) || state.Closure != codex.InteractionOpen {
			return publicationUncertain()
		}
		// Original identity must still match the retained private file. No adoption
		// or substitution is permitted even if a replacement contains valid IDs.
		raw, readErr := security.ReadPrivate(path, 64<<10)
		var retained dynamicReplyJournal
		if readErr != nil || domain.Decode(raw, &retained) != nil || !reflect.DeepEqual(retained, journal) {
			return publicationUncertain()
		}
		journal.Reply = state
		if err := writeJSON(path, journal); err != nil {
			return err
		}
		c.dynamicReplies[request.ID] = journal
		return nil
	})
	if err != nil {
		c.blocked = true
		return err
	}
	return nil
}
func (c *CodexEventPublisher) dynamicPath(id domain.ID) string {
	return filepath.Join(c.publisher.config.Root, "jobs", string(c.publisher.job), "dynamic-responses", string(id), "reply.json")
}

// Turn closure is retained independently of item completion and transmission.
func (c *CodexEventPublisher) endDynamicReplies() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, journal := range c.dynamicReplies {
		if journal.Reply.Closure != codex.InteractionOpen {
			continue
		}
		raw, err := security.ReadPrivate(c.dynamicPath(id), 64<<10)
		var retained dynamicReplyJournal
		if err != nil || domain.Decode(raw, &retained) != nil || !reflect.DeepEqual(retained, journal) {
			return publicationUncertain()
		}
		journal.Reply.Closure = codex.InteractionTurnEnded
		if err := writeJSON(c.dynamicPath(id), journal); err != nil {
			return publicationUncertain()
		}
		c.dynamicReplies[id] = journal
	}
	return nil
}
