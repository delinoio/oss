package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrePushPreservesAcceptedAttemptOnStartupFailure(t *testing.T) {
	for _, mode := range []Mode{Daemon, OnDemand} {
		t.Run(string(mode), func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"exit 0\"\n")
			s.Personal.Mode = mode
			passed := runFixture(t, s, repo)
			ctx := context.Background()
			if _, err := Git(ctx, repo, "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "second tip"); err != nil {
				t.Fatal(err)
			}
			sha, err := ResolveCommit(ctx, repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			// Deterministically fail before spawning a daemon/worker on every OS.
			if err := os.Mkdir(filepath.Join(s.Paths.Control, "runner.log"), 0700); err != nil {
				t.Fatal(err)
			}
			zeros := strings.Repeat("0", 40)
			input := fmt.Sprintf("refs/heads/passed %s refs/heads/passed %s\nrefs/heads/pending %s refs/heads/pending %s\n", passed.Commit, zeros, sha, zeros)
			var accepted string
			for attempt := 0; attempt < 2; attempt++ {
				gates, err := s.PrePush(ctx, repo, strings.NewReader(input), PushRun)
				if err == nil || len(gates) != 2 {
					t.Fatalf("lost gate after durable acceptance: %+v %v", gates, err)
				}
				if !gates[0].Passed || gates[0].RunID != passed.ID {
					t.Fatal("lost preceding successful tip")
				}
				gate := gates[1]
				if gate.Passed || gate.State != Queued || gate.Commit != sha || !ValidID(gate.RunID) {
					t.Fatalf("invalid accepted gate: %+v", gate)
				}
				if len(gate.Diagnostics) != 1 || gate.Diagnostics[0].Code != "startup-failed" || !strings.Contains(gate.Diagnostics[0].Hint, gate.RunID) {
					t.Fatalf("missing recovery diagnostic: %+v", gate)
				}
				if accepted != "" && accepted != gate.RunID {
					t.Fatal("retry created a duplicate attempt")
				}
				accepted = gate.RunID
				run, err := s.Store.Run(accepted)
				if err != nil || run.Commit != sha || run.State != Queued || run.Branch != "pending" || run.Checks[0].StartedAt != nil {
					t.Fatalf("receipt differs from storage: %+v %v", run, err)
				}
			}
			var count int
			if err := s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 2 {
				t.Fatalf("unexpected acceptance count: %d %v", count, err)
			}
			if gates, err := s.PrePush(ctx, repo, strings.NewReader("refs/heads/main invalid refs/heads/main "+zeros+"\n"), PushRun); err == nil || len(gates) != 0 {
				t.Fatal("fabricated a pre-acceptance receipt")
			}
		})
	}
}
