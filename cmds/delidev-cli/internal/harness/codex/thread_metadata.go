// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (c *Client) threadObservationLost() (Event, error) {
	// A native lifecycle observation does not prove original process cleanup or
	// mutate the product session. Retain the original turn and recovery fence.
	if c.problem == nil {
		c.problem = turnUncertain()
	}
	c.execution.paused = true
	return Event{}, c.problem
}

func (c *Client) observeThreadLifecycleLocked(native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID domain.ID       `json:"threadId"`
		TurnID   json.RawMessage `json:"turnId,omitempty"`
	}
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil {
		return Event{}, incompatible()
	}
	var compactedTurn domain.ID
	if native.Method == "thread/compacted" {
		if domain.Decode(params.TurnID, &compactedTurn) != nil || compactedTurn.Validate() != nil {
			return Event{}, incompatible()
		}
	} else if len(params.TurnID) > 0 {
		return Event{}, incompatible()
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	switch native.Method {
	case "thread/archived", "thread/deleted", "thread/closed":
		// This adapter has no thread archive/delete/close send owner. Closing the
		// original process remains independently reconciled by Client.Close.
		return c.threadObservationLost()
	case "thread/compacted":
		scoped := c.execution.compaction != nil && c.execution.compaction.turnID == compactedTurn
		for key := range c.execution.compactionItems {
			if strings.HasPrefix(key, string(compactedTurn)+"/") {
				scoped = true
			}
		}
		if c.execution.contextBase != nil {
			scoped = scoped || slices.ContainsFunc(c.execution.contextBase.Records, func(r ContextRecord) bool { return r.TurnID == compactedTurn })
		}
		if !scoped {
			return c.threadObservationLost()
		}
		return c.metadata(ThreadContextSupplementDiscarded), nil
	case "thread/reverted":
		if c.execution.revertAction == "" || c.execution.active != "" || len(c.execution.pending) != 0 || c.execution.compaction != nil {
			return c.threadObservationLost()
		}
		return c.metadata(ThreadContextSupplementDiscarded), nil
	default:
		return c.metadata(ThreadMetadataDiscarded), nil
	}
}

// Descriptors are decoded privately and discarded, never copied to Event or a
// product binding. Each union uses its own closed shape, including nullable
// versus required fields; an observation cannot trigger a native read.
type nativeUnreadPosition string

const (
	nativeUnreadThreadStart nativeUnreadPosition = "threadStart"
	nativeUnreadTurn        nativeUnreadPosition = "turn"
)

type nativeProjectChange string

const (
	nativeProjectCreated nativeProjectChange = "created"
	nativeProjectUpdated nativeProjectChange = "updated"
	nativeProjectDeleted nativeProjectChange = "deleted"
)

type nativeAttachmentOperation string

const (
	nativeAttachmentCreated nativeAttachmentOperation = "created"
	nativeAttachmentDeleted nativeAttachmentOperation = "deleted"
)

type nativePredictionType string

const (
	nativePredictionCompleted nativePredictionType = "completed"
	nativePredictionFailed    nativePredictionType = "failed"
)

func nativeDescriptor(value string, maximum int) bool {
	return domain.Text(value, "private native descriptor", maximum, true) == nil
}
func (c *Client) observeThreadMetadataLocked(native nativewire.Event) (Event, error) {
	var thread domain.ID
	switch native.Method {
	case "thread/environment/connected", "thread/environment/disconnected":
		var p struct {
			ThreadID      domain.ID `json:"threadId"`
			EnvironmentID string    `json:"environmentId"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || !nativeDescriptor(p.EnvironmentID, 1024) {
			return Event{}, incompatible()
		}
		thread = p.ThreadID
	case "thread/readState/changed":
		var p struct {
			ThreadID  domain.ID `json:"threadId"`
			ReadState *struct {
				Revision    string          `json:"revision"`
				FirstUnread json.RawMessage `json:"firstUnread"`
			} `json:"readState"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.ReadState == nil || !nativeDescriptor(p.ReadState.Revision, 1024) {
			return Event{}, incompatible()
		}
		raw := p.ReadState.FirstUnread
		if len(raw) > 0 && string(raw) != "null" {
			var position struct {
				Type   nativeUnreadPosition `json:"type"`
				TurnID json.RawMessage      `json:"turnId"`
			}
			if domain.Decode(raw, &position) != nil {
				return Event{}, incompatible()
			}
			switch position.Type {
			case nativeUnreadThreadStart:
				if len(position.TurnID) > 0 {
					return Event{}, incompatible()
				}
			case nativeUnreadTurn:
				var turn domain.ID
				if domain.Decode(position.TurnID, &turn) != nil || turn.Validate() != nil {
					return Event{}, incompatible()
				}
			default:
				return Event{}, incompatible()
			}
		}
		thread = p.ThreadID
	case "thread/name/updated":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			Name     *string   `json:"threadName"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.Name != nil && domain.Text(*p.Name, "private native name", 4096, false) != nil {
			return Event{}, incompatible()
		}
		thread = p.ThreadID
	case "thread/attachment/updated":
		var p struct {
			ThreadID       domain.ID                 `json:"threadId"`
			AttachmentID   string                    `json:"attachmentId"`
			AttachmentType string                    `json:"attachmentType"`
			IdentityKey    string                    `json:"identityKey"`
			Operation      nativeAttachmentOperation `json:"operation"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || !nativeDescriptor(p.AttachmentID, 1024) || !nativeDescriptor(p.AttachmentType, 1024) || !nativeDescriptor(p.IdentityKey, 4096) || p.Operation != nativeAttachmentCreated && p.Operation != nativeAttachmentDeleted {
			return Event{}, incompatible()
		}
		thread = p.ThreadID
	case "project/changed":
		var p struct {
			ProjectID string              `json:"projectId"`
			Change    nativeProjectChange `json:"changeType"`
		}
		if domain.Decode(native.Params, &p) != nil || !nativeDescriptor(p.ProjectID, 1024) || p.Change != nativeProjectCreated && p.Change != nativeProjectUpdated && p.Change != nativeProjectDeleted {
			return Event{}, incompatible()
		}
		// Process scope, not ownership of this external project. Do not enumerate it.
		return c.metadata(ThreadMetadataDiscarded), nil
	case "thread/project/updated":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			Project  json.RawMessage `json:"projectId"`
		}
		var project *string
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || len(p.Project) == 0 || domain.Decode(p.Project, &project) != nil || project != nil && !nativeDescriptor(*project, 1024) {
			return Event{}, incompatible()
		}
		thread = p.ThreadID
	case "thread/prediction/updated":
		var p struct {
			ThreadID     domain.ID       `json:"threadId"`
			SourceTurnID domain.ID       `json:"sourceTurnId"`
			Result       json.RawMessage `json:"result"`
		}
		if domain.Decode(native.Params, &p) != nil || p.ThreadID.Validate() != nil || p.SourceTurnID.Validate() != nil {
			return Event{}, incompatible()
		}
		var tag struct {
			Type nativePredictionType `json:"type"`
			Text json.RawMessage      `json:"text"`
		}
		if domain.Decode(p.Result, &tag) != nil {
			return Event{}, incompatible()
		}
		switch tag.Type {
		case nativePredictionCompleted:
			var text *string
			if len(tag.Text) > 0 && (domain.Decode(tag.Text, &text) != nil || text != nil && domain.Text(*text, "private native prediction", 1<<20, false) != nil) {
				return Event{}, incompatible()
			}
		case nativePredictionFailed:
			if len(tag.Text) > 0 {
				return Event{}, incompatible()
			}
		default:
			return Event{}, incompatible()
		}
		if p.ThreadID != c.thread {
			return privateNative(native), nil
		}
		if _, known := c.execution.turns[p.SourceTurnID]; !known {
			return c.threadObservationLost()
		}
		thread = p.ThreadID
	default:
		return privateNative(native), nil
	}
	if thread != c.thread {
		return privateNative(native), nil
	}
	return c.metadata(ThreadMetadataDiscarded), nil
}
