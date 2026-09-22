package core

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	pb "github.com/delinoio/oss/protos/gen/go/async_commit_hook/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestAPIRerunPreservesAcceptedReceiptOnStartupFailure(t *testing.T) {
	for _, startupFailure := range []bool{false, true} {
		name := "started"
		if startupFailure {
			name = "startup-failed"
		}
		t.Run(name, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand='unused'\n")
			receipt, err := s.Submit(context.Background(), repo, "", false)
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Store.Run(receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			r.Checks[0].State = Failed
			if err = s.Store.SaveCheck(r.Checks[0]); err != nil {
				t.Fatal(err)
			}
			r.State = Failed
			if err = s.Store.SaveRun(r); err != nil {
				t.Fatal(err)
			}
			if startupFailure {
				// Fail before spawning a process, independently of host permissions.
				if err = os.Mkdir(filepath.Join(s.Paths.Control, "runner.log"), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				_, leave, err := s.Enter("daemon")
				if err != nil {
					t.Fatal(err)
				}
				defer leave()
			}
			request := func(id string) *httptest.ResponseRecorder {
				t.Helper()
				body, err := protojson.Marshal(&pb.RerunRequest{RunId: id, FailedOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				req := httptest.NewRequest("POST", "http://127.0.0.1:46309/async_commit_hook.v1.LocalService/Rerun", bytes.NewReader(body))
				req.Header.Set("Origin", "http://127.0.0.1:46309")
				req.Header.Set("X-Ach-Api-Version", "1")
				req.Header.Set("Content-Type", "application/json")
				response := httptest.NewRecorder()
				s.Handler().ServeHTTP(response, req)
				return response
			}
			response := request(r.ID)
			if response.Code != 200 {
				t.Fatal("accepted receipt hidden by transport error", response.Code, response.Body.String())
			}
			var result pb.RerunResponse
			if err = protojson.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if (result.StartupDiagnostic != nil) != startupFailure {
				t.Fatal(&result)
			}
			if startupFailure && (result.StartupDiagnostic.Code != "startup-failed" || result.StartupDiagnostic.Hint == "") {
				t.Fatal(&result)
			}
			accepted, err := s.Store.Run(result.RunId)
			if err != nil || accepted.ParentID != r.ID || accepted.State != Queued {
				t.Fatal(accepted, err)
			}
			var count int
			if err = s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
			// Pre-acceptance rejection remains an error and creates no receipt.
			response = request("invalid")
			if response.Code == 200 {
				t.Fatal("invalid request was accepted")
			}
			if err = s.Store.DB.QueryRow("SELECT count(*) FROM runs").Scan(&count); err != nil || count != 2 {
				t.Fatal(count, err)
			}
		})
	}
}
