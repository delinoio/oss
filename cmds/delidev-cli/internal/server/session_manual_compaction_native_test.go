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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// The pinned native executable talks only to a scripted loopback provider under
// disposable server/Worker runtimes. No user configuration or key is consulted.
func TestManualNativePublicSessionCompaction(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary required")
	}
	// Match the production discovery contract even when the fixture caller
	// selects a temporary binary through macOS's /tmp directory alias.
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.Abs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"success", "provider-rejection", "insufficient-history", "cancellation"} {
		t.Run(scenario, func(t *testing.T) { nativePublicManualCompaction(t, binary, scenario) })
	}
}

func nativePublicManualCompaction(t *testing.T, binary, scenario string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var calls atomic.Int32
	summarizing := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "claude-sonnet-4-6", "object": "model"}}})
			return
		}
		var req struct {
			Model    string          `json:"model"`
			Stream   bool            `json:"stream"`
			Messages json.RawMessage `json:"messages"`
		}
		if r.Method != "POST" || r.URL.Path != "/messages" || json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req) != nil || req.Model != "claude-sonnet-4-6" {
			t.Error("fixture provider scope changed")
			w.WriteHeader(400)
			return
		}
		n := calls.Add(1)
		if n > 4 {
			t.Error("unexpected native/provider replay")
			w.WriteHeader(400)
			return
		}
		if n == 3 {
			close(summarizing)
			if scenario == "cancellation" {
				<-r.Context().Done()
				return
			}
			if scenario == "provider-rejection" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(400)
				io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Private fixture compaction rejection"}}`)
				return
			}
		}
		if n == 4 {
			retained := strings.Contains(string(req.Messages), strings.Repeat("fixture ", 100))
			if retained != (scenario != "success") || !strings.Contains(string(req.Messages), "Input after manual action") {
				t.Error("post-action history lost its exact native branch")
			}
		}
		answer := "Original public Claude result."
		if n == 1 && scenario != "insufficient-history" {
			answer = strings.Repeat("Private fixture conversation response. ", 4000)
		}
		if n == 3 {
			answer = "<summary>Original conversation retained by manual native compaction.</summary>"
		}
		message := map[string]any{"id": fmt.Sprintf("msg_manual_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": req.Model, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 5, "output_tokens": 0}}
		if !req.Stream {
			message["content"] = []any{map[string]any{"type": "text", "text": answer}}
			message["stop_reason"] = "end_turn"
			message["usage"].(map[string]any)["output_tokens"] = 8
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(message)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, e := range []map[string]any{
			{"type": "message_start", "message": message},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": answer}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 8}},
			{"type": "message_stop"},
		} {
			raw, _ := json.Marshal(e)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e["type"], raw)
		}
	}))
	defer provider.Close()
	f := newFirstDispatchFixtureProfile(t, domain.ClaudeCode, domain.ExecuteMode, binary, provider.URL, "claude-sonnet-4-6")
	client := sessionClient(f.accountFixture)
	if scenario != "insufficient-history" {
		_, err := client.EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(f.change.Input, domain.NewID()), SessionId: f.change.Session.Id, Prompt: "First manual fixture " + strings.Repeat("fixture ", 30000)}))
		if err != nil {
			t.Fatal(err)
		}
	}
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.release-setup-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.selection.MachineID, domain.ID(f.workerInstance), time.Now().Add(-2*time.Minute))
	})
	if err != nil {
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
			row, e := f.service.Store.Get(ctx, domain.JobKind, id)
			if e != nil {
				t.Fatal(e)
			}
			j, e := store.Decode[domain.Job](row)
			if e != nil {
				t.Fatal(e)
			}
			if j.State.Terminal() || j.State == domain.JobUncertain {
				return j
			}
			select {
			case <-ctx.Done():
				t.Fatal("native action did not settle", j.State)
			case <-tick.C:
			}
		}
	}
	resume := func() *pb.Resource {
		t.Helper()
		r := f.refresh(t)
		reply, e := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
		if e != nil {
			t.Fatal(e)
		}
		return reply.Msg.Change.ExecutionJob
	}
	first := resume()
	j := wait(domain.ID(first.Id))
	if j.State != domain.JobSucceeded {
		t.Fatal("first native input", j.Problem)
	}
	enqueue := func(prompt string) {
		t.Helper()
		body, _ := json.Marshal(domain.SessionInput{Prompt: prompt, Mode: domain.ExecuteMode})
		_, e := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: f.change.Session.Id, DocumentJson: body}))
		if e != nil {
			t.Fatal(e)
		}
	}
	// Pause before adding the second turn, so no automatic dispatcher can win
	// the explicit compaction acceptance transaction below.
	pause := func() {
		t.Helper()
		r := f.refresh(t)
		_, e := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: pb.SessionAction_SESSION_ACTION_STOP}))
		if e != nil {
			t.Fatal(e)
		}
	}
	pause()
	if scenario != "insufficient-history" {
		enqueue("Second original fixture input")
		second := resume()
		j = wait(domain.ID(second.Id))
		if j.State != domain.JobSucceeded {
			t.Fatal("second native input", j.Problem)
		}
		pause()
	}
	before := f.refresh(t)
	prior, e := store.Decode[domain.Session](before)
	if e != nil {
		t.Fatal(e)
	}
	req := &pb.CompactSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(before.ID), ExpectedRevision: before.Revision}}
	accepted, e := client.CompactSession(ctx, ownerRequest(f.identity, req))
	if e != nil {
		t.Fatal("compaction acceptance", e)
	}
	replay, e := client.CompactSession(ctx, ownerRequest(f.identity, req))
	if e != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Msg.Job.Id {
		t.Fatal("acceptance replay queued another action", e)
	}
	enqueue("Input after manual action")
	if scenario == "cancellation" {
		select {
		case <-summarizing:
		case <-ctx.Done():
			t.Fatal("missing original compact request")
		}
		pause()
	}
	job := wait(domain.ID(accepted.Msg.Job.Id))
	after := f.refresh(t)
	state, e := store.Decode[domain.Session](after)
	if e != nil {
		t.Fatal(e)
	}
	if state.Outcome != prior.Outcome || state.ExecutionSelection() != prior.ExecutionSelection() || state.PendingInputs != 1 || state.Dispatch != domain.DispatchPaused {
		t.Fatal("manual action overwrote conversation/FIFO state")
	}
	if scenario == "cancellation" {
		if job.State != domain.JobUncertain || state.Recovery != domain.NeedsRecovery {
			t.Fatal("uncertain canceled action regained dispatch")
		}
		stop()
		stop = startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
		contextReply, e := client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(before.ID)}))
		if e != nil || len(contextReply.Msg.Capabilities) != 1 || calls.Load() != 3 {
			t.Fatal("restart resent canceled compact command", e, calls.Load())
		}
		return
	}
	var result domain.SessionCompactionResult
	if domain.Decode(job.Output, &result) != nil || result.Validate() != nil || result.Checkpoint.JobID != domain.ID(accepted.Msg.Job.Id) {
		t.Fatal("native action lost retained result", job.Problem, string(job.Output))
	}
	failed := scenario != "success"
	if failed && (job.State != domain.JobFailed || result.Outcome != domain.CompactionFailed || !result.Checkpoint.RequiresResume) || !failed && (job.State != domain.JobSucceeded || result.Outcome != domain.CompactionSucceeded || result.Boundary == nil || result.Summary == nil) {
		t.Fatal("outer success replaced independent compact_result", result.Outcome, job.Problem)
	}
	contextReply, e := client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(before.ID)}))
	if e != nil {
		t.Fatal(e)
	}
	var view sessionContextView
	if json.Unmarshal(contextReply.Msg.DocumentJson, &view) != nil || view.CurrentTokens != nil || view.ManualAction == nil {
		t.Fatal("context view fabricated utilization or omitted action")
	}
	// A fresh Worker restores the action checkpoint. It must not repeat /compact.
	priorCalls := calls.Load()
	stop()
	stop = startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
	next := resume()
	nextJob := wait(domain.ID(next.Id))
	if nextJob.State != domain.JobSucceeded || calls.Load() != priorCalls+1 {
		t.Fatal("retained manual action could not continue without replay", nextJob.Problem, calls.Load(), priorCalls)
	}
	retained, e := f.service.Store.Get(ctx, domain.JobKind, domain.ID(accepted.Msg.Job.Id))
	if e != nil || string(retained.Data) != string(mustNativeCompactionJobJSON(t, job)) {
		t.Fatal("continuation rewrote original compaction evidence", e)
	}
}

func mustNativeCompactionJobJSON(t *testing.T, j domain.Job) []byte {
	t.Helper()
	raw, e := json.Marshal(j)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
