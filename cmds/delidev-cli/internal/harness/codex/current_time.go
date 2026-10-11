// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Source: openai/codex a06545b311fe01e51ce855c7aa5d8da21e9e7aaf,
// app-server-protocol/src/protocol/v2/current_time.rs. This experimental request
// is established by the emitted Rust protocol, not a numeric version gate.
type currentTimeResponse struct {
	CurrentTimeAt int64 `json:"currentTimeAt"`
}

func (c *Client) replyCurrentTimeLocked(ctx context.Context, native nativewire.Event) (Event, error) {
	if native.Kind != nativewire.ServerRequest || native.Method != "currentTime/read" || c.execution == nil {
		return privateNative(native), nil
	}
	var params struct {
		ThreadID domain.ID `json:"threadId"`
	}
	var fields map[string]json.RawMessage
	if domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || json.Unmarshal(native.Params, &fields) != nil || len(fields) != 1 || fields["threadId"] == nil || native.Token.Validate() != nil {
		return Event{}, incompatible()
	}
	if _, err := decodeNativeRequestID(native.ID); err != nil {
		return Event{}, err
	}
	if params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	if c.wire == nil || c.problem != nil || c.execution.paused || c.execution.thread.ID != c.thread {
		return Event{}, turnUncertain()
	}
	clock := c.currentTimeClock
	if clock == nil {
		clock = time.Now
	}
	// The original connection claims this arrival before the one clock sample.
	// Unix returns whole seconds independently of timezone and emittedAtMs.
	err := c.wire.ReplyGenerated(ctx, native, func() (any, error) {
		return currentTimeResponse{CurrentTimeAt: clock().Unix()}, nil
	})
	if c.logger != nil {
		delivery := "sent"
		if err != nil {
			delivery = "unconfirmed"
		}
		c.logger.InfoContext(ctx, "Codex native technical service response", "service", "current-time", "delivery", delivery)
	}
	if err != nil {
		return Event{}, err
	}
	// Transmission grants no human interaction, native acceptance or completion.
	return c.metadata(CurrentTimeReplySent), nil
}
