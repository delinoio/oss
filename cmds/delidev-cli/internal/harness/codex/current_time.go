// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"strconv"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

const maxCurrentTimeRequests = 4096

type currentTimeDelivery string

const (
	currentTimeTransmitted currentTimeDelivery = "transmitted"
	currentTimeRefused     currentTimeDelivery = "refused"
	currentTimeUncertain   currentTimeDelivery = "uncertain"
)

type currentTimeResult struct {
	CurrentTimeAt int64 `json:"currentTimeAt"`
}

// answerCurrentTimeLocked consumes only the emitted original root request. The
// connection retains its arrival token; the adapter also fences reused numeric
// or textual IDs after successful delivery, when nativewire retires its token.
// Sampling and transmission never create an interaction or input receipt.
func (c *Client) answerCurrentTimeLocked(ctx context.Context, native nativewire.Event, reply func(context.Context, nativewire.Event, any) error) (returned error) {
	delivery := currentTimeRefused
	defer func() {
		if c.logger != nil {
			c.logger.InfoContext(ctx, "Codex technical service response", "service_kind", "current-time", "delivery", delivery)
		}
	}()
	var params struct {
		ThreadID domain.ID `json:"threadId"`
	}
	if native.Method != "currentTime/read" || native.Kind != nativewire.ServerRequest || len(native.Params) > 4096 || domain.Decode(native.Params, &params) != nil || params.ThreadID.Validate() != nil || native.Token.Validate() != nil || c.mode != ThreadProtocol || c.execution == nil || c.thread.Validate() != nil || params.ThreadID != c.thread {
		return incompatible()
	}
	id, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return err
	}
	key := "s:" + id.Text
	if id.Kind == NumberRequestID {
		key = "n:" + strconv.FormatInt(*id.Number, 10)
	}
	if _, exists := c.timeRequests[key]; exists {
		return domain.Fail(domain.Conflict, "The native time request was already handled.", "Reconcile the original connection without replying again.")
	}
	if len(c.timeRequests) >= maxCurrentTimeRequests {
		return domain.Fail(domain.ResourceExhausted, "Native time request tracking reached its bound.", "Reconcile the original connection before replacing it.")
	}
	if c.problem != nil {
		return c.problem
	}
	if err := c.wire.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return domain.SafeError(err)
	}
	if c.timeRequests == nil {
		c.timeRequests = map[string]domain.ID{}
	}
	// Retain before sampling/replying. Even a canceled or uncertain first reply
	// cannot authorize a second clock sample or a replacement response.
	c.timeRequests[key] = native.Token
	clock := c.timeServiceClock
	if clock == nil {
		clock = time.Now
	}
	result := currentTimeResult{CurrentTimeAt: clock().Unix()}
	err = reply(ctx, native, result)
	if err == nil {
		delivery = currentTimeTransmitted
	} else if domain.SafeError(err).Code == domain.RecoveryRequired {
		delivery = currentTimeUncertain
		c.problem = turnUncertain()
		c.execution.paused = true
		return c.problem
	}
	return err
}
