// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestManualNativePublicOpenCodeRepeatedCompaction(t *testing.T) {
	nativePublicOpenCodeCompaction(t, "")
}
func TestManualNativePublicOpenCodeCompactionMissingCommand(t *testing.T) {
	nativePublicOpenCodeCompaction(t, "missing-command")
}
func nativePublicOpenCodeCompaction(t *testing.T, fault string) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native executable and private scripted provider required")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "fixture-model", "object": "model"}}})
			return
		}
		var body struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "" || json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&body) != nil || body.Model != "fixture-model" {
			t.Error("original OpenCode compaction provider scope changed")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		if n > 4 {
			t.Error("native context action replayed")
			w.WriteHeader(400)
			return
		}
		if n == 4 && (!strings.Contains(string(body.Messages), "Native compacted fixture summary") || !strings.Contains(string(body.Messages), "Input after two compactions")) {
			t.Error("fresh ordinary native process lost compacted history")
		}
		answer := "Original ordinary fixture response"
		if n == 2 || n == 3 {
			answer = "Native compacted fixture summary"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []any{
			map[string]any{"id": fmt.Sprintf("chat_fixture_%d", n), "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": answer}, "finish_reason": nil}}},
			map[string]any{"id": fmt.Sprintf("chat_fixture_%d", n), "object": "chat.completion.chunk", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}},
		} {
			raw, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	f := newFirstDispatchFixtureProfile(t, domain.OpenCode, domain.ExecuteMode, binary, provider.URL, "fixture-model")
	f.workerStream.Close()
	if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.release-setup-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.selection.MachineID, domain.ID(f.workerInstance), time.Now().Add(-2*time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: f.endpoint.URL, ServerID: f.identity.ServerID, DeviceID: f.workerDevice, MachineID: f.selection.MachineID, PairingID: domain.NewID(), Token: f.workerIdentity.Token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	stop := startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
	defer func() { stop() }()
	wait := func(id domain.ID) domain.Job {
		t.Helper()
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			r, e := f.service.Store.Get(ctx, domain.JobKind, id)
			if e != nil {
				t.Fatal(e)
			}
			j, e := store.Decode[domain.Job](r)
			if e != nil {
				t.Fatal(e)
			}
			if j.State.Terminal() || j.State == domain.JobUncertain {
				return j
			}
			select {
			case <-ctx.Done():
				t.Fatal("native compaction did not settle")
			case <-tick.C:
			}
		}
	}
	client := sessionClient(f.accountFixture)
	before := f.refresh(t)
	first, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil {
		t.Fatal(err)
	}
	if j := wait(domain.ID(first.Msg.Change.ExecutionJob.Id)); j.State != domain.JobSucceeded {
		t.Fatal("original native input", j.Problem)
	}
	originalRow := f.refresh(t)
	original, err := store.Decode[domain.Session](originalRow)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		before = f.refresh(t)
		contextReply, err := client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(before.ID)}))
		if err != nil {
			t.Fatal(err)
		}
		supported := false
		for _, c := range contextReply.Msg.Capabilities {
			supported = supported || c == pb.SessionContextCapability_SESSION_CONTEXT_CAPABILITY_OPENCODE_MANUAL_COMPACTION_V1
		}
		if !supported {
			t.Fatal("healthy original OpenCode boundary lacks its independent profile")
		}
		req := &pb.CompactSessionRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID())}
		accepted, err := client.CompactSession(ctx, ownerRequest(f.identity, req))
		if err != nil {
			t.Fatal("manual action acceptance", err)
		}
		replay, err := client.CompactSession(ctx, ownerRequest(f.identity, req))
		if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
			t.Fatal("action receipt created another native owner", err)
		}
		job := wait(domain.ID(accepted.Msg.Job.Id))
		if fault == "missing-command" && n == 1 {
			if job.State != domain.JobUncertain || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || calls.Load() != 2 {
				t.Fatal("missing original send claim gained a new native side effect", job.Problem, calls.Load())
			}
			after, _ := store.Decode[domain.Session](f.refresh(t))
			if after.Recovery != domain.NeedsRecovery || after.CompactionJobID != domain.ID(accepted.Msg.Job.Id) {
				t.Fatal("uncertain original action ownership released")
			}
			return
		}
		if job.State != domain.JobSucceeded {
			t.Fatal("native manual action", job.Problem)
		}
		var result domain.SessionCompactionResult
		if domain.Decode(job.Output, &result) != nil || result.Validate() != nil || result.OpenCode.Actions != uint32(n+1) {
			t.Fatal("incomplete original native proof")
		}
		after, err := store.Decode[domain.Session](f.refresh(t))
		if err != nil || after.Outcome != original.Outcome || !reflect.DeepEqual(after.Execution, original.Execution) || after.ExecutionSelection() != original.ExecutionSelection() || after.Dispatch != domain.DispatchReady {
			t.Fatal("compaction changed original conversation ownership", err)
		}
		stop()
		if fault == "missing-command" && n == 0 {
			if err := os.Remove(filepath.Join(f.workerRoot, "jobs", accepted.Msg.Job.Id, "opencode-compact-claim.json")); err != nil {
				t.Fatal(err)
			}
		}
		// Actual process/workspace cleanup was joined above. Expire only the
		// server's retained transport lease so this fixture can start the next
		// original Worker instance without waiting for its real 45-second TTL.
		if _, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.expire-closed-worker-lease", nil, func(tx *store.Tx) (any, error) {
			instance, _, err := tx.WorkerInstance(f.selection.MachineID)
			if err != nil {
				return nil, err
			}
			return nil, tx.SetWorkerInstance(f.selection.MachineID, instance, time.Now().Add(-2*time.Minute))
		}); err != nil {
			t.Fatal(err)
		}
		stop = startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
	}
	nextRaw, _ := json.Marshal(domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Input after two compactions"})
	next, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: string(originalRow.ID), DocumentJson: nextRaw}))
	if err != nil {
		t.Fatal(err)
	}
	var nextJob domain.Job
	var nextInput domain.ExecutionJobInput
	if next.Msg.Change.ExecutionJob == nil || domain.Decode(next.Msg.Change.ExecutionJob.DocumentJson, &nextJob) != nil || domain.Decode(nextJob.Input, &nextInput) != nil || nextInput.InputID != domain.ID(next.Msg.Change.Input.Id) {
		row := f.refresh(t)
		resumed, e := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(resourceForTest(row), domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_RESUME}))
		if e != nil {
			t.Fatal(e)
		}
		next.Msg.Change.ExecutionJob = resumed.Msg.Change.ExecutionJob
	}
	if job := wait(domain.ID(next.Msg.Change.ExecutionJob.Id)); job.State != domain.JobSucceeded {
		t.Fatal("ordinary successor through full compaction lineage", job.Problem)
	}
	if calls.Load() != 4 {
		t.Fatal("native side effect count changed", calls.Load())
	}
	// Native step-finish records include each genuine summary once. They remain
	// separate from unavailable exact Responses-provider telemetry.
	summary, err := delidevv1connect.NewUsageServiceClient(http.DefaultClient, f.endpoint.URL).GetUsageSummary(ctx, ownerRequest(f.identity, &pb.GetUsageSummaryRequest{SessionId: string(originalRow.ID), AccountingProfile: pb.UsageAccountingProfile_USAGE_ACCOUNTING_PROFILE_NATIVE_UNITS_V1}))
	if err != nil {
		t.Fatal(err)
	}
	var units uint32
	for _, item := range summary.Msg.NativeAccounting {
		if item.Totals.Kind == pb.AccountingUnitKind_ACCOUNTING_UNIT_KIND_OPENCODE_STEP {
			units += item.Totals.Units
		}
	}
	if units != 4 || summary.Msg.Totals.Responses != 0 || summary.Msg.AcceptedCompactionsWithoutResponse != 2 {
		t.Fatal("original summary steps lost, doubled or fabricated exact response coverage", units, summary.Msg.AcceptedCompactionsWithoutResponse)
	}
	t.Log("actual pinned OpenCode -> original server relay -> durable Worker: two once-only manual actions across Worker restarts, native step accounting, and successor full native history/cleanup; scripted provider on current platform only")
}
