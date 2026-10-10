// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

// Private consumed observation; it cannot become a product event or interaction.
const currentTimeRepliedEvent EventKind = "private-current-time-replied"

func (c *Client) replyCurrentTimeLocked(ctx context.Context, native nativewire.Event) (Event, error) {
	var params struct {
		ThreadID domain.ID `json:"threadId"`
	}
	if native.Kind != nativewire.ServerRequest || native.Method != "currentTime/read" || len(native.Params) > 1024 || domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || native.Token.Validate() != nil {
		return Event{}, incompatible()
	}
	if c.execution == nil || c.execution.thread.ID != c.thread || c.thread.Validate() != nil || params.ThreadID != c.thread {
		return privateNative(native), nil
	}
	if c.problem != nil {
		return Event{}, c.problem
	}
	if _, err := decodeNativeRequestID(native.ID); err != nil {
		return Event{}, err
	}
	clock := c.workerClock
	if clock == nil {
		clock = time.Now
	}
	err := c.wire.ReplyGenerated(ctx, native, func() any {
		return struct {
			CurrentTimeAt int64 `json:"currentTimeAt"`
		}{clock().Unix()}
	})
	if c.logger != nil {
		delivery := "transmitted"
		if err != nil {
			delivery = "rejected"
			if domain.SafeError(err).Code == domain.RecoveryRequired {
				delivery = "uncertain"
			}
		}
		c.logger.InfoContext(ctx, "Codex technical service reply", "service", "current-time", "delivery", delivery)
	}
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: currentTimeRepliedEvent}, nil
}
