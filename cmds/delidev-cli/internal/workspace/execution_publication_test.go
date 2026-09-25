package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestFailedUnlaunchedClaimPublicationCanRetry(t *testing.T) {
	for _, continuation := range []bool{false, true} {
		for _, boundary := range []string{"owner-sync", "before-rename", "after-rename"} {
			t.Run(boundary+map[bool]string{false: "/first", true: "/continuation"}[continuation], func(t *testing.T) {
				m, input, manifest := chatExecutionFixture(t)
				var previous ExecutionPredecessor
				var prior *executionClaim
				if continuation {
					previous = closeFirstExecution(t, m, input, manifest)
					saved, err := m.readExecutionClaim(input.SessionID)
					if err != nil {
						t.Fatal(err)
					}
					prior = &saved
					if err := m.retainClosedExecutionClaim(saved); err != nil {
						t.Fatal(err)
					}
				}
				claim := executionClaim{Version: 2, SessionID: input.SessionID, JobID: domain.NewID(), ExecutionID: domain.NewID(), State: executionClaimActive, ManifestDigest: strings.Repeat("a", 64), WorkspaceDigest: strings.Repeat("b", 64)}
				if prior != nil {
					claim.PreviousJobID, claim.PreviousExecutionID = prior.JobID, prior.ExecutionID
				}
				failure := errors.New("injected publication failure")
				write := func(path string, raw []byte) error {
					if boundary == "after-rename" {
						if err := security.WriteAtomic(path, raw); err != nil {
							t.Fatal(err)
						}
					}
					return failure
				}
				syncParent := func(path string) error {
					if boundary == "owner-sync" {
						return failure
					}
					return security.SyncParent(path)
				}
				if err := m.publishExecutionClaim(claim, prior, write, syncParent); err == nil {
					t.Fatal("publication failure succeeded")
				}
				if _, err := os.Lstat(filepath.Join(m.Git.ProcessRoot, string(claim.JobID))); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("empty owner prevented retry", err)
				}
				if prior != nil {
					current, err := m.readExecutionClaim(input.SessionID)
					if err != nil || current != *prior {
						t.Fatal("original closed ownership changed", err)
					}
				}
				var lease *ExecutionLease
				var err error
				if continuation {
					lease, err = m.ClaimContinuation(context.Background(), claim.JobID, claim.ExecutionID, previous, input, manifest)
				} else {
					lease, err = m.ClaimFirstExecution(context.Background(), claim.JobID, claim.ExecutionID, input, manifest)
				}
				if err != nil {
					t.Fatal("publication rollback could not retry exact identities", err)
				}
				if err := lease.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestUnlaunchedClaimRollbackPreservesUnexpectedOwnership(t *testing.T) {
	for _, change := range []string{"nonempty-owner", "foreign-claim"} {
		t.Run(change, func(t *testing.T) {
			m, input, _ := chatExecutionFixture(t)
			claim := executionClaim{Version: 2, SessionID: input.SessionID, JobID: domain.NewID(), ExecutionID: domain.NewID(), State: executionClaimActive, ManifestDigest: strings.Repeat("a", 64), WorkspaceDigest: strings.Repeat("b", 64)}
			owner := filepath.Join(m.Git.ProcessRoot, string(claim.JobID))
			write := func(path string, raw []byte) error {
				if err := security.WriteAtomic(path, raw); err != nil {
					t.Fatal(err)
				}
				if change == "nonempty-owner" {
					if err := os.WriteFile(filepath.Join(owner, "ownership-evidence"), []byte("keep"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					foreign := strings.Replace(string(raw), string(claim.ExecutionID), string(domain.NewID()), 1)
					if err := security.WriteAtomic(path, []byte(foreign)); err != nil {
						t.Fatal(err)
					}
				}
				return errors.New("injected failure")
			}
			if err := m.publishExecutionClaim(claim, nil, write, security.SyncParent); err == nil {
				t.Fatal("failure accepted")
			}
			if _, err := os.Stat(owner); err != nil {
				t.Fatal("uncertain owner removed", err)
			}
			if _, err := m.readExecutionClaim(input.SessionID); err != nil {
				t.Fatal("claim evidence removed", err)
			}
		})
	}
}
