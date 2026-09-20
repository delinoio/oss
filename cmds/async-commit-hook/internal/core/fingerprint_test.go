package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFingerprintIncludesOnlyExecutionContext(t *testing.T) {
	p, err := ParseProject([]byte(`version=1
[checks.build]
command="echo build"
[checks.test]
command="echo test"
depends_on=["build"]
[[checks.test.environment]]
name="PUBLIC_INPUT"
[[checks.test.environment]]
name="SECRET_INPUT"
secret=true
credential="test-key"
[[checks.test.reports]]
kind="junit"
path="report.xml"
`))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PUBLIC_INPUT": "one"}
	want := Fingerprint(p, env)
	// Preserve native-link digests; Windows now has a distinct source representation.
	legacy := Hash(Encode(struct {
		Project     Project
		Environment map[string]string
		OS, Arch    string
	}{p, env, runtime.GOOS, runtime.GOARCH}))
	if (want == legacy) != (runtime.GOOS != "windows") {
		t.Fatal("source representation fingerprint compatibility changed incorrectly")
	}
	for _, policy := range []PushPolicy{PushBlock, PushWait, PushRun} {
		for _, base := range []string{"", "origin/main", "another-branch"} {
			p.PrePush, p.DiffBase = policy, base
			if Fingerprint(p, env) != want {
				t.Fatalf("non-execution settings affect identity: %s %s", policy, base)
			}
			if p.PrePush != policy || p.DiffBase != base {
				t.Fatal("fingerprinting mutated the configuration snapshot")
			}
		}
	}
	for name, mutate := range map[string]func(*Command){
		"command":          func(c *Command) { c.Command = "echo changed" },
		"dependencies":     func(c *Command) { c.DependsOn = nil },
		"os":               func(c *Command) { c.OS = []string{"linux"} },
		"shell":            func(c *Command) { c.Shell = Bash },
		"optional":         func(c *Command) { c.Optional = true },
		"policy":           func(c *Command) { c.Policy = Queue },
		"group":            func(c *Command) { c.Group = "shared" },
		"environment":      func(c *Command) { c.Environment[0].Required = true },
		"secret-reference": func(c *Command) { c.Environment[1].Credential = "another-key" },
		"report-kind":      func(c *Command) { c.Reports[0].Kind = GoTest },
		"report-path":      func(c *Command) { c.Reports[0].Path = "another.xml" },
	} {
		t.Run(name, func(t *testing.T) {
			var changed Project
			if err := json.Unmarshal(Encode(p), &changed); err != nil {
				t.Fatal(err)
			}
			c := changed.Checks["test"]
			mutate(&c)
			changed.Checks["test"] = c
			if Fingerprint(changed, env) == want {
				t.Fatal("execution change did not change identity")
			}
		})
	}
	if Fingerprint(p, map[string]string{"PUBLIC_INPUT": "two"}) == want {
		t.Fatal("public environment value did not change identity")
	}
}

func TestComparisonSurvivesPushPolicyAndDiffBaseChanges(t *testing.T) {
	base := "version=1\n"
	checks := "[checks.test]\ncommand=\"exit 1\"\n"
	s, repo := fixture(t, base+checks)
	previous := runFixture(t, s, repo)
	for _, policy := range []PushPolicy{PushWait, PushRun} {
		config := base + "pre_push=\"" + string(policy) + "\"\ndiff_base=\"another-branch\"\n" + checks
		if err := os.WriteFile(filepath.Join(repo, ProjectFile), []byte(config), 0600); err != nil {
			t.Fatal(err)
		}
		for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "non-execution settings"}} {
			if _, err := Git(context.Background(), repo, args...); err != nil {
				t.Fatal(err)
			}
		}
		current := runFixture(t, s, repo)
		if current.Config.PrePush != policy || current.Config.DiffBase != "another-branch" {
			t.Fatal("complete accepted configuration was not retained")
		}
		for _, explicit := range []string{"", previous.ID} {
			comparison, err := s.Compare(current.ID, explicit)
			if err != nil || !comparison.Available || comparison.PreviousID != previous.ID || len(comparison.Continuing) != 1 || len(comparison.New) != 0 || len(comparison.Resolved) != 0 {
				t.Fatalf("comparison lost after non-execution change: %+v %v", comparison, err)
			}
		}
		previous = current
	}
}
