package claude

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOriginalInputCompletionRequiresIndependentCleanupAndExactController(t *testing.T) {
	for _, scenario := range []string{"success", "changed-permission", "foreign-owner", "foreign-session", "foreign-input", "foreign-turn", "not-idle", "automatic", "pending-task", "barrier"} {
		t.Run(scenario, func(t *testing.T) {
			s, transport := sessionFixture(t)
			owner, session, input, turn := s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID
			switch scenario {
			case "changed-permission":
				s.permissionChanged = true
			case "foreign-owner":
				owner = domain.NewID()
			case "foreign-session":
				session = domain.NewID()
			case "foreign-input":
				input = domain.NewID()
			case "foreign-turn":
				turn = string(domain.NewID())
			case "not-idle":
				s.current.runState = RunRunning
			case "automatic":
				s.current.continuationSeen = true
			case "pending-task":
				s.current.tasks = map[string]nativeTaskState{"pending": {status: TaskRunning}}
			case "barrier":
				transport.barrier = sessionBusy()
			}
			r, err := s.FinishOriginalInput(context.Background(), owner, session, input, turn)
			if scenario == "success" || scenario == "changed-permission" {
				if err != nil || !r.Successful() || !transport.closed.Load() || !s.cleanupJoined.Load() || r.Usage != nil {
					t.Fatal("original completion lost independent cleanup", err)
				}
				if _, err := s.FinishOriginalInput(context.Background(), owner, session, input, turn); err == nil {
					t.Fatal("closed controller reused")
				}
			} else if err == nil || transport.closed.Load() {
				t.Fatal("foreign/unsettled controller acquired cleanup", err)
			}
		})
	}
}

func TestOriginalInputCompletionCannotConvertForcedCleanupAfterEOFFailureIntoProof(t *testing.T) {
	s, transport := sessionFixture(t)
	transport.finishError = streamUncertain()
	r, err := s.FinishOriginalInput(context.Background(), s.config.Process.OwnerID, s.config.SessionID, s.current.input, s.current.turnID)
	if err == nil || r.Kind != "" || !transport.closed.Load() || s.cleanupJoined.Load() || s.problem == nil {
		t.Fatal("uncertain EOF acquired completion or escaped forced cleanup", err)
	}
}
