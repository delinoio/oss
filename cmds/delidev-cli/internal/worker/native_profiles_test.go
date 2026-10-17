package worker

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func profileTestConfig(t *testing.T) (Config, domain.ID) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Mode bits do not install a private DACL on Windows. Create the fixture
	// owner root through the same primitive as the Worker.
	root = filepath.Join(root, "private")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	return Config{Root: root}, domain.NewID()
}

func profileExecution(machine, account, session domain.ID) domain.ExecutionJobInput {
	return domain.ExecutionJobInput{MachineID: machine, AccountID: account, SessionID: session, ExecutionID: domain.NewID()}
}

func restartProfileOwner(config Config, machine domain.ID) {
	profileRegistriesMu.Lock()
	delete(profileRegistries, profileRegistryKey{root: config.Root, machine: machine})
	profileRegistriesMu.Unlock()
}

// Different execution identifiers of one conversation, and an owner restart,
// resolve the same permanent profile and keep its native history.
func TestExecutionProfileIsStableAcrossExecutionsAndOwnerRestart(t *testing.T) {
	config, machine := profileTestConfig(t)
	account, session := domain.NewID(), domain.NewID()
	ctx := context.Background()
	input := profileExecution(machine, account, session)
	first, err := executionProfile(ctx, config, domain.Codex, input)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(first.Root, "codex", "history")
	if err := security.PrivateDir(filepath.Dir(marker)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restartProfileOwner(config, machine)
	next := profileExecution(machine, account, session)
	next.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: input.ExecutionID}
	second, err := executionProfile(ctx, config, domain.Codex, next)
	if err != nil {
		t.Fatal(err)
	}
	if second.Root != first.Root || second.ID != first.ID || second.Created {
		t.Fatal("a later execution allocated a new native home")
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "retained" {
		t.Fatal("native history was not retained")
	}
	_ = second.Close()
	// An unsent retry of the first execution has a new identifier but the same conversation.
	retry, err := executionProfile(ctx, config, domain.Codex, profileExecution(machine, account, session))
	if err != nil || retry.Root != first.Root {
		t.Fatalf("retry did not reuse the conversation profile: %v", err)
	}
	_ = retry.Close()
}

func TestExecutionProfilesAreIsolatedAndConcurrentConversationsDoNotCollide(t *testing.T) {
	config, machine := profileTestConfig(t)
	account := domain.NewID()
	ctx := context.Background()
	first, err := executionProfile(ctx, config, domain.Codex, profileExecution(machine, account, domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	// A second conversation of the same account runs concurrently in its own profile.
	second, err := executionProfile(ctx, config, domain.Codex, profileExecution(machine, account, domain.NewID()))
	if err != nil {
		t.Fatalf("concurrent conversation was blocked: %v", err)
	}
	defer second.Close()
	other, err := executionProfile(ctx, config, domain.Codex, profileExecution(machine, domain.NewID(), domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	harnessProfile, err := executionProfile(ctx, config, domain.ClaudeCode, profileExecution(machine, account, domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	defer harnessProfile.Close()
	roots := map[string]bool{first.Root: true}
	for _, lease := range []*nativeprofile.Lease{second, other, harnessProfile} {
		if roots[lease.Root] {
			t.Fatal("profiles are shared across conversations, accounts or harnesses")
		}
		roots[lease.Root] = true
	}
	// A second Worker root never shares a profile.
	otherRoot, otherMachine := profileTestConfig(t)
	foreign, err := executionProfile(ctx, otherRoot, domain.Codex, profileExecution(otherMachine, account, domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	if roots[foreign.Root] {
		t.Fatal("profiles are shared across Worker roots")
	}
}

// A history created before persistent profiles keeps its original directory and
// content; it is registered in place instead of being moved or merged.
func TestLegacyHistoryIsRegisteredInPlace(t *testing.T) {
	config, machine := profileTestConfig(t)
	account, session := domain.NewID(), domain.NewID()
	history := domain.NewID()
	legacy := filepath.Join(config.Root, "runtimes", string(history))
	rollout := filepath.Join(legacy, "codex", "sessions", "rollout")
	if err := security.PrivateDir(filepath.Join(legacy, "codex")); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Dir(rollout)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte("legacy-history"), 0600); err != nil {
		t.Fatal(err)
	}
	input := profileExecution(machine, account, session)
	input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: history}
	lease, err := executionProfile(context.Background(), config, domain.Codex, input)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Root != legacy || lease.Origin != nativeprofile.OriginLegacy {
		t.Fatalf("legacy history was moved: %s", lease.Root)
	}
	_ = lease.Close()
	if raw, err := os.ReadFile(rollout); err != nil || string(raw) != "legacy-history" {
		t.Fatal("legacy history changed")
	}
	if _, err := os.Stat(filepath.Join(config.Root, "native-profiles", "p")); err == nil {
		entries, _ := os.ReadDir(filepath.Join(config.Root, "native-profiles", "p"))
		if len(entries) != 0 {
			t.Fatal("registering legacy history allocated a new profile")
		}
	}
	// A missing history is never recreated.
	missing := profileExecution(machine, account, session)
	missing.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: domain.NewID()}
	if _, err := executionProfile(context.Background(), config, domain.Codex, missing); err == nil {
		t.Fatal("a missing history was recreated")
	}
}

func TestOperationJournalIsFreshAndExclusive(t *testing.T) {
	config, _ := profileTestConfig(t)
	id := domain.NewID()
	journal, err := createOperationJournal(config.Root, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(journal, "native-revert-intent.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := createOperationJournal(config.Root, id); err == nil {
		t.Fatal("an interrupted attempt's journal was adopted")
	}
	title, err := createTitleJournal(config.Root, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := createTitleJournal(config.Root, id); err == nil || title == "" {
		t.Fatal("a title journal was adopted")
	}
}

func TestSessionDeletionRemovesOnlyIdleUnreferencedProfiles(t *testing.T) {
	config, machine := profileTestConfig(t)
	var logs bytes.Buffer
	config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	account, session := domain.NewID(), domain.NewID()
	ctx := context.Background()
	input := profileExecution(machine, account, session)
	lease, err := executionProfile(ctx, config, domain.Codex, input)
	if err != nil {
		t.Fatal(err)
	}
	other, err := executionProfile(ctx, config, domain.Codex, profileExecution(machine, account, domain.NewID()))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	work := domain.SessionDeletionWork{SessionID: session, MachineID: machine, Copies: []domain.SessionDeletionCopy{{ExecutionID: input.ExecutionID}}}
	if err := deleteSessionNativeProfiles(ctx, config, work); err == nil {
		t.Fatal("an active profile was deleted")
	}
	_ = lease.Close()
	registry, _ := nativeProfiles(config, machine)
	ref := nativeprofile.Reference{Kind: nativeprofile.ReferenceCheckpoint, ID: domain.NewID()}
	if err := registry.AddReference(ctx, lease.Key, ref); err != nil {
		t.Fatal(err)
	}
	if err := deleteSessionNativeProfiles(ctx, config, work); err == nil {
		t.Fatal("a referenced profile was deleted")
	}
	if remains, err := sessionNativeProfilesRemain(ctx, config, work); err != nil || !remains {
		t.Fatal("referenced profile disappeared")
	}
	if err := registry.RemoveReference(ctx, lease.Key, ref); err != nil {
		t.Fatal(err)
	}
	if err := deleteSessionNativeProfiles(ctx, config, work); err != nil {
		t.Fatal(err)
	}
	if remains, err := sessionNativeProfilesRemain(ctx, config, work); err != nil || remains {
		t.Fatalf("deleted profile remains: %v", err)
	}
	if _, err := os.Stat(lease.Root); !os.IsNotExist(err) {
		t.Fatal("profile directory remains after explicit deletion")
	}
	if _, err := os.Stat(other.Root); err != nil {
		t.Fatal("another session's profile was deleted")
	}
	if strings.Contains(logs.String(), config.Root) {
		t.Fatal("profile logs exposed a path")
	}
}
