// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionStartupProgressTwoRepositoryCloneBoundary(t *testing.T) {
	m, input, _ := managedCloneFixture(t)
	second := input.Repositories[0]
	second.ID = domain.NewID()
	input.Repositories = append(input.Repositories, second)
	var mu sync.Mutex
	var observed []domain.StartupProgressStep
	blocked := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var once sync.Once
	defer once.Do(func() { close(release) })
	ctx = WithStartupObserver(ctx, func(s domain.StartupProgressStep) {
		mu.Lock()
		observed = append(observed, s)
		mu.Unlock()
		if s.WorkspaceOperation == domain.StartupWorkspaceClone && s.State == domain.StartupProgressRunning && s.RepositoryOrdinal == 2 {
			close(blocked)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	})
	done := make(chan error, 1)
	go func() { _, err := m.Prepare(ctx, input); done <- err }()
	select {
	case <-blocked:
	case err := <-done:
		t.Fatal("clone never reached boundary", err)
	case <-ctx.Done():
		t.Fatal("clone boundary timeout")
	}
	mu.Lock()
	snapshot := append([]domain.StartupProgressStep(nil), observed...)
	mu.Unlock()
	a := domain.StartupProgressAttempt{Steps: StartupProgressPlan(input)}
	for i, s := range snapshot {
		s.Sequence = uint64(i + 1)
		if a.Apply(s) != nil {
			t.Fatal("original observed stage rejected", s)
		}
	}
	var first, secondState domain.StartupProgressState
	for _, s := range a.Steps {
		if s.WorkspaceOperation == domain.StartupWorkspaceClone {
			if s.RepositoryOrdinal == 1 {
				first = s.State
			} else {
				secondState = s.State
				if s.RepositoryID != second.ID || s.RepositoryCount != 2 {
					t.Fatal("original repository attribution lost")
				}
			}
		}
	}
	if first != domain.StartupProgressCompleted || secondState != domain.StartupProgressRunning {
		t.Fatal("aggregate clone completion guessed")
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	snapshot = append([]domain.StartupProgressStep(nil), observed...)
	mu.Unlock()

	a = domain.StartupProgressAttempt{Steps: StartupProgressPlan(input)}
	for i, s := range snapshot {
		s.Sequence = uint64(i + 1)
		if a.Apply(s) != nil {
			t.Fatal("completion stage rejected", s)
		}
	}
	for _, s := range a.Steps {
		if s.State != domain.StartupProgressCompleted {
			t.Fatal("operation completion absent", s)
		}
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), "https") || strings.Contains(string(raw), m.Root) {
		t.Fatal("telemetry leaked private source")
	}
}
func TestSessionStartupProgressApplicableLocalChatAndBound(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Local, domain.GeneralChat} {
		m := manager(t)
		input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: kind}
		if kind == domain.Local {
			input, _ = requestFor(repository(t))
			input.Type = domain.Local
		}
		var observed []domain.StartupProgressStep
		_, err := m.Prepare(WithStartupObserver(context.Background(), func(s domain.StartupProgressStep) { observed = append(observed, s) }), input)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range observed {
			if s.WorkspaceOperation == domain.StartupWorkspaceClone || kind == domain.GeneralChat && s.RepositoryID != "" {
				t.Fatal("inapplicable operation", s)
			}
		}
	}
	input := PrepareRequest{Type: domain.Worktree}
	for range 100 {
		input.Repositories = append(input.Repositories, RepositorySpec{ID: domain.NewID(), SourceKind: RemoteCloneSource})
	}
	if len(StartupProgressPlan(input)) != 403 {
		t.Fatal("stage set exceeded original 100 repository bound")
	}
}
