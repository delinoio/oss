// SPDX-License-Identifier: Apache-2.0
package nativewire

import (
	"context"
	"encoding/json"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// ReplyCurrentTime samples only after claiming the exact original arrival.
// This narrow service does not create a general native callback gateway.
func (c *Connection) ReplyCurrentTime(ctx context.Context, event Event, clock func() time.Time) error {
	key, err := idKey(event.ID)
	if err != nil || event.Kind != ServerRequest || event.Method != "currentTime/read" || event.Token.Validate() != nil || clock == nil {
		return domain.Fail(domain.InvalidArgument, "The original current-time request is required.", "Preserve its native arrival identity.")
	}
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	c.mu.Lock()
	original, ok := c.incoming[key]
	if !ok || original.token != event.Token || original.method != "currentTime/read" || original.replying {
		c.mu.Unlock()
		return domain.Fail(domain.Conflict, "The native service request was already answered or replaced.", "Reconcile the original native execution without resending.")
	}
	original.replying = true
	c.incoming[key] = original
	c.mu.Unlock()
	// Claim before sampling or writing. A failed/uncertain write retains this
	// claim, exactly as ordinary Reply does, and cannot resample or resend.
	seconds := clock().Unix()
	result, err := json.Marshal(struct {
		CurrentTimeAt int64 `json:"currentTimeAt"`
	}{seconds})
	if err != nil {
		return protocolFailure()
	}
	raw, err := json.Marshal(envelope{JSONRPC: c.jsonrpc, ID: event.ID, Result: result})
	if err != nil {
		return protocolFailure()
	}
	if err := c.write(ctx, raw); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.incoming, key)
	c.mu.Unlock()
	return nil
}
