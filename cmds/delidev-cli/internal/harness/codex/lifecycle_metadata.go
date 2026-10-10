// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Sources: openai/codex c1382380de69521303b416720a52f42d51af6248
// ServerNotification and its V2 metadata types; prediction/read-state types at
// a06545b311fe01e51ce855c7aa5d8da21e9e7aaf. Descriptors stay in this decoder.
type nativeMetadataChange string

const (
	nativeMetadataCreated nativeMetadataChange = "created"
	nativeMetadataUpdated nativeMetadataChange = "updated"
	nativeMetadataDeleted nativeMetadataChange = "deleted"
)

type nativePredictionStatus string

const (
	nativePredictionCompleted nativePredictionStatus = "completed"
	nativePredictionFailed    nativePredictionStatus = "failed"
)

type nativeUnreadPositionKind string

const (
	nativeUnreadThreadStart nativeUnreadPositionKind = "threadStart"
	nativeUnreadTurn        nativeUnreadPositionKind = "turn"
)

func lifecycleMetadataMethod(method string) bool {
	return slices.Contains([]string{"thread/archived", "thread/deleted", "thread/unarchived", "thread/closed", "thread/name/updated", "thread/attachment/updated", "thread/queue/changed", "project/changed", "thread/project/updated", "thread/environment/connected", "thread/environment/disconnected", "thread/prediction/updated", "thread/readState/changed", "thread/compacted", "thread/reverted"}, method)
}

func nativeMetadataFields(raw json.RawMessage, required, allowed []string) (map[string]json.RawMessage, bool) {
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &fields) != nil || fields == nil {
		return nil, false
	}
	for _, key := range required {
		if _, ok := fields[key]; !ok {
			return nil, false
		}
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return nil, false
		}
	}
	return fields, true
}

func nativeNullableMetadataText(raw json.RawMessage, limit int) bool {
	if string(raw) == "null" {
		return true
	}
	var text string
	return len(raw) > 0 && json.Unmarshal(raw, &text) == nil && domain.Text(text, "native private metadata", limit, false) == nil
}

func (c *Client) observeLifecycleMetadataLocked(native nativewire.Event) (Event, error) {
	fields, ok := nativeMetadataFields(native.Params, nil, []string{"threadId", "threadName", "attachmentType", "identityKey", "attachmentId", "operation", "projectId", "changeType", "environmentId", "sourceTurnId", "result", "readState", "turnId"})
	if !ok {
		return Event{}, incompatible()
	}
	var scope struct {
		ThreadID domain.ID `json:"threadId"`
	}
	// Project changes are scoped to this original process, not an external product
	// project. Every other family must name the original native root explicitly.
	if native.Method != "project/changed" {
		if json.Unmarshal(native.Params, &scope) != nil || scope.ThreadID.Validate() != nil {
			return Event{}, incompatible()
		}
	}
	kind := NativeThreadMetadataDiscarded
	valid := false
	switch native.Method {
	case "thread/archived", "thread/deleted", "thread/unarchived", "thread/closed", "thread/queue/changed", "thread/reverted":
		_, valid = nativeMetadataFields(native.Params, []string{"threadId"}, []string{"threadId"})
	case "thread/name/updated":
		_, valid = nativeMetadataFields(native.Params, []string{"threadId"}, []string{"threadId", "threadName"})
		if name, exists := fields["threadName"]; exists {
			var text string
			valid = valid && string(name) != "null" && json.Unmarshal(name, &text) == nil && domain.Text(text, "native thread name", 4096, false) == nil
		}
	case "thread/attachment/updated":
		var p struct {
			ThreadID  domain.ID            `json:"threadId"`
			Type      string               `json:"attachmentType"`
			Key       string               `json:"identityKey"`
			ID        string               `json:"attachmentId"`
			Operation nativeMetadataChange `json:"operation"`
		}
		valid = domain.Decode(native.Params, &p) == nil && domain.Text(p.Type, "native attachment type", 256, true) == nil && domain.Text(p.Key, "native attachment identity", 4096, true) == nil && domain.Text(p.ID, "native attachment ID", 1024, true) == nil && (p.Operation == nativeMetadataCreated || p.Operation == nativeMetadataDeleted)
	case "project/changed":
		var p struct {
			ProjectID string               `json:"projectId"`
			Change    nativeMetadataChange `json:"changeType"`
		}
		valid = domain.Decode(native.Params, &p) == nil && domain.Text(p.ProjectID, "native project ID", 1024, true) == nil && slices.Contains([]nativeMetadataChange{nativeMetadataCreated, nativeMetadataUpdated, nativeMetadataDeleted}, p.Change)
		kind = NativeProjectMetadataDiscarded
	case "thread/project/updated":
		_, valid = nativeMetadataFields(native.Params, []string{"threadId", "projectId"}, []string{"threadId", "projectId"})
		valid = valid && nativeNullableMetadataText(fields["projectId"], 1024)
	case "thread/environment/connected", "thread/environment/disconnected":
		var p struct {
			ThreadID      domain.ID `json:"threadId"`
			EnvironmentID string    `json:"environmentId"`
		}
		valid = domain.Decode(native.Params, &p) == nil && domain.Text(p.EnvironmentID, "native environment ID", 1024, true) == nil
	case "thread/prediction/updated":
		var p struct {
			ThreadID domain.ID       `json:"threadId"`
			Source   domain.ID       `json:"sourceTurnId"`
			Result   json.RawMessage `json:"result"`
		}
		if domain.Decode(native.Params, &p) != nil || p.Source.Validate() != nil {
			return Event{}, incompatible()
		}
		result, closed := nativeMetadataFields(p.Result, []string{"type"}, []string{"type", "text"})
		var status nativePredictionStatus
		if !closed || json.Unmarshal(result["type"], &status) != nil {
			return Event{}, incompatible()
		}
		switch status {
		case nativePredictionCompleted:
			valid = nativeNullableMetadataText(result["text"], nativewire.MaxFrame)
		case nativePredictionFailed:
			_, text := result["text"]
			valid = !text
		}
		// Prediction text is never input. Unknown owned source turns cannot inherit
		// a current-turn claim; foreign roots still retain their private extension.
		if scope.ThreadID == c.thread {
			if _, known := c.execution.turns[p.Source]; !known {
				return Event{}, incompatible()
			}
		}
	case "thread/readState/changed":
		var p struct {
			ThreadID  domain.ID       `json:"threadId"`
			ReadState json.RawMessage `json:"readState"`
		}
		if domain.Decode(native.Params, &p) != nil {
			return Event{}, incompatible()
		}
		state, closed := nativeMetadataFields(p.ReadState, []string{"firstUnread", "revision"}, []string{"firstUnread", "revision"})
		var revision string
		if !closed || string(state["revision"]) == "null" || json.Unmarshal(state["revision"], &revision) != nil || domain.Text(revision, "native read-state revision", 4096, true) != nil {
			return Event{}, incompatible()
		}
		valid = string(state["firstUnread"]) == "null"
		if !valid {
			position, closed := nativeMetadataFields(state["firstUnread"], []string{"type"}, []string{"type", "turnId"})
			var kind nativeUnreadPositionKind
			if !closed || json.Unmarshal(position["type"], &kind) != nil {
				return Event{}, incompatible()
			}
			_, hasTurn := position["turnId"]
			switch kind {
			case nativeUnreadThreadStart:
				valid = !hasTurn
			case nativeUnreadTurn:
				var turn domain.ID
				valid = hasTurn && json.Unmarshal(position["turnId"], &turn) == nil && turn.Validate() == nil
			}
		}
	case "thread/compacted":
		var p struct {
			ThreadID domain.ID `json:"threadId"`
			TurnID   domain.ID `json:"turnId"`
		}
		valid = domain.Decode(native.Params, &p) == nil && p.TurnID.Validate() == nil
		if valid && p.ThreadID == c.thread {
			owned := false
			if action := c.execution.compaction; action != nil {
				owned = p.TurnID == action.turnID || p.TurnID == action.source.TurnID
			}
			for key := range c.execution.compactionItems {
				if strings.HasPrefix(key, string(p.TurnID)+"/") {
					owned = true
					break
				}
			}
			if base := c.execution.contextBase; base != nil && base.validate() == nil {
				for _, record := range base.Records {
					if record.TurnID == p.TurnID {
						owned = true
						break
					}
				}
			}

			if !owned {
				return c.nativeLifecycleLoss()
			}
		}
	}
	if !valid {
		return Event{}, incompatible()
	}
	if native.Method != "project/changed" && scope.ThreadID != c.thread {
		return privateNative(native), nil
	}
	switch native.Method {
	case "thread/archived", "thread/deleted", "thread/closed":
		return c.nativeLifecycleLoss()
	case "thread/reverted":
		if c.execution.revertClaim.Validate() != nil {
			return c.nativeLifecycleLoss()
		}
		kind = NativeLifecycleSupplementDiscarded
	case "thread/compacted":
		kind = NativeLifecycleSupplementDiscarded
	}
	return c.metadata(kind), nil
}

func (c *Client) nativeLifecycleLoss() (Event, error) {
	// Never rewrite the original input/turn facts or infer native cleanup. Even a
	// later unarchive, passive snapshot or terminal item cannot clear this fence.
	if c.problem == nil {
		c.problem = domain.Fail(domain.RecoveryRequired, "The original native thread lifecycle requires reconciliation.", "Retain the original execution and independent cleanup evidence; do not replay input or native lifecycle operations.")
	}
	c.execution.paused = true
	event := c.metadata(NativeThreadLifecycleLost)
	event.Problem = c.problem
	return event, nil
}
