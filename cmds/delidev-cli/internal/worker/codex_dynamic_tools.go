// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// This private journal binds the original actor and accepted assignment, not a
// recoverable public request. Existing intent is a permanent no-resend fence.
type dynamicToolIntent struct {
	ReplyID            domain.ID               `json:"reply_id,omitempty"`
	Version            uint32                  `json:"version"`
	Actor              domain.DeviceType       `json:"actor"`
	ServerID           domain.ID               `json:"server_id"`
	DeviceID           domain.ID               `json:"device_id"`
	MachineID          domain.ID               `json:"machine_id"`
	InstanceID         domain.ID               `json:"instance_id"`
	SessionID          domain.ID               `json:"session_id"`
	JobID              domain.ID               `json:"job_id"`
	ExecutionID        domain.ID               `json:"execution_id"`
	AssignmentRevision uint64                  `json:"assignment_revision"`
	AssignmentDigest   string                  `json:"assignment_digest"`
	ThreadID           domain.ID               `json:"thread_id"`
	TurnID             domain.ID               `json:"turn_id"`
	Original           json.RawMessage         `json:"original"`
	OriginalRequestID  json.RawMessage         `json:"original_request_id,omitempty"`
	OriginalWireToken  domain.ID               `json:"original_wire_token,omitempty"`
	Observation        domain.CodexDynamicTool `json:"observation"`
	ReplyDigest        string                  `json:"reply_digest,omitempty"`
}

func dynamicPrivateDigest(raw []byte) string {
	value := sha256.Sum256(raw)
	return hex.EncodeToString(value[:])
}
func exclusiveDynamicIntent(path string, raw []byte) error {
	// O_EXCL deliberately retains even an incomplete file after a failed sync.
	// Replacing or removing that fence could silently repeat a native attempt.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return publicationUncertain()
	}
	_, writeErr := file.Write(raw)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || security.SyncParent(path) != nil {
		return publicationUncertain()
	}
	return nil
}
func (c *CodexEventPublisher) retainDynamicOriginal(event codex.Event) (string, dynamicToolIntent, error) {
	p := c.publisher
	var intent dynamicToolIntent
	if p == nil || p.input.Configuration.Harness != domain.Codex || p.input.Configuration.SidechatPolicy != "" || event.DynamicTool == nil || event.DynamicTool.Observation.Validate() != nil || !event.Correlated || event.Late && event.Kind != codex.DynamicToolObservedEvent || event.ThreadID != c.thread || event.TurnID != c.turn {
		return "", intent, publicationUncertain()
	}
	directory := filepath.Join(p.config.Root, "jobs", string(p.job), "dynamic-tools")
	if security.PrivateDir(directory) != nil {
		return "", intent, publicationUncertain()
	}
	original := event.DynamicTool.OriginalJSON
	if len(original) == 0 || len(original) > 4<<20 {
		return "", intent, publicationUncertain()
	}
	intent = dynamicToolIntent{Version: 1, Actor: p.config.Credential.Type, ServerID: p.config.Credential.ServerID, DeviceID: p.config.Credential.DeviceID, MachineID: p.config.Credential.MachineID, InstanceID: p.config.Instance, SessionID: p.input.SessionID, JobID: p.job, ExecutionID: p.execution, AssignmentRevision: p.state.Revision, AssignmentDigest: p.state.AssignmentDigest, ThreadID: event.ThreadID, TurnID: event.TurnID, Original: bytes.Clone(original), Observation: event.DynamicTool.Observation}
	name := "observation-" + dynamicPrivateDigest(original) + ".json"
	if event.Kind == codex.DynamicToolRequestedEvent {
		native := event.DynamicTool.OriginalRequest
		if native == nil || native.Kind != nativewire.ServerRequest || native.Method != "item/tool/call" || native.Token != intent.Observation.ArrivalID || len(native.ID) == 0 || !bytes.Equal(native.Params, original) {
			return "", intent, publicationUncertain()
		}
		intent.OriginalRequestID = bytes.Clone(native.ID)
		intent.OriginalWireToken = native.Token
		intent.ReplyID = domain.NewID()
		// The original request is retained while the separate send state below is
		// private. A prepared journal is not a successful native response.
		intent.ReplyDigest = dynamicPrivateDigest([]byte(`{"success":false,"contentItems":[{"type":"inputText","text":"This dynamic tool is unavailable in DeliDev."}]}`))
		name = "request-" + dynamicPrivateDigest([]byte(string(event.TurnID)+"\x00"+intent.Observation.CallID)) + ".json"
	}
	path := filepath.Join(directory, name)
	if _, err := os.Lstat(path); err == nil {
		if event.Kind == codex.DynamicToolRequestedEvent {
			return "", intent, publicationUncertain()
		}
		existing, err := security.ReadPrivate(path, 5<<20)
		var retained dynamicToolIntent
		if err != nil || domain.DecodeBounded(existing, &retained, 5<<20) != nil || retained.JobID != intent.JobID || retained.DeviceID != intent.DeviceID || retained.InstanceID != intent.InstanceID || retained.ServerID != intent.ServerID || retained.ExecutionID != intent.ExecutionID || retained.AssignmentDigest != intent.AssignmentDigest || !bytes.Equal(retained.Original, intent.Original) {
			return "", intent, publicationUncertain()
		}
		return path, retained, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", intent, publicationUncertain()
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) >= 4096 {
		return "", intent, publicationUncertain()
	}
	size := int64(len(original))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return "", intent, publicationUncertain()
		}
		size += info.Size()
	}
	if size > 8<<20 {
		return "", intent, publicationUncertain()
	}
	raw, err := json.Marshal(intent)
	if err != nil {
		return "", intent, publicationUncertain()
	}
	if err := exclusiveDynamicIntent(path, raw); err != nil {
		return "", intent, err
	}
	return path, intent, nil
}
func (c *CodexEventPublisher) publishDynamic(ctx context.Context, event codex.Event) error {
	if event.DynamicTool == nil || event.DynamicTool.Observation.Validate() != nil {
		return publicationUncertain()
	}
	if len(event.DynamicTool.OriginalJSON) > 0 {
		if _, _, err := c.retainDynamicOriginal(event); err != nil {
			return err
		}
	}
	value := event.DynamicTool.Observation
	return c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionCodexDynamicToolObserved, CodexDynamicTool: &value})
}

// HandleDynamicRequest owns the private write-ahead barrier. It never adopts a
// tool, resolves a product question or retries any retained native send.
type dynamicNegativeResponder interface {
	InspectDynamicTool(context.Context, domain.ID) (codex.Event, error)
	RejectDynamicTool(context.Context, domain.ID, domain.ID, domain.ID) (codex.DynamicTool, error)
}

func (c *CodexEventPublisher) HandleDynamicRequest(ctx context.Context, client dynamicNegativeResponder, event codex.Event, supported bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !supported || c.blocked || c.finished || client == nil || event.Kind != codex.DynamicToolRequestedEvent {
		return publicationUncertain()
	}
	if event.DynamicTool == nil {
		return publicationUncertain()
	}
	original, err := client.InspectDynamicTool(ctx, event.DynamicTool.Observation.ArrivalID)
	if err != nil || original.DynamicTool == nil || original.DynamicTool.Observation.Stage != domain.DynamicToolRequested || original.ThreadID != event.ThreadID || original.TurnID != event.TurnID || original.ItemID != event.ItemID {
		return publicationUncertain()
	}
	event = original
	path, intent, err := c.retainDynamicOriginal(event)
	if err != nil {
		c.blocked = true
		return err
	}
	requested := event.DynamicTool.Observation
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionCodexDynamicToolObserved, CodexDynamicTool: &requested}); err != nil {
		return err
	}
	// Synchronize send-started before the only native invocation. Cancellation,
	// lost acknowledgment and process restart retain this fence without resend.
	intent.Observation.ResponseID = intent.ReplyID
	intent.Observation.Stage = domain.DynamicToolReplied
	intent.Observation.Delivery = domain.DynamicSendStarted
	negative := false
	intent.Observation.NegativeOutcome = &negative
	if err := writeJSON(path, intent); err != nil {
		c.blocked = true
		return publicationUncertain()
	}
	started := intent.Observation
	started.ID = domain.NewID()
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionCodexDynamicToolObserved, CodexDynamicTool: &started}); err != nil {
		return err
	}
	result, sendErr := client.RejectDynamicTool(ctx, intent.OriginalWireToken, intent.Observation.ResponseID, event.TurnID)
	if result.Observation.ID == "" {
		result.Observation = started
		result.Observation.ID = domain.NewID()
		result.Observation.Delivery = domain.DynamicUncertain
	}
	intent.Observation = result.Observation
	if err := writeJSON(path, intent); err != nil {
		c.blocked = true
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionCodexDynamicToolObserved, CodexDynamicTool: &result.Observation}); err != nil {
		return err
	}
	if p := c.publisher; p.config.Logger != nil {
		p.config.Logger.InfoContext(ctx, "native_dynamic_negative_reply_recorded", "job_id", p.job, "arrival_id", intent.OriginalWireToken, "delivery", result.Observation.Delivery)
	}
	if sendErr != nil {
		c.blocked = true
		return publicationUncertain()
	}
	return nil
}
