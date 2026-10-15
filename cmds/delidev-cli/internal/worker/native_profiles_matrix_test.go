package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/nativeprofile"
)

// profileOperation acquires the profile that one native launch path uses.
// attempt carries a fresh request, execution or action identifier per call.
type profileOperation struct {
	name    string
	harness domain.Harness
	acquire func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error)
}

type profileFixture struct {
	config  Config
	machine domain.ID
	account domain.ID
	session domain.ID
	// history is the execution identifier that created the conversation history.
	history domain.ID
	runtime domain.ID
}

func (f *profileFixture) execution(harnessKind domain.Harness) domain.ExecutionJobInput {
	return domain.ExecutionJobInput{MachineID: f.machine, AccountID: f.account, SessionID: f.session, ExecutionID: domain.NewID()}
}

func profileOperations() []profileOperation {
	var operations []profileOperation
	for _, h := range []domain.Harness{domain.Codex, domain.ClaudeCode, domain.OpenCode, domain.GrokBuild} {
		h := h
		operations = append(operations,
			profileOperation{"execution", h, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
				return executionProfile(ctx, f.config, h, f.execution(h))
			}},
			profileOperation{"continuation", h, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
				input := f.execution(h)
				input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.history}
				return executionProfile(ctx, f.config, h, input)
			}},
		)
		if h == domain.GrokBuild {
			continue
		}
		operations = append(operations, profileOperation{"compaction", h, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
			return continuationProfile(ctx, f.config, f.machine, h, f.account, f.history, domain.NewID())
		}})
		if h == domain.Codex {
			operations = append(operations, profileOperation{"revert", h, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
				return continuationProfile(ctx, f.config, f.machine, h, f.account, f.history)
			}})
		}
		if h == domain.Codex || h == domain.OpenCode {
			operations = append(operations, profileOperation{"fork-child", h, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
				return forkChildProfile(ctx, f.config, f.machine, h, f.account, f.runtime)
			}})
		}
	}
	operations = append(operations,
		profileOperation{"opencode-continuation", domain.OpenCode, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
			input := f.execution(domain.OpenCode)
			input.Continuation = &domain.ExecutionContinuation{HistoryExecutionID: f.history}
			return openCodeExecutionProfile(ctx, f.config, input, f.history)
		}},
		profileOperation{"sidechat", domain.Codex, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
			input := f.execution(domain.Codex)
			input.Configuration.SidechatPolicy = domain.CodexReadOnlySidechatV1
			return executionProfile(ctx, f.config, domain.Codex, input)
		}},
		profileOperation{"title", domain.Codex, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
			return titleProfile(ctx, f.config, domain.AuxiliaryTitleInput{MachineID: f.machine, AccountID: f.account, SessionID: f.session})
		}},
		profileOperation{"review", domain.Codex, func(ctx context.Context, f *profileFixture) (*nativeprofile.Lease, error) {
			return reviewProfile(ctx, f.config, f.machine, f.account, f.session, domain.NewID())
		}},
	)
	return operations
}

func countProfileDirectories(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "native-profiles", "p"))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return len(entries)
}

// Every adapter and operation resolves the same permanent profile when invoked
// twice with different request identifiers and again after an owner restart.
// The retained history survives and no per-attempt native home is allocated.
func TestEveryNativeLaunchProfileIsStableAcrossRequestsAndOwnerRestart(t *testing.T) {
	ctx := context.Background()
	for _, operation := range profileOperations() {
		operation := operation
		t.Run(string(operation.harness)+"/"+operation.name, func(t *testing.T) {
			config, machine := profileTestConfig(t)
			f := &profileFixture{config: config, machine: machine, account: domain.NewID(), session: domain.NewID(), runtime: domain.NewID()}
			// The conversation history that continuation, compaction and revert resume.
			created := f.execution(operation.harness)
			f.history = created.ExecutionID
			seed, err := executionProfile(ctx, config, operation.harness, created)
			if err != nil {
				t.Fatal(err)
			}
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}
			first, err := operation.acquire(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(first.Root, "retained-history")
			if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
			helper := filepath.Join(first.Root, "helper-link")
			if err := os.Symlink(marker, helper); err != nil {
				t.Skip("symlinks unavailable")
			}
			firstID, firstRoot, directories := first.ID, first.Root, countProfileDirectories(t, config.Root)
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				if attempt == 1 {
					restartProfileOwner(config, machine)
				}
				next, err := operation.acquire(ctx, f)
				if err != nil {
					t.Fatalf("attempt %d was blocked by retained state: %v", attempt, err)
				}
				if next.ID != firstID || next.Root != firstRoot || next.Created {
					t.Fatalf("attempt %d allocated a new native home", attempt)
				}
				if raw, err := os.ReadFile(marker); err != nil || string(raw) != "retained" {
					t.Fatalf("attempt %d lost retained history", attempt)
				}
				if _, err := os.Lstat(helper); err != nil {
					t.Fatalf("attempt %d lost the helper link", attempt)
				}
				if err := next.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if got := countProfileDirectories(t, config.Root); got != directories {
				t.Fatalf("a per-attempt profile directory was allocated: %d != %d", got, directories)
			}
			if _, err := os.Stat(filepath.Join(config.Root, "runtimes")); err == nil {
				t.Fatal("profile resolution allocated a per-attempt runtime directory")
			}
		})
	}
}
