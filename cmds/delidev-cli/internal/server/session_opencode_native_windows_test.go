package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Opt in with DELIDEV_NATIVE_OPENCODE_EXECUTABLE pointing to an already installed
// pinned Windows binary. Every provider is scripted loopback; no user account,
// inference service, installation, native history replay or state is inherited.
func TestManualNativeWindowsOpenCodeGeneralChatLifecycle(t *testing.T) {
	binary := nativeOpenCodeRecoveryExecutable(t)
	t.Run("Build-Plan-FIFO", func(t *testing.T) {
		nativeOpenCodePublicDispatch(t, 3, false, "", true)
	})
	t.Run("Build-Plan-Stop-Resume", func(t *testing.T) {
		nativeOpenCodePublicDispatchProfile(t, 3, false, "", "text-stop", true)
	})
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []string{"first", "resumed", "switched", "failed", "missing-checkpoint"} {
			t.Run(string(mode)+"/lost-report/"+scenario, func(t *testing.T) {
				nativeOpenCodeRecovery(t, binary, mode, scenario)
			})
		}
	}
}
