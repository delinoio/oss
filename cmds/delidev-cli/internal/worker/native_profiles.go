package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type profileRegistryKey struct {
	root    string
	machine domain.ID
}

var (
	profileRegistriesMu sync.Mutex
	profileRegistries   = map[profileRegistryKey]*nativeprofile.Registry{}
)

// nativeProfiles resolves the Worker-local persistent native profile registry.
// The registry is shared by every native launch path of this Worker root so
// discovery, execution, Fork, compaction, Revert, Sidechat, title and review
// resolve the same semantic profiles. Server and Worker files stay local to
// their own owner; the Worker machine identity is part of profile ownership.
func nativeProfiles(config Config, machine domain.ID) (*nativeprofile.Registry, error) {
	if err := machine.Validate(); err != nil {
		return nil, err
	}
	key := profileRegistryKey{root: config.Root, machine: machine}
	profileRegistriesMu.Lock()
	defer profileRegistriesMu.Unlock()
	if registry := profileRegistries[key]; registry != nil {
		registry.UseLogger(config.Logger)
		return registry, nil
	}
	// A Worker machine identity is minted by exactly one server pairing, so the
	// machine alone binds the Worker root to its server; no server identifier is
	// read from credentials, keeping every launch path on one stable key.
	registry, err := nativeprofile.Open(nativeprofile.Config{Root: config.Root, Owner: nativeprofile.OwnerWorker, Machine: machine, Logger: config.Logger})
	if err != nil {
		return nil, err
	}
	profileRegistries[key] = registry
	return registry, nil
}

// discoveryConfig builds the accountless discovery configuration of a job.
func discoveryConfig(config Config, root string, owner, machine domain.ID) (harness.DiscoveryConfig, error) {
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return harness.DiscoveryConfig{}, err
	}
	return harness.DiscoveryConfig{Root: root, OwnerID: owner, Logger: config.Logger, Profiles: registry}, nil
}

// legacyRuntimePath is the historical per-execution runtime of one operation
// identifier, relative to the Worker root. New work never creates it as a
// native home; it remains only as the per-operation journal directory and as
// the in-place location of history created before persistent profiles.
func legacyRuntimePath(id domain.ID) string {
	return "runtimes/" + string(id)
}

// conversationProfile acquires the persistent profile that keeps one logical
// native history. Every identifier in histories (execution, action or fork
// runtime identifiers) is bound to that profile so later continuation, Fork,
// compaction and Revert find the retained history without deriving a path from
// the identifier.
func conversationProfile(ctx context.Context, config Config, machine domain.ID, key nativeprofile.Key, histories ...domain.ID) (*nativeprofile.Lease, error) {
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return nil, err
	}
	lease, err := registry.Acquire(ctx, key)
	if err != nil {
		return nil, err
	}
	for _, history := range histories {
		if history.Validate() != nil {
			continue
		}
		if err := registry.RecordHistory(ctx, lease.Key, history); err != nil {
			_ = lease.Close()
			return nil, err
		}
	}
	return lease, nil
}

// historyProfile resolves the retained profile of a native history identifier.
// A registered identifier resolves through the local profile registry. An
// identifier from before persistent profiles resolves to its verified legacy
// runtime directory, which is registered in place: nothing is moved, merged or
// rewritten. A missing history is never recreated.
func historyProfile(ctx context.Context, config Config, machine domain.ID, harnessKind domain.Harness, account domain.ID, history domain.ID) (nativeprofile.Info, error) {
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return nativeprofile.Info{}, err
	}
	info, ok, err := registry.History(ctx, history)
	if err != nil {
		return nativeprofile.Info{}, err
	}
	if ok {
		if info.Key.Harness != harnessKind {
			return nativeprofile.Info{}, executionCheckpointUncertain()
		}
		return info, nil
	}
	if history.Validate() != nil || account.Validate() != nil {
		return nativeprofile.Info{}, executionCheckpointUncertain()
	}
	key := nativeprofile.Key{Harness: harnessKind, Purpose: nativeprofile.PurposeConversation, Account: account, Conversation: history}
	info, err = registry.RegisterLegacy(ctx, key, legacyRuntimePath(history), history)
	if err != nil {
		return nativeprofile.Info{}, executionCheckpointUncertain()
	}
	return info, nil
}

// continuationProfile locks the profile that retains a continued history and
// binds the new operation identifier to it.
func continuationProfile(ctx context.Context, config Config, machine domain.ID, harnessKind domain.Harness, account, history domain.ID, bind ...domain.ID) (*nativeprofile.Lease, error) {
	info, err := historyProfile(ctx, config, machine, harnessKind, account, history)
	if err != nil {
		return nil, err
	}
	return conversationProfile(ctx, config, machine, info.Key, bind...)
}

// codexExecutionProfile resolves the persistent profile of one Codex execution.
// A continuation or an execution after Fork reuses the retained history's
// profile; a first execution (or an unsent retry of it) uses the session's
// conversation profile. The execution identifier is bound to the profile as
// history so later operations resolve it without a derived path.
func codexExecutionProfile(ctx context.Context, config Config, input domain.ExecutionJobInput) (*nativeprofile.Lease, error) {
	return executionProfile(ctx, config, domain.Codex, input)
}

func executionProfile(ctx context.Context, config Config, harnessKind domain.Harness, input domain.ExecutionJobInput) (*nativeprofile.Lease, error) {
	switch {
	case input.Continuation != nil:
		account := input.AccountID
		if input.Continuation.PreviousAccountID != "" {
			account = input.Continuation.PreviousAccountID
		}
		return continuationProfile(ctx, config, input.MachineID, harnessKind, account, input.Continuation.HistoryExecutionID, input.ExecutionID)
	case input.Fork != nil:
		return continuationProfile(ctx, config, input.MachineID, harnessKind, input.AccountID, input.Fork.RuntimeID, input.ExecutionID)
	}
	purpose := nativeprofile.PurposeConversation
	if input.Configuration.SidechatPolicy != "" {
		purpose = nativeprofile.PurposeSidechat
	}
	return conversationProfile(ctx, config, input.MachineID, nativeprofile.Key{Harness: harnessKind, Purpose: purpose, Account: input.AccountID, Conversation: input.SessionID}, input.ExecutionID)
}

// historyAccount returns the account that created the retained history an
// execution input continues. It is used only to name a legacy history's
// registry key; the registry binding resolves the profile afterwards.
func historyAccount(input domain.ExecutionJobInput) domain.ID {
	if input.Continuation != nil && input.Continuation.PreviousAccountID != "" {
		return input.Continuation.PreviousAccountID
	}
	return input.AccountID
}

// createOperationJournal creates the fresh per-operation journal directory.
// A leftover directory means an earlier attempt may have started, so it is
// never adopted; this exclusivity belongs to the operation, not to the native
// profile.
func createOperationJournal(root string, id domain.ID) (string, error) {
	runtimeRoot := filepath.Join(root, "runtimes")
	if err := security.PrivateDir(runtimeRoot); err != nil {
		return "", domain.SafeError(err)
	}
	journal := filepath.Join(runtimeRoot, string(id))
	if _, err := os.Lstat(journal); !errors.Is(err, os.ErrNotExist) {
		return "", domain.CompactionUncertain()
	}
	if err := security.CreatePrivateDirExclusive(journal); err != nil {
		return "", domain.CompactionUncertain()
	}
	return journal, nil
}

// historyNativeRoot resolves the native profile root that retains a history
// identifier for readers that only hold the Worker root (checkpoint readers
// and inspections). It locks nothing and creates nothing.
func historyNativeRoot(ctx context.Context, root string, machine domain.ID, harnessKind domain.Harness, account, history domain.ID) (string, error) {
	info, err := historyProfile(ctx, Config{Root: root}, machine, harnessKind, account, history)
	if err != nil {
		return "", err
	}
	return info.Root, nil
}

// createTitleJournal creates the fresh per-job automatic title journal. Native
// title state lives in the persistent title profile of the conversation.
func createTitleJournal(root string, job domain.ID) (string, error) {
	base := filepath.Join(root, "title-runtimes")
	if err := security.PrivateDir(base); err != nil {
		return "", err
	}
	journal := filepath.Join(base, string(job))
	if _, err := os.Lstat(journal); !errors.Is(err, os.ErrNotExist) {
		return "", publicationUncertain()
	}
	if err := security.CreatePrivateDirExclusive(journal); err != nil {
		return "", publicationUncertain()
	}
	return journal, nil
}

// titleProfile resolves the conversation's persistent automatic-title profile.
func titleProfile(ctx context.Context, config Config, input domain.AuxiliaryTitleInput) (*nativeprofile.Lease, error) {
	return conversationProfile(ctx, config, input.MachineID, nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeTitle, Account: input.AccountID, Conversation: input.SessionID})
}

// peekHistoryNativeRoot locates the retained native root of a history without
// registering or creating anything: a registered identifier resolves through
// the registry and an older one to its legacy runtime directory.
func peekHistoryNativeRoot(root string, machine domain.ID, harnessKind domain.Harness, history domain.ID) (string, error) {
	registry, err := nativeProfiles(Config{Root: root}, machine)
	if err != nil {
		return "", err
	}
	info, ok, err := registry.History(context.Background(), history)
	if err != nil {
		return "", err
	}
	if ok {
		if info.Key.Harness != harnessKind {
			return "", executionCheckpointUncertain()
		}
		return info.Root, nil
	}
	if history.Validate() != nil {
		return "", executionCheckpointUncertain()
	}
	return filepath.Join(root, filepath.FromSlash(legacyRuntimePath(history))), nil
}
