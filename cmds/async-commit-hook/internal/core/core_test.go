package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, configuration string) (*Service, string) {
	t.Helper()
	return fixtureWithObjectFormat(t, configuration, "sha1")
}

func fixtureWithObjectFormat(t *testing.T, configuration, format string) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if e := os.Mkdir(repo, 0700); e != nil {
		t.Fatal(e)
	}
	paths := Paths{Config: filepath.Join(root, "config.toml"), State: filepath.Join(root, "state"), Control: filepath.Join(root, "control")}
	s, e := Open(paths)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	git := func(args ...string) {
		t.Helper()
		if _, e := Git(context.Background(), repo, args...); e != nil {
			t.Fatal(e)
		}
	}
	git("init", "--quiet", "--template=", "--object-format="+format)
	git("config", "user.email", "ach-test@example.invalid")
	git("config", "user.name", "ach test")
	if _, _, e = s.Init(context.Background(), repo); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(repo, ProjectFile), []byte(configuration), 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(repo, "source.txt"), []byte("committed"), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "fixture")
	return s, repo
}
func runFixture(t *testing.T, s *Service, repo string) Run {
	t.Helper()
	receipt, e := s.Submit(context.Background(), repo, "", false)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RunOne(receipt.RunID); e != nil {
		t.Fatal(e)
	}
	r, e := s.Store.Run(receipt.RunID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func TestConfigurationRejectsInvalidGraphs(t *testing.T) {
	for _, config := range []string{`version=2`, `version=1
unknown=true`, `version=1
[checks.a]
command="true"
depends_on=["absent"]`, `version=1
[checks.a]
command="true"
depends_on=["b"]
[checks.b]
command="true"
depends_on=["a"]`, `version=1
[checks.a]
command="true"
[[checks.a.reports]]
kind="junit"
path="../secret"`} {
		if _, e := ParseProject([]byte(config)); e == nil {
			t.Errorf("accepted invalid config %s", config)
		}
	}
}

func TestAtomicInitializationCannotOverwriteExistingConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() { results <- AtomicCreate(path, []byte("user configuration"), 0600) }()
	}
	created := 0
	for i := 0; i < 8; i++ {
		err := <-results
		if err == nil {
			created++
		} else if !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	if created != 1 {
		t.Fatalf("created %d configurations", created)
	}
	if err := AtomicCreate(path, []byte("replacement"), 0600); !os.IsExist(err) {
		t.Fatal("existing configuration replaced")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "user configuration" {
		t.Fatal("configuration changed")
	}
}
func TestRedactionAcrossEveryBoundary(t *testing.T) {
	input := []byte("prefix-super-secret-tail-secret-and-super-secret")
	for size := 1; size < len(input); size++ {
		var out bytes.Buffer
		r := NewRedactor(&out, []string{"super-secret", "secret"})
		for i := 0; i < len(input); i += size {
			end := i + size
			if end > len(input) {
				end = len(input)
			}
			if _, e := r.Write(input[i:end]); e != nil {
				t.Fatal(e)
			}
		}
		if e := r.Close(); e != nil {
			t.Fatal(e)
		}
		if strings.Contains(out.String(), "secret") || out.String() != "prefix-[REDACTED]-tail-[REDACTED]-and-[REDACTED]" {
			t.Fatalf("boundary %d: %q", size, out.String())
		}
	}
}
func TestReportSemantics(t *testing.T) {
	cases := []struct {
		kind     ReportKind
		body     string
		failures int
		invalid  bool
	}{{JUnit, `<testsuite tests="1"><testcase name="ok"/></testsuite>`, 0, false}, {JUnit, `<testsuite><testcase name="bad" file="x.go" line="9"><failure message="failed">details</failure></testcase></testsuite>`, 1, false}, {JUnit, `<testsuite failures="2"/>`, 1, false}, {JUnit, `broken`, 0, true}, {JUnit, `<!DOCTYPE foo><testsuite/>`, 0, true}, {GoTest, "{\"Action\":\"fail\",\"Package\":\"p\",\"Test\":\"bad\"}\n", 1, false}, {GoTest, "{\"Action\":\"run\",\"Test\":\"unfinished\"}\n", 0, true}, {GoTest, "not json", 0, true}}
	for _, c := range cases {
		f, e := ParseReport(c.kind, []byte(c.body), "test", "command", "log")
		if (e != nil) != c.invalid || len(f) != c.failures {
			t.Errorf("%s: failures=%v err=%v", c.body, f, e)
		}
	}
}
func TestCommittedSourceAndLatestAttempt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"test $(cat source.txt) = committed\"\n")
	if e := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("uncommitted"), 0600); e != nil {
		t.Fatal(e)
	}
	r := runFixture(t, s, repo)
	if !s.GateRun(r).Passed {
		t.Fatalf("run did not pass: %+v", r)
	}
	next, e := s.Submit(context.Background(), repo, "", false)
	if e != nil {
		t.Fatal(e)
	}
	gate, e := s.Gate(context.Background(), repo, "")
	if e != nil || gate.Passed || gate.RunID != next.RunID {
		t.Fatalf("older success accepted: %+v %v", gate, e)
	}
	if _, e = os.Stat(filepath.Join(s.Store.Root, "workspaces", r.ID)); !os.IsNotExist(e) {
		t.Fatal("workspace was not cleaned")
	}
}
func TestOptionalFailureBlockedAndMissingReports(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, repo := fixture(t, `version=1
[checks.required]
command="true"
[[checks.required.reports]]
kind="junit"
path="missing.xml"
[checks.dependent]
command="true"
depends_on=["required"]
[checks.optional]
command="exit 7"
optional=true
`)
	r := runFixture(t, s, repo)
	states := map[string]State{}
	for _, c := range r.Checks {
		states[c.Name] = c.State
	}
	if states["required"] != Failed || states["dependent"] != Blocked || states["optional"] != Failed || s.GateRun(r).Passed {
		t.Fatalf("incorrect graph outcomes: %+v", r)
	}
}
func TestAcknowledgementIdempotencyAndAutomaticDedup(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	a, e := s.Submit(context.Background(), repo, "", true)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Submit(context.Background(), repo, "", true)
	if e != nil || a.RunID != b.RunID {
		t.Fatalf("automatic duplication: %v %v", a, b)
	}
	r, _ := s.Store.Run(a.RunID)
	if r.AcknowledgedAt != nil {
		t.Fatal("read acknowledged")
	}
	for _, state := range []State{Queued, Preparing, Running, Collecting} {
		r.State = state
		if err := s.Store.SaveRun(r); err != nil {
			t.Fatal(err)
		}
		if err := s.Store.Ack(r.ID); err == nil {
			t.Fatal("acknowledged unfinished run")
		}
		stored, _ := s.Store.Run(r.ID)
		if stored.AcknowledgedAt != nil {
			t.Fatal("premature ack was persisted")
		}
	}
	r.State = Queued
	if err := s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, e = s.Wait(ctx, a.RunID); e == nil {
		t.Fatal("wait did not expire")
	}
	r, _ = s.Store.Run(a.RunID)
	if r.State != Queued {
		t.Fatal("wait cancelled execution")
	}
	r.State = Failed
	if err := s.Store.SaveRun(r); err != nil {
		t.Fatal(err)
	}
	page, err := s.Store.List("", true, "", 50)
	if err != nil || len(page.Runs) != 1 {
		t.Fatal("completed failure missing from inbox", err)
	}
	if err := s.Store.Ack(r.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Store.Run(r.ID)
	first := *r.AcknowledgedAt
	if err := s.Store.Ack(r.ID); err != nil {
		t.Fatal(err)
	}
	r, _ = s.Store.Run(r.ID)
	if !first.Equal(*r.AcknowledgedAt) {
		t.Fatal("ack changed timestamp")
	}
	page, err = s.Store.List("", true, "", 50)
	if err != nil || len(page.Runs) != 0 {
		t.Fatal("acknowledged failure remains in inbox", err)
	}
}
func TestPruneCannotResurrectSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fixture")
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo hello\"\n")
	_ = runFixture(t, s, repo)
	r := runFixture(t, s, repo)
	r.CreatedAt = time.Now().Add(-72 * time.Hour)
	if e := s.Store.SaveRun(r); e != nil {
		t.Fatal(e)
	}
	preview, e := s.Prune(true, 1, 0)
	if e != nil || len(preview.RunIDs) != 1 {
		t.Fatalf("preview %+v %v", preview, e)
	}
	if _, e = s.Prune(false, 1, 0); e != nil {
		t.Fatal(e)
	}
	gate, e := s.Gate(context.Background(), repo, "")
	if e != nil || gate.Passed || gate.State != Expired {
		t.Fatalf("resurrected result: %+v %v", gate, e)
	}
	before, _ := s.Store.Run(r.ID)
	for _, dry := range []bool{true, false, false} {
		result, err := s.Prune(dry, 1, 0)
		if err != nil || len(result.RunIDs) != 0 {
			t.Fatalf("repeated tombstone: %+v %v", result, err)
		}
	}
	// Resume cleanup after a crash between persisting expiry and deleting files.
	orphan := filepath.Join(s.Store.Root, "evidence", r.ID)
	if err := os.MkdirAll(orphan, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "leftover"), []byte("evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := s.Prune(false, 0, 0)
	if err != nil || len(result.RunIDs) != 1 {
		t.Fatalf("cleanup not resumed: %+v %v", result, err)
	}
	after, _ := s.Store.Run(r.ID)
	if !bytes.Equal(Encode(before), Encode(after)) {
		t.Fatal("tombstone changed during repeated pruning")
	}
}
func TestAgentPreservesUnrelatedSettings(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	path := filepath.Join(repo, "opencode.jsonc")
	original := []byte("{\n // user comment\n \"theme\": \"custom\",\n \"mcp\": {\"other\": {\"type\":\"local\",\"command\":[\"other\"]}}\n}\n")
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Agent("opencode", "project", repo, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Agent("opencode", "project", repo, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Agent("opencode", "project", repo, true); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("user comment")) || !bytes.Contains(b, []byte("other")) || !bytes.Contains(b, []byte("custom")) {
		t.Fatalf("settings lost: %s", b)
	}
}
