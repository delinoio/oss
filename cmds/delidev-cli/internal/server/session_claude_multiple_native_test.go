package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeClaudeMultipleRepositoryContinuation(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, profile := range []claudePublicCase{claudePublicMultipleLocal, claudePublicMultipleWorktree} {
			t.Run(string(mode)+"/"+string(profile), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, profile) })
		}
	}
}

func TestManualNativeClaudeMultipleRepositoryRecovery(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, profile := range []claudePublicCase{claudePublicMultipleLocal, claudePublicMultipleWorktree} {
			t.Run(string(mode)+"/"+string(profile), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, profile, claudePublicRecoveryCase{turn: 2}) })
		}
	}
}
