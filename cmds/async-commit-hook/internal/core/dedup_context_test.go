package core

import (
	"context"
	"testing"
)

func TestAutomaticDeduplicationIncludesExecutionContext(t *testing.T) {
	s, repo := fixture(t, `version=1
[checks.test]
command="echo test"
[[checks.test.environment]]
name="ACH_TEST_PUBLIC_INPUT"
[[checks.test.environment]]
name="ACH_TEST_SECRET_INPUT"
secret=true
`)
	ctx := context.Background()
	t.Setenv("ACH_TEST_PUBLIC_INPUT", "first")
	t.Setenv("ACH_TEST_SECRET_INPUT", "secret-one")
	submit := func(automatic bool) Receipt {
		t.Helper()
		r, err := s.Submit(ctx, repo, "", automatic)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	first := submit(true)
	if again := submit(true); again.RunID != first.RunID {
		t.Fatal("same context did not deduplicate")
	}
	t.Setenv("ACH_TEST_SECRET_INPUT", "secret-two")
	if again := submit(true); again.RunID != first.RunID {
		t.Fatal("secret value affected compatibility")
	}
	t.Setenv("ACH_TEST_PUBLIC_INPUT", "second")
	second := submit(true)
	if second.RunID == first.RunID {
		t.Fatal("new public context reused an incompatible attempt")
	}
	if again := submit(true); again.RunID != second.RunID {
		t.Fatal("new context retry did not deduplicate")
	}
	if explicit := submit(false); explicit.RunID == second.RunID {
		t.Fatal("explicit attempt deduplicated")
	}
	plan, err := s.Plan(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.Store.Run(second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Fingerprint != plan.Fingerprint || current.Environment["ACH_TEST_PUBLIC_INPUT"] != "second" {
		t.Fatal("new attempt has stale context")
	}
}
