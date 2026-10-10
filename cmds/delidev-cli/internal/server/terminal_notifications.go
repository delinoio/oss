// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"time"
)

type terminalWakeStage string

const (
	terminalStoreChanged   terminalWakeStage = "store_changed"
	terminalOutputAppended terminalWakeStage = "output_appended"
	terminalSafetyTick     terminalWakeStage = "safety_tick"
)

type terminalStreamKind string

const (
	terminalAssignmentStream terminalStreamKind = "assignments"
	terminalOutputStream     terminalStreamKind = "output"
)

// Notifications are hints only. The caller reauthorizes and rebuilds its current
// snapshot on every wake; a signal itself admits no control or byte delivery.
func waitTerminalChange(ctx context.Context, primary, changed, output <-chan struct{}, safety <-chan time.Time) (terminalWakeStage, bool) {
	select {
	case <-ctx.Done():
		return "", false
	case <-primary:
		return "", false
	case <-changed:
		return terminalStoreChanged, true
	case <-output:
		return terminalOutputAppended, true
	case <-safety:
		return terminalSafetyTick, true
	}
}

func (s *Service) logTerminalWake(stream terminalStreamKind, stage terminalWakeStage, duration time.Duration) {
	if s.logger != nil {
		s.logger.Debug("delidev.terminal.stream_wake", "stream", stream, "stage", stage, "duration_ms", duration.Milliseconds())
	}
}
