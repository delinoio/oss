package core

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairingExpiryReplayRevokeAndHTTPBoundary(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	code, err := s.PairingCode()
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := s.Pair(code, "test browser")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Pair(code, "replay"); err == nil {
		t.Fatal("pairing replay accepted")
	}
	if !s.Authenticate("Bearer " + token) {
		t.Fatal("token rejected")
	}
	code, _ = s.PairingCode()
	_, err = s.Store.DB.Exec("UPDATE pairings SET expires=?", time.Now().Add(-time.Minute).Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Pair(code, "expired"); err == nil {
		t.Fatal("expired pairing accepted")
	}
	handler := s.Handler()
	for _, test := range []struct {
		origin, host, token string
		status              int
	}{
		{"https://ach.delino.io", "127.0.0.1:46309", token, 200},
		{"https://evil.example", "127.0.0.1:46309", token, 403},
		{"http://localhost:46308", "rebind.example:46309", token, 403},
		{"http://localhost:46308", "127.0.0.1:46309", "", 401},
	} {
		r := httptest.NewRequest("POST", "http://"+test.host+"/async_commit_hook.v1.LocalService/ListRepositories", strings.NewReader("{}"))
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Authorization", "Bearer "+test.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("%+v: %d %s", test, w.Code, w.Body.String())
		}
	}
	if err = s.Revoke(id); err != nil {
		t.Fatal(err)
	}
	if s.Authenticate("Bearer " + token) {
		t.Fatal("revoked token accepted")
	}
	var stored string
	if err = s.Store.DB.QueryRow("SELECT token_hash FROM browsers WHERE id=?", id).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("raw credential stored")
	}
}

func TestOwnedReadsRejectEscapeAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("secret"), 0600)
	if _, err := ReadOwned(root, "../secret", 1024); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err == nil {
		if _, err = ReadOwned(root, "link", 1024); err == nil {
			t.Fatal("symlink escape accepted")
		}
	}
}

func TestDeclaredEnvironmentAndRedactedReport(t *testing.T) {
	t.Setenv("ACH_TEST_SECRET", "never-record-this-value")
	t.Setenv("ACH_UNDECLARED", "inherited-value")
	s, repo := fixture(t, `version=1
[checks.test]
command='test -z "$ACH_UNDECLARED"; printf "%s" "$ACH_TEST_SECRET"; printf "<testsuite><testcase name=\"bad\"><failure>%s</failure></testcase></testsuite>" "$ACH_TEST_SECRET" > report.xml'
[[checks.test.environment]]
name="ACH_TEST_SECRET"
secret=true
required=true
[[checks.test.reports]]
kind="junit"
path="report.xml"
`)
	r := runFixture(t, s, repo)
	if r.State != Failed {
		t.Fatalf("report did not fail: %+v", r)
	}
	b := Encode(r)
	if bytes.Contains(b, []byte("never-record-this-value")) || bytes.Contains(b, []byte("inherited-value")) {
		t.Fatal("credential in metadata")
	}
	log, err := s.Logs(r.ID, "test", 0, 65536)
	if err != nil {
		t.Fatal(err)
	}
	if log.Text != "[REDACTED]" {
		t.Fatalf("unsafe log %q", log.Text)
	}
	if r.Checks[0].Failures[0].Message != "[REDACTED]" {
		t.Fatal("unsafe failure")
	}
}

func TestNoApplicableAndOptionalOnlyCancellation(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	r := runFixture(t, s, repo)
	if s.GateRun(r).Passed {
		t.Fatal("empty graph passed")
	}
	r.State = Cancelled
	r.Checks = []Check{{State: Cancelled, Optional: true}}
	if s.GateRun(r).Passed {
		t.Fatal("cancelled optional run passed")
	}
}

func TestWorktreeIdentityAndSeparateClone(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	wt := filepath.Join(t.TempDir(), "linked")
	if _, err := Git(context.Background(), repo, "worktree", "add", "--detach", wt, "HEAD"); err != nil {
		t.Fatal(err)
	}
	a, aw, err := s.Init(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	b, bw, err := s.Init(context.Background(), wt)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID || aw.ID == bw.ID {
		t.Fatal("linked worktree identity incorrect")
	}
	clone := filepath.Join(t.TempDir(), "clone")
	if _, err = Git(context.Background(), repo, "clone", "--no-local", repo, clone); err != nil {
		t.Fatal(err)
	}
	c, _, err := s.Init(context.Background(), clone)
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == a.ID {
		t.Fatal("separate clone merged")
	}
}

func TestRerunInheritedProvenanceAndActiveRetention(t *testing.T) {
	s, repo := fixture(t, `version=1
[checks.pass]
command="echo pass"
[checks.fail]
command="exit 1"
`)
	original := runFixture(t, s, repo)
	receipt, err := s.Rerun(original.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	original.CreatedAt = time.Now().Add(-72 * time.Hour)
	s.Store.SaveRun(original)
	result, err := s.Prune(false, 1, 0)
	if err != nil || len(result.RunIDs) > 0 {
		t.Fatalf("pruned active inherited evidence: %+v %v", result, err)
	}
	if err = s.RunOne(receipt.RunID); err != nil {
		t.Fatal(err)
	}
	second, err := s.Rerun(receipt.RunID, true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Store.Run(second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range r.Checks {
		if c.Name == "pass" && (c.InheritedFrom != original.ID || s.ValidateEvidence(r.ID, c) != nil) {
			t.Fatal("original provenance lost")
		}
	}
}

func TestUpdateRecoveryAndActiveRefusal(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	_, leave, err := s.Enter("viewer")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SelfUpdate(context.Background(), "1.0.0"); err == nil {
		t.Fatal("update allowed active process")
	}
	leave()
	exe := filepath.Join(t.TempDir(), "ach")
	backup := exe + ".old"
	candidate := exe + ".new"
	os.WriteFile(backup, []byte("old"), 0700)
	os.WriteFile(candidate, []byte("new"), 0700)
	j := UpdateJournal{Executable: exe, Backup: backup, Candidate: candidate, SHA256: Hash([]byte("new")), Phase: "original-backed-up"}
	if err = AtomicWrite(filepath.Join(s.Paths.Control, "update.json"), Encode(j), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecoverUpdate(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil || string(b) != "old" {
		t.Fatal("original not restored")
	}
}
