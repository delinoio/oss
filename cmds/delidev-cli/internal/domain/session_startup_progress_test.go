// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionStartupProgressMonotonicBoundedSummary(t *testing.T) {
	a := StartupProgressAttempt{Steps: []StartupProgressStep{{WorkspaceOperation: StartupWorkspaceSetup}}}
	s := StartupProgressStep{WorkspaceOperation: StartupWorkspaceSetup, Sequence: 1, State: StartupProgressRunning}
	if a.Apply(s) != nil || a.Apply(s) != nil {
		t.Fatal("exact observation replay rejected")
	}
	changed := s
	changed.State = StartupProgressCompleted
	if a.Apply(changed) == nil {
		t.Fatal("changed sequence accepted")
	}
	changed.Sequence = 2
	if a.Apply(changed) != nil {
		t.Fatal("completion rejected")
	}
	s.Sequence = 3
	if a.Apply(s) == nil {
		t.Fatal("completed operation reopened")
	}
	if len(a.Steps) != 1 || a.LastSequence != 2 {
		t.Fatal("summary became a log")
	}
	for _, bad := range []StartupProgressStep{{WorkspaceOperation: 99, Sequence: 1, State: StartupProgressRunning}, {NativePhase: StartupCleanupPhase, Sequence: 1, State: StartupProgressRunning}, {WorkspaceOperation: StartupWorkspaceClone, RepositoryID: NewID(), RepositoryOrdinal: 1, RepositoryCount: 101, Sequence: 1, State: StartupProgressRunning}, {WorkspaceOperation: StartupWorkspaceSetup, NativePhase: StartupResolve, Sequence: 1, State: StartupProgressRunning}, {WorkspaceOperation: StartupWorkspaceClone, RepositoryID: "https://private.invalid", RepositoryOrdinal: 1, RepositoryCount: 1, Sequence: 1, State: StartupProgressRunning}} {
		if bad.Validate() == nil {
			t.Fatal("invalid observation accepted", bad)
		}
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), "http") || strings.Contains(string(raw), "prompt") {
		t.Fatal("unsafe summary")
	}
}
