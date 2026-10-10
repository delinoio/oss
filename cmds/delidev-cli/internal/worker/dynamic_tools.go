// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type dynamicReplyJournal struct {
	Version            uint32                  `json:"version"`
	ServerID           domain.ID               `json:"server_id"`
	DeviceID           domain.ID               `json:"device_id"`
	InstanceID         domain.ID               `json:"instance_id"`
	JobID              domain.ID               `json:"job_id"`
	ExecutionID        domain.ID               `json:"execution_id"`
	AssignmentRevision uint64                  `json:"assignment_revision"`
	AssignmentDigest   string                  `json:"assignment_digest"`
	Reply              codex.DynamicReplyState `json:"reply"`
}

// Journals sit beside the original execution outbox under its existing lock.
// Retained intent never grants another native send, even after process restart.
func dynamicUnavailableRecorder(p *ExecutionPublisher) func(context.Context, codex.DynamicReplyState) error {
	return func(ctx context.Context, state codex.DynamicReplyState) error {
		if p == nil {
			return publicationUncertain()
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if ctx.Err() != nil || p.closed || p.release == nil || p.input.Configuration.Harness != domain.Codex || p.input.Configuration.SidechatPolicy != "" || state.ArrivalID.Validate() != nil || state.ThreadID.Validate() != nil || state.TurnID.Validate() != nil || domain.Text(state.CallID, "dynamic native call", 1024, true) != nil || domain.Text(state.RequestKey, "native request key", 256, true) != nil || len(state.RequestDigest) != 64 {
			return publicationUncertain()
		}
		if _, err := hex.DecodeString(state.RequestDigest); err != nil {
			return publicationUncertain()
		}
		key := sha256.Sum256([]byte(state.RequestKey))
		path := filepath.Join(filepath.Dir(p.path), "dynamic-reply-"+hex.EncodeToString(key[:])+".json")
		expected := dynamicReplyJournal{Version: 1, ServerID: p.state.ServerID, DeviceID: p.state.DeviceID, InstanceID: p.state.InstanceID, JobID: p.job, ExecutionID: p.execution, AssignmentRevision: p.state.Revision, AssignmentDigest: p.state.AssignmentDigest, Reply: state}
		raw, err := security.ReadPrivate(path, 16<<10)
		if errors.Is(err, os.ErrNotExist) {
			if state.Delivery != codex.DynamicReplyIntent || state.Resolved {
				return publicationUncertain()
			}
		} else {
			var prior dynamicReplyJournal
			if err != nil || domain.Decode(raw, &prior) != nil {
				return publicationUncertain()
			}
			oldState, newState := prior.Reply, expected.Reply
			oldState.Delivery, newState.Delivery = "", ""
			oldState.Resolved, newState.Resolved = false, false
			prior.Reply = expected.Reply
			if prior != expected || oldState != newState || state.Delivery == codex.DynamicReplyIntent || state.Delivery != codex.DynamicReplyTransmitted && state.Delivery != codex.DynamicReplyUncertain {
				return publicationUncertain()
			}
			var observed dynamicReplyJournal
			if domain.Decode(raw, &observed) != nil || observed.Reply.Resolved && !state.Resolved || observed.Reply.Delivery != codex.DynamicReplyIntent && observed.Reply.Delivery != state.Delivery {
				return publicationUncertain()
			}
		}
		if writeJSON(path, expected) != nil {
			return publicationUncertain()
		}
		return nil
	}
}
