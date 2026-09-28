package worker

import (
	"context"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// One joined controller preserves response publication ordering with the
// original event composer. Targeted cancellation stops claims/sends without
// canceling the native Stop grace. A failure without cancellation revokes the
// original native lifetime, never starts another response or input.
func startOpenCodeControls(ctx, nativeCtx context.Context, cancelNative context.CancelFunc, config Config, mapper *OpenCodeEventPublisher) func() error {
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
					err = mapper.DeliverQuestionResponse(owned, nativeCtx, control)
				}
			case control, ok := <-config.approvalControls:
				if !ok {
					err = publicationUncertain()
				} else {
					err = mapper.DeliverApprovalResponse(owned, nativeCtx, control)
				}
			case <-config.steerControls:
				err = domain.Fail(domain.Unsupported, "OpenCode Steer requires a separate original-input adapter.", "Retain the queued input; it cannot use another harness's response encoder.")
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
	return func() error {
		once.Do(func() { stop(); result = <-done })
		return result
	}
}
