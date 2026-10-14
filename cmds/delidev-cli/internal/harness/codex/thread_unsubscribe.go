// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type threadUnsubscribeStatus string

const (
	threadUnsubscribed threadUnsubscribeStatus = "unsubscribed"
)

// UnsubscribeAndWait joins only this client's native thread subscription. A
// successful request response is insufficient: the exact thread/closed event
// is the native shutdown observation. Descendant and process containment are
// independent obligations of the caller before it releases a workspace.
func (c *Client) UnsubscribeAndWait(ctx context.Context, requestID domain.ID) (returned error) {
	defer c.recordFailure(ctx, domain.CodexCleanup, &returned)
	if requestID.Validate() != nil || c.mode != ThreadProtocol || c.thread.Validate() != nil || c.execution == nil || c.problem != nil || c.unsubscribing || c.threadClosed || c.execution.active != "" {
		return turnUncertain()
	}
	if err := c.acquireControl(ctx); err != nil {
		return err
	}
	// Set the expectation before the send. The notification may reach the
	// event queue before the response to thread/unsubscribe does.
	c.unsubscribing = true
	response, err := c.wire.Call(ctx, requestID, "thread/unsubscribe", struct {
		ThreadID domain.ID `json:"threadId"`
	}{c.thread})
	<-c.control
	if err != nil || response.ErrorCode != nil {
		c.problem = turnUncertain()
		return c.problem
	}
	var result struct {
		Status threadUnsubscribeStatus `json:"status"`
	}
	if domain.Decode(response.Result, &result) != nil || result.Status != threadUnsubscribed {
		c.problem = turnUncertain()
		return c.problem
	}
	for !c.threadClosed {
		event, err := c.NextEvent(ctx)
		if err != nil {
			return err
		}
		if event.Kind != ThreadClosedEvent && event.Kind != MetadataEvent {
			c.problem = turnUncertain()
			return c.problem
		}
	}
	return nil
}
