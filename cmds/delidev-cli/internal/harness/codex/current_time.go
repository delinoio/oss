// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

type currentTimeDelivery string

const (
	currentTimeSent      currentTimeDelivery = "sent"
	currentTimeRejected  currentTimeDelivery = "rejected"
	currentTimeUncertain currentTimeDelivery = "uncertain"
)

func (c *Client) currentTimeRequest(native nativewire.Event) (bool, error) {
	if native.Kind != nativewire.ServerRequest || native.Method != "currentTime/read" || c.execution == nil {
		return false, nil
	}
	var params struct {
		ThreadID domain.ID `json:"threadId"`
	}
	if domain.DecodeWithLimit(native.Params, &params, 4096) != nil || params.ThreadID.Validate() != nil || native.Token.Validate() != nil {
		return false, incompatible()
	}
	if _, err := decodeNativeRequestID(native.ID); err != nil {
		return false, err
	}
	return params.ThreadID == c.thread && c.thread != "", nil
}

func (c *Client) replyCurrentTimeLocked(ctx context.Context, native nativewire.Event) (Event, error) {
	owned, err := c.currentTimeRequest(native)
	if err != nil {
		return Event{}, err
	}
	if !owned {
		return privateNative(native), nil
	}
	clock := c.currentTimeClock
	if clock == nil {
		clock = time.Now
	}
	err = c.wire.ReplyCurrentTime(ctx, native, clock)
	if c.logger != nil {
		delivery := currentTimeSent
		if err != nil {
			delivery = currentTimeUncertain
			if code := domain.SafeError(err).Code; code == domain.Conflict || code == domain.InvalidArgument {
				delivery = currentTimeRejected
			}
		}
		c.logger.InfoContext(ctx, "Codex native technical service response", "service_kind", "current-time", "delivery", delivery)
	}
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: CurrentTimeRepliedEvent, ThreadID: c.thread, Correlated: true}, nil
}
