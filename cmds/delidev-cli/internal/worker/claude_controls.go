package worker

import (
	"context"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
)

func startClaudeControls(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, config Config, display *ClaudeContentPublisher, api *claude.APISession) func() error {
	owned, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		for {
			var err error
			select {
			case <-owned.Done():
				done <- nil
				return
			case control, ok := <-config.questionControls:
				if !ok {
					err = publicationUncertain()
				} else {
					err = display.DeliverQuestionResponse(owned, nativeCtx, control, api)
				}
			case control, ok := <-config.approvalControls:
				if !ok {
					err = publicationUncertain()
				} else {
					err = display.DeliverApprovalResponse(owned, nativeCtx, control, api)
				}
			case <-config.steerControls:
				err = domain.Fail(domain.Unsupported, "Claude Steer requires a separate original-input adapter.", "Retain the queued input; do not use another harness's response encoder.")
			}
			if err != nil {
				if owned.Err() != nil {
					done <- nil
				} else {
					done <- err
					cancelNative()
				}
				return
			}
		}
	}()
	var once sync.Once
	var result error
	return func() error { once.Do(func() { stop(); result = <-done }); return result }
}
