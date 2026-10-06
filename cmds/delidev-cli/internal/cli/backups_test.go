package cli

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
)

func TestBackupRestoreCLIConfirmsOriginalInspectionAndObservesRestart(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	start := func() (context.CancelFunc, chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		ready, done := make(chan struct{}), make(chan error, 1)
		go func() {
			done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
		}()
		select {
		case <-ready:
		case err := <-done:
			cancel()
			t.Fatal(err)
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatal("startup timeout")
		}
		return cancel, done
	}
	cancel, done := start()
	joined := false
	defer func() {
		cancel()
		if !joined {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	code, value := cliRun(t, root, []string{"backup", "create", "--wait"}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	id := value["result"].(map[string]any)["job"].(map[string]any)["backup_id"].(string)
	code, value = cliRun(t, root, []string{"backup", "inspect", "--id", id}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	inspection := value["result"].(map[string]any)
	metadata := inspection["backup"].(map[string]any)
	requestID := string(domain.NewID())
	args := []string{"--request-id", requestID, "backup", "restore", "--id", id, "--expected-revision", metadata["revision"].(string), "--size-bytes", metadata["size_bytes"].(string), "--modified-at", metadata["modified_at"].(string), "--sha256", inspection["sha256"].(string), "--expected-restore-revision", inspection["restore_revision"].(string)}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("unconfirmed restore accepted")
	}
	code, value = cliRun(t, root, append(args, "--confirm"), "")
	if code != 0 || value["result"].(map[string]any)["receipt"].(map[string]any)["state"] != "BACKUP_RESTORE_STATE_PUBLISHED" {
		t.Fatal(code, value)
	}
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("restore did not join original server")
	}
	cancel()
	cancel, done = start()
	joined = false
	code, value = cliRun(t, root, []string{"backup", "restore-status", "--id", requestID}, "")
	if code != 0 || value["result"].(map[string]any)["receipt"].(map[string]any)["state"] != "BACKUP_RESTORE_STATE_RESTORED" {
		t.Fatal(code, value)
	}
	code, value = cliRun(t, root, append(args, "--confirm"), "")
	if code != 0 || value["result"].(map[string]any)["replayed"] != true {
		t.Fatal("exact retry republished or failed", code, value)
	}
}

func TestBackupCLIUsesServerInventoryAndChecksOriginalImage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case err := <-done:
		done <- err // Preserve the joined result for deferred cleanup.
		t.Fatal(err)
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timed out")
	}
	code, value := cliRun(t, root, []string{"backup", "create", "--wait"}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	creation := value["result"].(map[string]any)["job"].(map[string]any)
	id := creation["backup_id"].(string)
	if creation["state"] != "BACKUP_CREATION_STATE_SUCCEEDED" {
		t.Fatal(value)
	}
	code, value = cliRun(t, root, []string{"backup", "creation", "--id", creation["id"].(string)}, "")
	if code != 0 || value["result"].(map[string]any)["job"].(map[string]any)["backup_id"] != id {
		t.Fatal(code, value)
	}
	code, value = cliRun(t, root, []string{"backup", "creations", "--limit", "1"}, "")
	if code != 0 || len(value["result"].(map[string]any)["jobs"].([]any)) != 1 {
		t.Fatal(code, value)
	}
	code, value = cliRun(t, root, []string{"backup", "list", "--limit", "1"}, "")
	if code != 0 {
		t.Fatal(code, value)
	}
	item := value["result"].(map[string]any)["backups"].([]any)[0].(map[string]any)
	if item["id"] != id {
		t.Fatal(value)
	}
	if _, ok := item["size_bytes"].(string); !ok {
		t.Fatal("bytes lost decimal-string encoding", item)
	}
	code, value = cliRun(t, root, []string{"backup", "inspect", "--id", id}, "")
	if code != 0 || len(value["result"].(map[string]any)["sha256"].(string)) != 64 {
		t.Fatal(code, value)
	}
	inspection := value["result"].(map[string]any)
	metadata := inspection["backup"].(map[string]any)
	args := []string{"backup", "delete", "--id", id, "--expected-revision", metadata["revision"].(string), "--size-bytes", metadata["size_bytes"].(string), "--modified-at", metadata["modified_at"].(string), "--sha256", inspection["sha256"].(string)}
	if code, _ := cliRun(t, root, args, ""); code == 0 {
		t.Fatal("unconfirmed permanent deletion accepted")
	}
	code, value = cliRun(t, root, append(args, "--confirm"), "")
	if code != 0 {
		t.Fatal(code, value)
	}
	job := value["result"].(map[string]any)["job"].(map[string]any)["id"]
	deadline := time.Now().Add(10 * time.Second)
	for {
		code, value = cliRun(t, root, []string{"backup", "deletions"}, "")
		if code != 0 {
			t.Fatal(code, value)
		}
		jobs := value["result"].(map[string]any)["jobs"].([]any)
		if len(jobs) != 1 {
			t.Fatal(value)
		}
		code, value = cliRun(t, root, []string{"backup", "deletion", "--id", job.(string)}, "")
		if code != 0 {
			t.Fatal(code, value)
		}
		state := value["result"].(map[string]any)["job"].(map[string]any)
		if state["id"] != job {
			t.Fatal(value)
		}
		if state["state"] == "BACKUP_DELETION_STATE_SUCCEEDED" {
			if _, err := strconv.ParseUint(state["image_bytes"].(string), 10, 64); err != nil {
				t.Fatal(err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deletion did not complete", value)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestBackupCLIWaitReturnsTerminalFailureWithAcceptedJob(t *testing.T) {
	for _, problem := range []domain.Code{domain.PermissionDenied, domain.Unauthenticated, domain.RecoveryRequired, domain.ResourceExhausted} {
		for _, replay := range []bool{false, true} {
			t.Run(string(problem)+strconv.FormatBool(replay), func(t *testing.T) {
				requestID, jobID, backupID := string(domain.NewID()), string(domain.NewID()), string(domain.NewID())
				var accepts, reads atomic.Int32
				mux := http.NewServeMux()
				failed := func() *pb.BackupCreationJob {
					return &pb.BackupCreationJob{Id: jobID, BackupId: backupID, Revision: 2, State: pb.BackupCreationState_BACKUP_CREATION_STATE_FAILED, ProblemCode: string(problem)}
				}
				mux.Handle(delidevv1connect.SystemServiceRequestBackupProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceRequestBackupProcedure, func(_ context.Context, req *connect.Request[pb.RequestBackupRequest]) (*connect.Response[pb.RequestBackupResponse], error) {
					accepts.Add(1)
					if req.Msg.RequestId != requestID {
						t.Error("lost original request")
					}
					job := failed()
					if !replay {
						job.State, job.Revision, job.ProblemCode = pb.BackupCreationState_BACKUP_CREATION_STATE_PENDING, 1, ""
					}
					return connect.NewResponse(&pb.RequestBackupResponse{Job: job, RequestId: requestID, Replayed: replay}), nil
				}))
				mux.Handle(delidevv1connect.SystemServiceGetBackupCreationProcedure, connect.NewUnaryHandler(delidevv1connect.SystemServiceGetBackupCreationProcedure, func(_ context.Context, req *connect.Request[pb.GetBackupCreationRequest]) (*connect.Response[pb.GetBackupCreationResponse], error) {
					reads.Add(1)
					if req.Msg.Id != jobID {
						t.Error("polled a different job")
					}
					return connect.NewResponse(&pb.GetBackupCreationResponse{Job: failed()}), nil
				}))
				peer := httptest.NewServer(mux)
				defer peer.Close()
				var out bytes.Buffer
				code := Run(context.Background(), []string{"--data-dir", filepath.Join(t.TempDir(), "client"), "--server", peer.URL, "--token-stdin", "--request-id", requestID, "backup", "create", "--wait"}, IO{In: strings.NewReader("private-fixture-token"), Out: &out, Err: io.Discard})
				var response struct {
					Result struct {
						Raw       json.RawMessage `json:"job"`
						RequestID string          `json:"request_id"`
						Replayed  bool            `json:"replayed"`
					} `json:"result"`
					Error *domain.Error `json:"error"`
				}
				if err := json.Unmarshal(out.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				var job map[string]any
				if err := json.Unmarshal(response.Result.Raw, &job); err != nil {
					t.Fatal(err)
				}
				if code != (&domain.Error{Code: problem}).ExitCode() || response.Error == nil || response.Error.Code != problem || job["id"] != jobID || job["backup_id"] != backupID || job["revision"] != "2" || job["state"] != "BACKUP_CREATION_STATE_FAILED" || job["problem_code"] != string(problem) || response.Result.RequestID != requestID || response.Result.Replayed != replay || accepts.Load() != 1 || (reads.Load() == 0) != replay {
					t.Fatal(code, out.String(), accepts.Load(), reads.Load())
				}
			})
		}
	}
}
