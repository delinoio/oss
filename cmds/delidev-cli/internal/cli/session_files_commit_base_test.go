// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
)

// Both public commands and both session files aliases share one negotiation.
var branchCommands = []string{"diff", "review-context", "files diff", "files review-context"}

func commitBaseArgs(command, name string) []string {
	return branchArgs(command, "--comparison", "branch", "--base-type", "commit", "--base-name", name)
}

func resultDiff(t *testing.T, command string, envelope map[string]any) map[string]any {
	t.Helper()
	result, _ := envelope["result"].(map[string]any)
	diff, _ := result["diff"].(map[string]any)
	if diff == nil {
		t.Fatal("result has no diff", command, envelope)
	}
	return diff
}

func TestSessionBranchCommitBaseAbsentFromChoicesReachesFinalRead(t *testing.T) {
	for _, command := range branchCommands {
		t.Run(command, func(t *testing.T) {
			f := newBranchFixture(t)
			commit := strings.Repeat("a", 40)
			code, envelope := f.run(context.Background(), commitBaseArgs(command, commit)...)
			if code != 0 {
				t.Fatal("explicit commit base was rejected", code, envelope)
			}
			reads := f.snapshot()
			if len(reads) != 2 || reads[0].query.Operation != domain.WorkspaceGitDiffOptions || reads[0].query.BaseRef != (domain.Reference{}) {
				t.Fatal("options negotiation must precede exactly one final read", reads)
			}
			wantProcedure := "ReadSessionWorkspace"
			if strings.HasSuffix(command, "review-context") {
				wantProcedure = "ReadSessionReviewContext"
			}
			if !strings.HasSuffix(reads[1].procedure, wantProcedure) || reads[1].query.Comparison != domain.DiffBranch || reads[1].query.BaseRef != (domain.Reference{Type: domain.CommitReference, Name: commit}) {
				t.Fatal("final read changed the typed commit base", reads[1])
			}
			diff := resultDiff(t, command, envelope)
			ref, _ := diff["base_ref"].(map[string]any)
			if ref["type"] != string(domain.CommitReference) || ref["name"] != commit || diff["base_commit"] != branchFixtureCommit || diff["merge_base"] != branchFixtureCommit || diff["patch"] != branchFixturePatch {
				t.Fatal("result identity must come from the Worker observation", diff)
			}
			if strings.HasSuffix(command, "review-context") {
				files, _ := envelope["result"].(map[string]any)["files"].([]any)
				if len(files) != 1 {
					t.Fatal("review coordinates were not derived from the validated patch", envelope)
				}
			}
		})
	}
}

func TestSessionBranchCommitBaseKeepsWorkerErrorsWithoutFallback(t *testing.T) {
	for _, command := range []string{"diff", "review-context"} {
		for _, problem := range []domain.Code{domain.Unavailable, domain.InvalidArgument} {
			t.Run(command+"/"+string(problem), func(t *testing.T) {
				f := newBranchFixture(t)
				f.diff = func(context.Context, domain.WorkspaceReadQuery) (*domain.WorkspaceDiff, error) {
					return nil, rpc.Error(domain.SafeError(domain.Fail(problem, "The Worker rejected the commit base.", "Choose a locally present full commit.")), "")
				}
				// Uppercase and abbreviated IDs are Worker decisions, not CLI decisions.
				code, envelope := f.run(context.Background(), commitBaseArgs(command, "ABCDEF0")...)
				if code == 0 || envelopeError(envelope) != problem {
					t.Fatal("Worker error was replaced", code, envelope)
				}
				if reads := f.snapshot(); len(reads) != 2 {
					t.Fatal("expected one options read and one final read", reads)
				}
			})
		}
	}
}

func TestSessionBranchInventoryMatchingIsPreserved(t *testing.T) {
	remote := domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.RemoteBranch, Remote: "origin", Name: "main"}, Available: true}
	configured := domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.CommitReference, Name: branchFixtureCommit}, Available: true, Configured: true}
	missing := domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.LocalBranch, Name: "gone"}, Available: false}
	missingConfigured := domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.CommitReference, Name: strings.Repeat("c", 40)}, Available: false, Configured: true}
	for _, tc := range []struct {
		name  string
		args  []string
		final bool
	}{
		{"available local branch", []string{"--base-type", "local-branch", "--base-name", "main"}, true},
		{"available remote branch", []string{"--base-type", "remote-branch", "--base-remote", "origin", "--base-name", "main"}, true},
		{"configured commit", []string{"--base-type", "commit", "--base-name", branchFixtureCommit}, true},
		{"local branch missing from choices", []string{"--base-type", "local-branch", "--base-name", "feature"}, false},
		{"remote branch missing from choices", []string{"--base-type", "remote-branch", "--base-remote", "origin", "--base-name", "feature"}, false},
		{"unavailable local branch", []string{"--base-type", "local-branch", "--base-name", "gone"}, false},
		{"unavailable configured commit", []string{"--base-type", "commit", "--base-name", strings.Repeat("c", 40)}, false},
		{"branch name used as remote branch on wrong remote", []string{"--base-type", "remote-branch", "--base-remote", "upstream", "--base-name", "main"}, false},
	} {
		for _, command := range []string{"diff", "review-context"} {
			t.Run(tc.name+"/"+command, func(t *testing.T) {
				f := newBranchFixture(t)
				f.options = func() (*domain.WorkspaceReadResult, error) {
					// An accepted inventory carries at most one configured reference.
					if strings.Contains(strings.Join(tc.args, " "), strings.Repeat("c", 40)) {
						return branchOptionsResult(branchMainChoice(), remote, missing, missingConfigured), nil
					}
					return branchOptionsResult(branchMainChoice(), remote, configured, missing), nil
				}
				code, envelope := f.run(context.Background(), branchArgs(command, append([]string{"--comparison", "branch"}, tc.args...)...)...)
				reads := f.snapshot()
				if tc.final {
					if code != 0 || len(reads) != 2 {
						t.Fatal("available base stopped", code, envelope, reads)
					}
					return
				}
				if code == 0 || envelopeError(envelope) != domain.Unavailable || len(reads) != 1 || reads[0].query.Operation != domain.WorkspaceGitDiffOptions {
					t.Fatal("unavailable base reached the final read", code, envelope, reads)
				}
			})
		}
	}
}

func TestSessionBranchNegotiationFailuresStopBeforeFinalRead(t *testing.T) {
	tooMany := make([]domain.WorkspaceDiffChoice, 0, domain.WorkspaceDiffReferenceLimit+2)
	for i := 0; i < domain.WorkspaceDiffReferenceLimit+2; i++ {
		tooMany = append(tooMany, domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.LocalBranch, Name: "branch-" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + strings.Repeat("y", i/26+1)}, Available: true})
	}
	for _, tc := range []struct {
		name    string
		options func() (*domain.WorkspaceReadResult, error)
		code    domain.Code
	}{
		{"old server", func() (*domain.WorkspaceReadResult, error) {
			return nil, rpc.Error(domain.SafeError(domain.Fail(domain.InvalidArgument, "Unknown operation.", "Update the server.")), "")
		}, domain.InvalidArgument},
		{"old worker", func() (*domain.WorkspaceReadResult, error) {
			return nil, rpc.Error(domain.SafeError(domain.Fail(domain.Unsupported, "Unsupported operation.", "Update the Worker.")), "")
		}, domain.Unsupported},
		{"options failure", func() (*domain.WorkspaceReadResult, error) {
			return nil, rpc.Error(domain.SafeError(domain.Fail(domain.Unavailable, "Options unavailable.", "Retry later.")), "")
		}, domain.Unavailable},
		{"invalid version", func() (*domain.WorkspaceReadResult, error) {
			result := branchOptionsResult(branchMainChoice())
			result.DiffOptions.Version = 2
			return result, nil
		}, ""},
		{"oversized inventory", func() (*domain.WorkspaceReadResult, error) {
			return branchOptionsResult(tooMany...), nil
		}, ""},
		{"commit choice that is not configured", func() (*domain.WorkspaceReadResult, error) {
			return branchOptionsResult(branchMainChoice(), domain.WorkspaceDiffChoice{Reference: domain.Reference{Type: domain.CommitReference, Name: strings.Repeat("a", 40)}, Available: true}), nil
		}, ""},
	} {
		for _, command := range []string{"diff", "review-context"} {
			t.Run(tc.name+"/"+command, func(t *testing.T) {
				f := newBranchFixture(t)
				f.options = tc.options
				code, envelope := f.run(context.Background(), commitBaseArgs(command, strings.Repeat("a", 40))...)
				reads := f.snapshot()
				if code == 0 || len(reads) != 1 || reads[0].query.Operation != domain.WorkspaceGitDiffOptions {
					t.Fatal("negotiation failure reached the final read", code, envelope, reads)
				}
				if tc.code != "" && envelopeError(envelope) != tc.code {
					t.Fatal("negotiation failure class changed", envelopeError(envelope), envelope)
				}
			})
		}
	}
}

func TestSessionNonBranchComparisonsKeepOneRead(t *testing.T) {
	for _, comparison := range []domain.WorkspaceDiffComparison{domain.DiffWorkingTree, domain.DiffStaged} {
		for _, command := range []string{"diff", "review-context"} {
			t.Run(string(comparison)+"/"+command, func(t *testing.T) {
				f := newBranchFixture(t)
				code, envelope := f.run(context.Background(), branchArgs(command, "--comparison", string(comparison))...)
				reads := f.snapshot()
				if code != 0 || len(reads) != 1 || reads[0].query.Operation != domain.WorkspaceGitDiff || reads[0].query.Comparison != comparison || reads[0].query.BaseRef != (domain.Reference{}) {
					t.Fatal("non-branch comparison changed its single read", code, envelope, reads)
				}
			})
		}
	}
}
