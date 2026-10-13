// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestBranchComparisonRequestedRecognizesEveryBranchForm(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"diff separate", []string{"diff", "--id", "i", "--comparison", "branch"}, true},
		{"diff inline", []string{"diff", "--id", "i", "--comparison=branch"}, true},
		{"diff single dash", []string{"diff", "-comparison", "branch"}, true},
		{"diff flags after comparison", []string{"diff", "--comparison=branch", "--base-type", "commit", "--base-name", "x"}, true},
		{"review-context separate", []string{"review-context", "--comparison", "branch"}, true},
		{"review-context inline", []string{"review-context", "--comparison=branch"}, true},
		{"files diff separate", []string{"files", "diff", "--comparison", "branch"}, true},
		{"files diff inline", []string{"files", "diff", "--comparison=branch"}, true},
		{"files review-context separate", []string{"files", "review-context", "--comparison", "branch"}, true},
		{"files review-context inline", []string{"files", "review-context", "--comparison=branch"}, true},
		{"last comparison wins", []string{"diff", "--comparison", "staged", "--comparison", "branch"}, true},
		{"last comparison wins away from branch", []string{"diff", "--comparison=branch", "--comparison=staged"}, false},
		{"default working tree", []string{"diff", "--id", "i"}, false},
		{"working tree", []string{"diff", "--comparison", "working-tree"}, false},
		{"staged", []string{"review-context", "--comparison=staged"}, false},
		{"creation", []string{"files", "diff", "--comparison", "creation"}, false},
		{"branch as another flag value", []string{"diff", "--base-name", "branch"}, false},
		{"unknown flag", []string{"diff", "--comparison", "branch", "--unknown"}, false},
		{"positional argument", []string{"diff", "--comparison", "branch", "extra"}, false},
		{"files read", []string{"files", "read", "--comparison", "branch"}, false},
		{"files list", []string{"files", "list"}, false},
		{"files roots", []string{"files", "roots"}, false},
		{"files without operation", []string{"files"}, false},
		{"other session command", []string{"steer", "--comparison", "branch"}, false},
		{"no arguments", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := branchComparisonRequested(tc.args); got != tc.want {
				t.Fatalf("branchComparisonRequested(%v)=%v want %v", tc.args, got, tc.want)
			}
		})
	}
}

// The first observation carries the command deadline the CLI propagated.
func TestSessionBranchCommandsGetFortyFiveSecondContext(t *testing.T) {
	branchForms := [][]string{
		{"--comparison", "branch"},
		{"--comparison=branch"},
	}
	for _, command := range branchCommands {
		for _, form := range branchForms {
			t.Run(command+"/"+form[0], func(t *testing.T) {
				f := newBranchFixture(t)
				args := branchArgs(command, append(form, "--base-type", "local-branch", "--base-name", "main")...)
				code, envelope := f.run(context.Background(), args...)
				reads := f.snapshot()
				if code != 0 || len(reads) != 2 {
					t.Fatal("branch command failed", code, envelope, reads)
				}
				for _, read := range reads {
					if read.remaining <= 40*time.Second || read.remaining > branchComparisonCommandBudget {
						t.Fatalf("branch request deadline %s does not match %s", read.remaining, branchComparisonCommandBudget)
					}
				}
			})
		}
	}
}

func TestSessionNonBranchCommandsKeepThirtySecondContext(t *testing.T) {
	for _, command := range branchCommands {
		for _, comparison := range []string{"working-tree", "staged", "creation"} {
			t.Run(command+"/"+comparison, func(t *testing.T) {
				f := newBranchFixture(t)
				code, envelope := f.run(context.Background(), branchArgs(command, "--comparison", comparison)...)
				reads := f.snapshot()
				if code != 0 || len(reads) != 1 {
					t.Fatal("non-branch command did not make exactly one read", code, envelope, reads)
				}
				if reads[0].remaining <= 25*time.Second || reads[0].remaining > 30*time.Second {
					t.Fatalf("non-branch request deadline %s does not match 30s", reads[0].remaining)
				}
			})
		}
	}
}

// Reproduces the 14s / 2.5s / 14s sequence: each observation is within its own
// 15-second bound, yet the sequence outlives the former shared 30-second context.
func TestSessionBranchSequentialObservationsCompleteWithinBudget(t *testing.T) {
	for _, command := range []string{"diff", "review-context"} {
		t.Run(command, func(t *testing.T) {
			// Overlap the isolated real-time fixtures on Linux; keep native
			// Windows and macOS scheduling serialized.
			if runtime.GOOS == "linux" {
				t.Parallel()
			}
			f := newBranchFixture(t)
			f.delay = func(ctx context.Context, index int) error {
				wait := 14 * time.Second
				if index == 0 {
					// The first response also spends 2.5 seconds in delivery.
					wait += 2500 * time.Millisecond
				}
				timer := time.NewTimer(wait)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
					return nil
				}
			}
			started := time.Now()
			code, envelope := f.run(context.Background(), commitBaseArgs(command, branchFixtureCommit)...)
			elapsed := time.Since(started)
			reads := f.snapshot()
			if code != 0 || len(reads) != 2 {
				t.Fatal("valid sequential observations were abandoned", code, envelope, reads, elapsed)
			}
			if elapsed < 30*time.Second {
				t.Fatalf("fixture did not exceed the former 30-second context: %s", elapsed)
			}
			if reads[1].remaining < 15*time.Second {
				t.Fatalf("second request deadline %s is shorter than the 15-second observation bound", reads[1].remaining)
			}
		})
	}
}

func TestSessionBranchShorterCallerDeadlineStopsCommand(t *testing.T) {
	for _, command := range []string{"diff", "review-context"} {
		t.Run(command, func(t *testing.T) {
			f := newBranchFixture(t)
			f.delay = func(ctx context.Context, index int) error {
				if index == 0 {
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
			defer cancel()
			started := time.Now()
			code, envelope := f.run(ctx, commitBaseArgs(command, branchFixtureCommit)...)
			if code == 0 || time.Since(started) > 10*time.Second {
				t.Fatal("caller deadline did not stop the command", code, envelope, time.Since(started))
			}
			time.Sleep(200 * time.Millisecond)
			reads := f.snapshot()
			if len(reads) != 1 || reads[0].query.Operation != domain.WorkspaceGitDiffOptions || envelope["result"] != nil {
				t.Fatal("work continued or published a result after the caller deadline", reads, envelope)
			}
			if reads[0].remaining > time.Second {
				t.Fatalf("caller deadline was extended to %s", reads[0].remaining)
			}
		})
	}
}

func TestSessionBranchCallerCancellationStopsCommand(t *testing.T) {
	for _, command := range []string{"diff", "review-context"} {
		t.Run(command, func(t *testing.T) {
			f := newBranchFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.delay = func(reqCtx context.Context, index int) error {
				if index != 0 {
					return nil
				}
				cancel()
				<-reqCtx.Done()
				return reqCtx.Err()
			}
			started := time.Now()
			code, envelope := f.run(ctx, commitBaseArgs(command, branchFixtureCommit)...)
			if code == 0 || time.Since(started) > 10*time.Second {
				t.Fatal("cancellation did not stop the command", code, envelope, time.Since(started))
			}
			time.Sleep(200 * time.Millisecond)
			if reads := f.snapshot(); len(reads) != 1 || envelope["result"] != nil {
				t.Fatal("work continued or published a result after cancellation", reads, envelope)
			}
		})
	}
}
