package core

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestPrePushRecordsPushedBranchContextForEveryNewTip(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"exit 1\"\n")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	git := func(args ...string) string {
		t.Helper()
		out, err := Git(ctx, repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	git("checkout", "-B", "main")
	base := git("rev-parse", "HEAD")
	seed := func(branch string) Run {
		t.Helper()
		plan, err := s.Plan(ctx, repo, base)
		if err != nil {
			t.Fatal(err)
		}
		plan.Branch = branch
		if _, err = s.Store.InsertRun(&plan, ""); err != nil {
			t.Fatal(err)
		}
		if err = s.RunOne(plan.ID); err != nil {
			t.Fatal(err)
		}
		r, err := s.Store.Run(plan.ID)
		if err != nil || r.State != Failed {
			t.Fatalf("seed run: %+v %v", r, err)
		}
		return r
	}
	previous := seed("release")
	seed("main") // A newer unrelated branch must not become the comparison base.
	tree := git("rev-parse", "HEAD^{tree}")
	release := git("-c", "commit.gpgsign=false", "commit-tree", tree, "-p", base, "-m", "release tip")
	hotfix := git("-c", "commit.gpgsign=false", "commit-tree", tree, "-p", base, "-m", "hotfix tip")
	git("update-ref", "refs/heads/release", release)
	component, leave, err := s.Enter("daemon")
	if err != nil {
		t.Fatal(err)
	}
	defer leave()
	done := make(chan error, 1)
	go func() { done <- s.Work(ctx, true, component.ID) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	zeros := strings.Repeat("0", 40)
	input := fmt.Sprintf("refs/heads/release %s refs/heads/production %s\n%s %s refs/heads/hotfix %s\n", release, zeros, hotfix, hotfix, zeros)
	gates, err := s.PrePush(ctx, repo, strings.NewReader(input), PushRun)
	if err == nil || len(gates) != 2 || gates[0].Passed || gates[1].Passed {
		t.Fatalf("expected two independently failed tips: %+v %v", gates, err)
	}
	for i, branch := range []string{"release", "hotfix"} {
		r, err := s.Store.Run(gates[i].RunID)
		if err != nil || r.Branch != branch || r.State != Failed {
			t.Fatalf("tip history uses checkout branch: %+v %v", r, err)
		}
		page, err := s.Store.ListFiltered(r.RepositoryID, "", branch, false, false, "", 50)
		if err != nil || len(page.Runs) == 0 || page.Runs[0].ID != r.ID {
			t.Fatalf("branch history lost run: %+v %v", page, err)
		}
		comparison, err := s.Compare(r.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (!comparison.Available || comparison.PreviousID != previous.ID || len(comparison.Continuing) != 1) {
			t.Fatalf("comparison selected the wrong branch: %+v", comparison)
		}
		if i == 1 && comparison.Available {
			t.Fatalf("first hotfix run inherited another branch's history: %+v", comparison)
		}
	}
	if git("branch", "--show-current") != "main" {
		t.Fatal("push preparation changed the checkout")
	}
}
