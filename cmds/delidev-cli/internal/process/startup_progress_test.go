// SPDX-License-Identifier: Apache-2.0
package process

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestSessionStartupProgressOriginalNativeResumeBoundary(t *testing.T) {
	c := config(t, "sleep")
	var observations []domain.StartupProgressStep
	c.StartupObserver = func(p domain.ExecutionStartupPhase, s domain.StartupProgressState) {
		observations = append(observations, domain.StartupProgressStep{NativePhase: p, State: s})
	}
	h, err := Start(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if len(observations) != 1 || observations[0].NativePhase != domain.StartupLaunch || observations[0].State != domain.StartupProgressRunning {
		t.Fatal("preparation guessed launch completion")
	}
	if err := h.Resume(); err != nil {
		t.Fatal(err)
	}
	if len(observations) != 3 || observations[1].State != domain.StartupProgressCompleted || observations[2].NativePhase != domain.StartupInitialize || observations[2].State != domain.StartupProgressRunning {
		t.Fatal("native barrier observations lost")
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	h.Wait()
}
