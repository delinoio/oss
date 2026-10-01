package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/grok"
)

type grokStopControl struct {
	finished chan struct{}
	done     chan struct{}
}

// Start only after original input acceptance is acknowledged and the startup
// cancellation hook has been removed. Stream loss still cancels nativeCtx;
// targeted Stop instead allows bounded original native termination to drain.
func startGrokStopControl(targeted, nativeCtx context.Context, cancelNative context.CancelFunc, api *grok.OwnedAPI, logger *slog.Logger) *grokStopControl {
	c := &grokStopControl{finished: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		select {
		case <-c.finished:
			return
		case <-nativeCtx.Done():
			return
		case <-targeted.Done():
		}
		select {
		case <-c.finished:
			return
		default:
		}
		if nativeCtx.Err() != nil {
			return
		}
		grace, cancel := context.WithTimeout(nativeCtx, 15*time.Second)
		forceCleanup := context.AfterFunc(grace, cancelNative)
		defer func() { forceCleanup(); cancel() }()
		request := domain.NewID()
		submitted, err := api.StopText(grace, request)
		code := domain.Code("")
		if err != nil {
			code = domain.SafeError(err).Code
		}
		logger.InfoContext(nativeCtx, "grok_execution_stop_submitted", "request_id", request, "claimed", submitted.Claimed, "delivered", submitted.Delivered, "code", code)
		if err != nil && !submitted.Claimed {
			cancelNative()
			return
		}
		select {
		case <-c.finished:
		case <-grace.Done():
			cancelNative()
			logger.WarnContext(nativeCtx, "grok_execution_stop_requires_recovery", "request_id", request, "code", domain.RecoveryRequired)
		}
	}()
	return c
}

func (c *grokStopControl) join() {
	if c == nil {
		return
	}
	close(c.finished)
	<-c.done
}
