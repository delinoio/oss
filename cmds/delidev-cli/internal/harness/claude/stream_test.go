package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func init() {
	if len(os.Args) != 3 || os.Args[1] != "--delidev-claude-stream-fixture" {
		return
	}
	mode := os.Args[2]
	if mode == "blocked-input" {
		for {
			time.Sleep(time.Second)
		}
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 4096), maxStreamFrame+1)
	emit := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	for scanner.Scan() {
		var message struct {
			Type            string                     `json:"type"`
			RequestID       domain.ID                  `json:"request_id"`
			Request         map[string]json.RawMessage `json:"request"`
			Response        json.RawMessage            `json:"response"`
			UUID            domain.ID                  `json:"uuid"`
			SessionID       domain.ID                  `json:"session_id"`
			ParentToolUseID *string                    `json:"parent_tool_use_id"`
			Message         struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		}
		if domain.Decode(scanner.Bytes(), &message) != nil {
			os.Exit(50)
		}
		switch message.Type {
		case "control_request":
			var subtype string
			if json.Unmarshal(message.Request["subtype"], &subtype) != nil || message.RequestID.Validate() != nil {
				os.Exit(51)
			}
			if subtype == "late" {
				time.Sleep(100 * time.Millisecond)
			}
			response := map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": message.RequestID, "response": map[string]any{"ready": true}}}
			if subtype == "error" {
				response["response"] = map[string]any{"subtype": "error", "request_id": message.RequestID, "error": "private-error-sentinel"}
			}
			switch mode {
			case "malformed":
				_, _ = os.Stdout.WriteString("not-json\n")
				continue
			case "duplicate-key":
				_, _ = os.Stdout.WriteString("{\"type\":\"system\",\"type\":\"assistant\"}\n")
				continue
			case "invalid-utf8":
				_, _ = os.Stdout.Write(append([]byte("{\"type\":\""), 0xff, '"', '}', '\n'))
				continue
			case "ambiguous-response":
				response["response"].(map[string]any)["error"] = "private-error-sentinel"
			case "array-response":
				response["response"].(map[string]any)["response"] = []string{"private-array-sentinel"}
			case "duplicate-response":
				emit(response)
			case "unknown-cancel":
				emit(map[string]any{"type": "control_cancel_request", "request_id": "unseen"})
				continue
			case "identity-collision":
				emit(map[string]any{"type": "control_request", "request_id": message.RequestID, "request": map[string]any{"subtype": "can_use_tool"}})
				continue
			case "oversize":
				_, _ = os.Stdout.Write(bytes.Repeat([]byte{'x'}, maxStreamFrame+1))
				continue
			case "foreign-response":
				response["response"].(map[string]any)["request_id"] = domain.NewID()
			case "partial-exit":
				_, _ = os.Stdout.WriteString("{\"type\":")
				os.Exit(0)
			case "flood":
				for range maxStreamEvents + 1 {
					emit(map[string]any{"type": "system", "private": "private-event-sentinel"})
				}
				continue
			}
			if mode == "interaction" || mode == "canceled" || mode == "duplicate-callback" || mode == "duplicate-cancel" || strings.HasSuffix(mode, "echo") {
				r := map[string]any{"type": "control_request", "request_id": "callback-1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{"command": "private-command-sentinel"}}}
				emit(r)
				if mode == "unclaimed-echo" {
					emit(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": "callback-1", "response": map[string]any{"behavior": "deny"}}})
				}
				if mode == "duplicate-callback" {
					emit(r)
				}
				if mode == "canceled" || mode == "duplicate-cancel" {
					emit(map[string]any{"type": "control_cancel_request", "request_id": "callback-1"})
					if mode == "duplicate-cancel" {
						emit(map[string]any{"type": "control_cancel_request", "request_id": "callback-1"})
					}
				}
			}
			emit(response)
		case "control_response":
			var r struct {
				Subtype   string `json:"subtype"`
				RequestID string `json:"request_id"`
				Response  struct {
					Behavior string `json:"behavior"`
				} `json:"response"`
			}
			if domain.Decode(message.Response, &r) != nil || r.RequestID != "callback-1" || r.Subtype != "success" || r.Response.Behavior != "deny" {
				os.Exit(52)
			}
			if strings.HasSuffix(mode, "echo") {
				echo := map[string]any{"type": "control_response", "response": message.Response}
				if mode == "mismatched-echo" {
					echo["response"] = map[string]any{"subtype": "success", "request_id": r.RequestID, "response": map[string]any{"behavior": "allow"}}
				}
				emit(echo)
				if mode == "duplicate-echo" {
					emit(echo)
				}
			}
			emit(map[string]any{"type": "control_cancel_request", "request_id": r.RequestID})
			emit(map[string]any{"type": "system", "subtype": "response_received"})
		case "user":
			if message.UUID.Validate() != nil || message.SessionID.Validate() != nil || message.ParentToolUseID != nil || message.Message.Role != "user" {
				os.Exit(53)
			}
			raw, _ := json.Marshal(map[string]any{"type": "user", "uuid": message.UUID, "session_id": message.SessionID, "isReplay": true, "message": message.Message})
			for _, b := range append(raw, '\n') {
				_, _ = os.Stdout.Write([]byte{b})
			}
		default:
			os.Exit(54)
		}
		_, _ = os.Stderr.WriteString("private-stderr-sentinel")
	}
	os.Exit(0)
}

func streamFixture(t *testing.T, mode string) (*Stream, process.Config, *bytes.Buffer) {
	t.Helper()
	cfg, logs := fixtureConfig(t, "stream")
	cfg.Process.Args = []string{"--delidev-claude-stream-fixture", mode}
	ctx, cancel := context.WithCancel(context.Background())
	s, err := StartStream(ctx, cfg.Process)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
			t.Error(err)
		}
		if strings.Contains(logs.String(), "sentinel") {
			t.Error("raw native data entered logs")
		}
	})
	return s, cfg.Process, logs
}

func TestStreamCorrelatesControlsAndNativeInputWithoutReplay(t *testing.T) {
	s, _, _ := streamFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	id := domain.NewID()
	r, err := s.Call(ctx, id, map[string]any{"subtype": "initialize", "hooks": nil})
	if err != nil || r.Failed || string(r.Result) != `{"ready":true}` {
		t.Fatalf("initialization: %+v %v", r, err)
	}
	if _, err := s.Call(ctx, id, map[string]any{"subtype": "initialize"}); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("control identity reused: %v", err)
	}
	input, session := domain.NewID(), domain.NewID()
	if err := s.SendInput(ctx, input, session, "Private Unicode input: \u03bb\U0001f600"); err != nil {
		t.Fatal(err)
	}
	event, err := s.Next(ctx)
	if err != nil || event.Kind != NativeMessage || event.Type != "user" {
		t.Fatalf("native user replay: %+v %v", event, err)
	}
	var message struct {
		Type    string    `json:"type"`
		UUID    domain.ID `json:"uuid"`
		Session domain.ID `json:"session_id"`
		Replay  bool      `json:"isReplay"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	if domain.Decode(event.Body, &message) != nil || message.UUID != input || message.Session != session || !message.Replay || message.Message.Content != "Private Unicode input: \u03bb\U0001f600" {
		t.Fatal("native identity/content changed")
	}
	if err := s.SendInput(ctx, input, session, "duplicate"); domain.SafeError(err).Code != domain.Conflict {
		t.Fatalf("input replayed: %v", err)
	}
	r, err = s.Call(ctx, domain.NewID(), map[string]any{"subtype": "error"})
	if err != nil || !r.Failed || len(r.Result) != 0 {
		t.Fatal("native error body was exposed")
	}
	for _, value := range []any{r, event} {
		raw, _ := json.Marshal(value)
		if string(raw) != "{}" {
			t.Fatal("private stream data became serializable")
		}
	}
}

func TestStreamPreservesLateAcknowledgmentsAndCanceledReads(t *testing.T) {
	s, _, _ := streamFixture(t, "normal")
	id := domain.NewID()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	_, err := s.Call(ctx, id, map[string]any{"subtype": "late"})
	cancel()
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("late response became rejection: %v", err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	canceled, stop := context.WithCancel(ctx)
	stop()
	if _, err := s.Next(canceled); err == nil {
		t.Fatal("canceled reader consumed native event")
	}
	event, err := s.Next(ctx)
	if err != nil || event.Kind != NativeLateResponse || event.RequestID != string(id) || event.Response.Failed || string(event.Response.Result) != `{"ready":true}` {
		t.Fatalf("lost late acknowledgment: %+v %v", event, err)
	}
	if _, err := s.Call(ctx, id, map[string]any{"subtype": "initialize"}); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("timed-out identity could be replayed")
	}
}

func TestStreamClaimsOneOriginalCallbackAndRetainsCancellation(t *testing.T) {
	s, _, _ := streamFixture(t, "interaction")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.Call(ctx, domain.NewID(), map[string]any{"subtype": "initialize"}); err != nil {
		t.Fatal(err)
	}
	event, err := s.Next(ctx)
	if err != nil || event.Kind != NativeRequest {
		t.Fatal("missing native callback", err)
	}
	foreign := event
	foreign.ArrivalID = domain.NewID()
	if err := s.Reply(ctx, foreign, map[string]any{"behavior": "deny"}); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("foreign arrival was answered")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- s.Reply(ctx, event, map[string]any{"behavior": "deny"}) })
	}
	wg.Wait()
	close(results)
	sent, conflict := 0, 0
	for err := range results {
		if err == nil {
			sent++
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if sent != 1 || conflict != 1 {
		t.Fatal("callback response was not claimed exactly once")
	}
	closed, err := s.Next(ctx)
	if err != nil || closed.Kind != NativeCancellation || closed.RequestID != event.RequestID || closed.ArrivalID != event.ArrivalID {
		t.Fatal("cancellation lost original arrival")
	}
	ack, err := s.Next(ctx)
	if err != nil || ack.Type != "system" {
		t.Fatal("missing fixture receipt", err)
	}
	if err := s.Reply(ctx, event, map[string]any{"behavior": "deny"}); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("closed callback could be answered again")
	}
}

func TestStreamCancellationBeforeReplyCannotAuthorizeSend(t *testing.T) {
	s, _, _ := streamFixture(t, "canceled")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.Call(ctx, domain.NewID(), map[string]any{"subtype": "initialize"}); err != nil {
		t.Fatal(err)
	}
	event, err := s.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := s.Next(ctx)
	if err != nil || closed.ArrivalID != event.ArrivalID {
		t.Fatal("lost cancellation")
	}
	if err := s.Reply(ctx, event, map[string]any{"behavior": "deny"}); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("canceled request was answered")
	}
}

func TestStreamRejectsProtocolFailuresAndStopsOwnedScope(t *testing.T) {
	for _, test := range []struct {
		mode string
		code domain.Code
	}{{"malformed", domain.Unsupported}, {"duplicate-key", domain.Unsupported}, {"invalid-utf8", domain.Unsupported}, {"ambiguous-response", domain.Unsupported}, {"array-response", domain.Unsupported}, {"duplicate-response", domain.Unsupported}, {"unknown-cancel", domain.Unsupported}, {"duplicate-cancel", domain.Unsupported}, {"identity-collision", domain.Unsupported}, {"unclaimed-echo", domain.Unsupported}, {"oversize", domain.ResourceExhausted}, {"foreign-response", domain.Unsupported}, {"partial-exit", domain.Unsupported}, {"flood", domain.ResourceExhausted}, {"duplicate-callback", domain.Unsupported}} {
		t.Run(test.mode, func(t *testing.T) {
			s, _, _ := streamFixture(t, test.mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = s.Call(ctx, domain.NewID(), map[string]any{"subtype": "initialize"})
			select {
			case <-s.Done():
			case <-ctx.Done():
				t.Fatal("incompatible native scope did not stop")
			}
			if err := s.Err(); err == nil || err.Code != test.code {
				t.Fatalf("protocol failure: %v", err)
			}
			if _, err := s.Next(ctx); err == nil {
				t.Fatal("failed connection exposed retained events")
			}
		})
	}
}

func TestStreamMatchesOnlyOneExactClaimedReplyEcho(t *testing.T) {
	for _, mode := range []string{"reply-echo", "mismatched-echo", "duplicate-echo"} {
		t.Run(mode, func(t *testing.T) {
			s, _, _ := streamFixture(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := s.Call(ctx, domain.NewID(), map[string]any{"subtype": "initialize"}); err != nil {
				t.Fatal(err)
			}
			event, err := s.Next(ctx)
			if err != nil || event.Kind != NativeRequest {
				t.Fatal("missing callback", err)
			}
			if err := s.Reply(ctx, event, map[string]any{"behavior": "deny"}); err != nil {
				t.Fatal(err)
			}
			if mode != "reply-echo" {
				select {
				case <-s.Done():
				case <-ctx.Done():
					t.Fatal("invalid reply echo did not terminate stream")
				}
				if err := s.Err(); err == nil || err.Code != domain.Unsupported {
					t.Fatal("invalid echo lost its failure classification", err)
				}
				return
			}
			echo, err := s.Next(ctx)
			if err != nil || echo.Kind != NativeReplyEcho || echo.ArrivalID != event.ArrivalID || echo.RequestID != event.RequestID || echo.Response.Failed || string(echo.Response.Result) != `{"behavior":"deny"}` {
				t.Fatal("reply echo changed its original claim", err)
			}
			closed, err := s.Next(ctx)
			if err != nil || closed.Kind != NativeCancellation || closed.ArrivalID != event.ArrivalID {
				t.Fatal("reply echo consumed cancellation evidence", err)
			}
		})
	}
}

func TestStreamReplyDigestPreservesExactValues(t *testing.T) {
	a, err := streamReplyDigest([]byte(`{"behavior":"allow","updatedInput":{"b":9007199254740993,"a":[1,"x"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := streamReplyDigest([]byte(`{ "updatedInput": { "a": [1,"x"], "b":9007199254740993 }, "behavior":"allow" }`))
	if err != nil || a != b {
		t.Fatal("object ordering changed reply identity")
	}
	for _, raw := range []string{`{"behavior":"allow","updatedInput":{"b":9007199254740992,"a":[1,"x"]}}`, `{"behavior":"allow","updatedInput":{"b":9007199254740993,"a":["x",1]}}`, `{"behavior":"deny","updatedInput":{"b":9007199254740993,"a":[1,"x"]}}`} {
		digest, err := streamReplyDigest([]byte(raw))
		if err != nil || digest == a {
			t.Fatal("changed reply borrowed original identity")
		}
	}
}

func TestStreamCanceledBeforeSendPreservesIdentity(t *testing.T) {
	s, _, _ := streamFixture(t, "normal")
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	id := domain.NewID()
	if _, err := s.Call(canceled, id, map[string]any{"subtype": "initialize"}); err == nil {
		t.Fatal("pre-canceled control sent")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if _, err := s.Call(ctx, id, map[string]any{"subtype": "initialize"}); err != nil {
		t.Fatal("pre-canceled operation consumed identity", err)
	}
	id = domain.NewID()
	if err := s.SendInput(canceled, id, domain.NewID(), "private"); err == nil {
		t.Fatal("pre-canceled input sent")
	}
	if err := s.SendInput(ctx, id, domain.NewID(), "private"); err != nil {
		t.Fatal("pre-canceled input consumed identity", err)
	}
}

func TestStreamBoundsBlockedInputAndJoinsWriter(t *testing.T) {
	s, _, _ := streamFixture(t, "blocked-input")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := s.Call(ctx, domain.NewID(), map[string]any{"subtype": "initialize", "data": strings.Repeat("x", 512<<10)})
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("blocked write did not retain uncertainty: %v", err)
	}
	select {
	case <-s.Done():
	default:
		t.Fatal("blocked writer outlived owned cleanup")
	}
}

func TestStreamEnforcesAggregateBoundsAndIdentityCapacity(t *testing.T) {
	s := &Stream{notify: make(chan struct{}, 1), seen: map[domain.ID]bool{}}
	if err := s.queueLocked(StreamEvent{size: maxStreamBytes}); err != nil {
		t.Fatal(err)
	}
	if err := s.queueLocked(StreamEvent{size: 1}); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("byte bound was ignored")
	}
	for range maxStreamIdentities {
		if err := s.claimLocked(domain.NewID()); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.claimLocked(domain.NewID()); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("identity bound was ignored")
	}
}
