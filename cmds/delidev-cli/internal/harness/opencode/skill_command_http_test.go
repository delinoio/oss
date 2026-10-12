// SPDX-License-Identifier: Apache-2.0
package opencode

import (
	"context"
	"testing"
)

func TestSkillCommandCleanupJoinsOriginalCanceledOperation(t *testing.T) {
	operation, cancel := context.WithCancel(context.Background())
	attempt := &skillCommandHTTP{cancel: cancel, done: make(chan skillCommandHTTPResult, 1)}
	exited := make(chan struct{})
	go func() {
		<-operation.Done()
		attempt.done <- skillCommandHTTPResult{err: operation.Err()}
		close(exited)
	}()
	session := &sessionAPI{commandHTTP: attempt}
	if err := session.joinSkillCommandHTTP(context.Background(), true); err != nil || !attempt.joined {
		t.Fatal("cleanup did not join the original native command", err)
	}
	<-exited
	if err := session.joinSkillCommandHTTP(context.Background(), true); err != nil {
		t.Fatal("joined cleanup lost its original result", err)
	}
}
