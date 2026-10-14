package worker

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
)

// openCodeRuntimeRoot returns the native runtime root of a persistent OpenCode
// profile. A managed profile root is the runtime root itself. A legacy Fork
// child created before persistent profiles kept its native runtime in the
// "native" subdirectory of the Fork runtime directory, which stays in place.
func openCodeRuntimeRoot(info nativeprofile.Info) (string, error) {
	if info.Origin == nativeprofile.OriginLegacy {
		native := filepath.Join(info.Root, "native")
		if stat, err := os.Lstat(native); err == nil {
			if !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
				return "", executionCheckpointUncertain()
			}
			return native, nil
		}
	}
	return info.Root, nil
}

// openCodeHistoryRoot resolves the native runtime root that retains an OpenCode
// execution, Fork runtime or compaction action. It locks nothing and creates
// nothing; a missing history is never recreated.
func openCodeHistoryRoot(ctx context.Context, root string, machine domain.ID, account, history domain.ID) (string, error) {
	info, err := historyProfile(ctx, Config{Root: root}, machine, domain.OpenCode, account, history)
	if err != nil {
		return "", err
	}
	return openCodeRuntimeRoot(info)
}

// resetUnsentOpenCodeHistory clears the abandoned native session of an unsent
// first execution before its retry. It never runs for a continuation or a
// history that accepted input.
func resetUnsentOpenCodeHistory(home string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := opencode.ResetUnsentHistory(ctx, home); err != nil {
		return err
	}
	return nil
}

// openCodeExecutionProfile locks the persistent profile of one OpenCode
// execution. A continuation resumes the profile that retains the newest
// checkpoint (historyOwner) and binds the new execution and the original
// history identifier to it, so later operations resolve the same profile.
func openCodeExecutionProfile(ctx context.Context, config Config, input domain.ExecutionJobInput, historyOwner domain.ID) (*nativeprofile.Lease, error) {
	if input.Continuation == nil {
		return executionProfile(ctx, config, domain.OpenCode, input)
	}
	return continuationProfile(ctx, config, input.MachineID, domain.OpenCode, historyAccount(input), historyOwner, input.ExecutionID, input.Continuation.HistoryExecutionID)
}
