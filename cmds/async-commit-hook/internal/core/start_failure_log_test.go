package core

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnstartedChecksPublishVerifiableEmptyLogs(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo should-not-start\"\n")
		ids := []string{}
		for i := 0; i < 2; i++ {
			r, err := s.Plan(context.Background(), repo, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Store.InsertRun(&r, ""); err != nil {
				t.Fatal(err)
			}
			c := r.Checks[0]
			if cancel {
				if err = s.Store.Cancel(r.ID, Cancelled); err != nil {
					t.Fatal(err)
				}
			} else if runtime.GOOS == "windows" {
				// Exercise the real Windows missing-executable startup path.
				c.Shell = Shell(filepath.Join(t.TempDir(), "missing-shell.exe"))
			} else {
				// Fail ownership setup before launch without depending on which
				// optional shells happen to be installed on the test host.
				path := filepath.Join(s.Store.Root, "evidence", r.ID, "processes")
				if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(path, []byte("obstructed ownership directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.execute(context.Background(), r, c, repo); err != nil {
				t.Fatal(err)
			}
			r.State = Failed
			if cancel {
				r.State = Cancelled
			}
			if err = s.Store.SaveRun(r); err != nil {
				t.Fatal(err)
			}
			r, err = s.Store.Run(r.ID)
			if err != nil {
				t.Fatal(err)
			}
			c = r.Checks[0]
			if c.State != r.State || c.Log.ID == "" || c.Log.Size != 0 || c.Log.SHA256 != Hash(nil) {
				t.Fatalf("unstarted log missing integrity: %+v", c)
			}
			if err = s.ValidateEvidence(r.ID, c); err != nil {
				t.Fatalf("empty log rejected: %v", err)
			}
			if !cancel && (len(c.Diagnostics) != 1 || c.Diagnostics[0].Code != "command-start-failed") {
				t.Fatalf("startup diagnostic lost: %+v", c.Diagnostics)
			}
			ids = append(ids, r.ID)
		}
		comparison, err := s.Compare(ids[1], ids[0])
		if err != nil || !comparison.Available || (!cancel && len(comparison.Continuing) != 1) {
			t.Fatalf("empty log blocked failure comparison: %+v %v", comparison, err)
		}
	}
}

func TestLogFinalizationPreservesWriteFailureDiagnostic(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatal(err)
	}
	redactor := NewRedactor(f, nil)
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	evidence := Evidence{ID: ID()}
	diagnostic := finalizeCapturedLog(f, redactor, &evidence)
	if diagnostic == nil || diagnostic.Code != "evidence-write-failed" || evidence.SHA256 != "" {
		t.Fatalf("failed sync became valid evidence: %+v %+v", diagnostic, evidence)
	}
}
