// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"testing"
	"time"
)

func TestStartupTimingOriginalSettlement(t *testing.T) {
	start := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	workspace, agent, execution := NewID(), NewID(), NewID()
	session := Session{StartupTiming: &SessionStartupTiming{Workspace: &StartupGroupTiming{JobID: workspace, StartedAt: start}, Agent: &StartupGroupTiming{JobID: agent, ExecutionID: execution, StartedAt: start.Add(12 * time.Second)}}}
	session.FinishStartupTiming(NewID(), "", start.Add(time.Second))
	if session.StartupTiming.Workspace.FinishedAt != nil {
		t.Fatal("foreign settlement changed time")
	}
	session.FinishStartupTiming(workspace, "", start.Add(12*time.Second))
	session.FinishStartupTiming(agent, execution, start.Add(20*time.Second))
	session.FinishStartupTiming(agent, execution, start.Add(30*time.Second))
	if session.StartupTiming.Workspace.FinishedAt.Sub(start) != 12*time.Second || session.StartupTiming.Agent.FinishedAt.Sub(session.StartupTiming.Agent.StartedAt) != 8*time.Second {
		t.Fatal("original durations changed")
	}
	var absent *StartupGroupTiming
	absent.Finish(agent, execution, start)
	reversed := &StartupGroupTiming{JobID: agent, StartedAt: start}
	reversed.Finish(agent, "", start.Add(-time.Second))
	if reversed.FinishedAt != nil {
		t.Fatal("negative duration retained")
	}
}
