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

// sharedCodexOwnerKey is the registry key of the account-wide Codex history
// home shared by the members of one managed Codex account owner.
func sharedCodexOwnerKey(account domain.ID) nativeprofile.Key {
	return nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeSharedOwner, Account: account}
}

// sharedCodexLegacyPath is the registry-relative path of the shared Codex
// history home. The directory predates the registry and is registered in
// place; nothing is moved or merged.
func sharedCodexLegacyPath(account domain.ID) string {
	return "runtimes/accounts/" + string(account) + "/codex"
}

// resolveSharedCodexHome registers the fixed per-account Codex history home in
// place and returns its profile without locking it. Registration is idempotent
// and survives owner restarts. The registry is validated before any directory
// is created: a key or path bound to another history blocks with a closed cause
// and creates nothing. A registered home must still be a real private
// directory; it is never repaired, recreated or replaced. Only an unregistered
// account creates its fixed private directory, and the registry creates it
// under its own lock only after it accepted the key. Creation and registration
// are all-or-nothing: a failed publication removes the empty directory this
// call created unless the entry is proven durable, so a failed attempt leaves
// no home for a retry to adopt.
func resolveSharedCodexHome(ctx context.Context, config Config, machine, account domain.ID) (nativeprofile.Info, error) {
	home, err := codexAccountHistoryHome(config.Root, account)
	if err != nil {
		return nativeprofile.Info{}, err
	}
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return nativeprofile.Info{}, err
	}
	key, relative := sharedCodexOwnerKey(account), sharedCodexLegacyPath(account)
	registered, bound, err := registry.CheckLegacy(ctx, key, relative)
	if err != nil {
		return nativeprofile.Info{}, err
	}
	if bound && (registered.Root != home || security.CheckPrivateDir(home) != nil) {
		return nativeprofile.Info{}, executionCheckpointUncertain()
	}
	info, err := registry.RegisterLegacyCreating(ctx, key, relative)
	if err != nil {
		return nativeprofile.Info{}, err
	}
	if info.Root != home {
		nativeprofile.Log(config.Logger, "native_profile_blocked", info.Key, info.ID, nativeprofile.ReasonOwnerInvalid)
		return nativeprofile.Info{}, nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonOwnerInvalid, "The shared Codex history home does not match its registered profile.", "Inspect the profile without deleting retained history.")
	}
	return info, nil
}

// acquireSharedCodexHome resolves the shared Codex history home and locks it.
// The founding owner holds the lock for the owner's lifetime; joined members
// rely on that lock. A held lock blocks with the closed cause profile_busy.
func acquireSharedCodexHome(ctx context.Context, config Config, machine, account domain.ID) (*nativeprofile.Lease, error) {
	info, err := resolveSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		return nil, err
	}
	registry, err := nativeProfiles(config, machine)
	if err != nil {
		return nil, err
	}
	lease, err := registry.Acquire(ctx, info.Key)
	if err != nil {
		return nil, err
	}
	if lease.Root != info.Root || lease.Origin != nativeprofile.OriginLegacy {
		_ = lease.Close()
		return nil, nativeprofile.Failure(domain.RecoveryRequired, nativeprofile.ReasonOwnerInvalid, "The shared Codex history home does not match its registered profile.", "Inspect the profile without deleting retained history.")
	}
	return lease, nil
}

// sharedHomeHold is a held lock of the registered shared Codex account home. A
// founding shared owner and an exclusive continuation of the account history
// each hold one, and each must release it before it publishes the Finish that
// lets the server admit the next holder.
type sharedHomeHold struct {
	config  Config
	machine domain.ID
	account domain.ID
	lease   *nativeprofile.Lease
	// unverified is set once the lock was closed and cleared only after a probe
	// proved that the lock is free again.
	unverified bool
}

// holdSharedCodexHome locks the shared Codex account home.
func holdSharedCodexHome(ctx context.Context, config Config, machine, account domain.ID) (*sharedHomeHold, error) {
	lease, err := acquireSharedCodexHome(ctx, config, machine, account)
	if err != nil {
		return nil, err
	}
	return &sharedHomeHold{config: config, machine: machine, account: account, lease: lease}, nil
}

// root is the registered home path the hold locks.
func (h *sharedHomeHold) root() string { return h.lease.Root }

// release closes the lock and verifies that a new holder can take it. It only
// unlocks: history, caches and helper files stay retained. It is idempotent and
// safe on a nil hold; a failed verification is reported again by the next call.
func (h *sharedHomeHold) release() error {
	if h == nil {
		return nil
	}
	var closeErr error
	if h.lease != nil {
		closeErr, h.lease, h.unverified = h.lease.Close(), nil, true
	}
	if !h.unverified {
		return nil
	}
	probe, err := acquireSharedCodexHome(context.Background(), h.config, h.machine, h.account)
	if err != nil {
		h.log("shared_codex_home_release_unverified", err)
		return errors.Join(closeErr, err)
	}
	probeErr := probe.Close()
	h.unverified = false
	if closeErr != nil || probeErr != nil {
		h.log("shared_codex_home_release_failed", errors.Join(closeErr, probeErr))
		return errors.Join(closeErr, probeErr)
	}
	return nil
}

func (h *sharedHomeHold) log(event string, err error) {
	if h.config.Logger != nil {
		reason, _ := nativeprofile.Reasoned(err)
		h.config.Logger.Warn(event, "account_id", h.account, "reason", reason)
	}
}

// publishAfterRelease runs publish, the Finish that frees the account lease on
// the server, only after the shared home lock was released and verified. The
// server commits the lease removal before the Worker could release afterwards,
// and a continuation already retrying its Take would then be admitted while the
// lock is still held and fail with profile_busy. A release failure is returned
// next to the publish outcome and never skips the publish: the lease must not
// be held for a lock that is already closed.
func publishAfterRelease(hold *sharedHomeHold, publish func() error) (releaseErr, publishErr error) {
	releaseErr = hold.release()
	return releaseErr, publish()
}

// verifySharedCodexHome checks a checkpoint-bound shared home against the
// registry without registering or creating anything. An unregistered legacy
// home passes; a registered one must resolve to the same path.
func verifySharedCodexHome(root string, machine, account domain.ID, home string) error {
	registry, err := nativeProfiles(Config{Root: root}, machine)
	if err != nil {
		return err
	}
	info, ok, err := registry.Lookup(context.Background(), sharedCodexOwnerKey(account))
	if err != nil {
		return err
	}
	if ok && info.Root != home {
		return executionCheckpointUncertain()
	}
	return nil
}

// reviewProfile resolves the persistent native review profile of a session.
func reviewProfile(ctx context.Context, config Config, machine, account, session, action domain.ID) (*nativeprofile.Lease, error) {
	return conversationProfile(ctx, config, machine, nativeprofile.Key{Harness: domain.Codex, Purpose: nativeprofile.PurposeReview, Account: account, Conversation: session}, action)
}

// forkChildProfile resolves the persistent profile of a Fork child history.
func forkChildProfile(ctx context.Context, config Config, machine domain.ID, harnessKind domain.Harness, account, runtime domain.ID) (*nativeprofile.Lease, error) {
	return conversationProfile(ctx, config, machine, nativeprofile.Key{Harness: harnessKind, Purpose: nativeprofile.PurposeFork, Account: account, Conversation: runtime}, runtime)
}
