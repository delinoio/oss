// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/delinoio/oss/packages/tairouter/store"
)

type socketPendingRelease struct {
	lease   store.Lease
	cleanup func(context.Context)
}

type socketReleaseCapture struct{ pending chan socketPendingRelease }

func (q socketReleaseCapture) Submit(l store.Lease, cleanup func(context.Context)) {
	q.pending <- socketPendingRelease{l, cleanup}
}

func TestProviderAcceptanceSequentialResponsesSocketTurns(t *testing.T) {
	testProviderAcceptanceSequentialResponsesSocketTurns(t, "azure-openai", "deployment")
}

func TestMistralSequentialResponsesSocketTurns(t *testing.T) {
	testProviderAcceptanceSequentialResponsesSocketTurns(t, "mistral", "codestral-latest")
}

func testProviderAcceptanceSequentialResponsesSocketTurns(t *testing.T, provider, model string) {
	var sends atomic.Int32
	secondSend := make(chan struct{})
	continueSecond := make(chan struct{})
	defer func() {
		select {
		case <-continueSecond:
		default:
			close(continueSecond)
		}
	}()
	f := newProviderAcceptance(t, provider, model, func(w http.ResponseWriter, r *http.Request) {
		if sends.Add(1) == 2 && provider == "mistral" {
			close(secondSend)
			<-continueSecond
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if provider == "mistral" {
			_, _ = io.WriteString(w, "data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"fixture ok\"},\"finish_reason\":null}]}\n\n"+
				"data: {\"id\":\"chat_fixture\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture ok\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":"+providerAcceptanceResponses+"}\n\n")
	})
	var releases chan socketPendingRelease
	if provider == "mistral" {
		releases = make(chan socketPendingRelease, 2)
		f.srv.releases = socketReleaseCapture{releases}
	}
	srv := httptest.NewServer(f.srv.handler())
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	headers := http.Header{"Authorization": []string{"Bearer " + f.key}}
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/v1/responses", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		if response != nil {
			t.Fatalf("socket dial: %v (HTTP %d)", err, response.StatusCode)
		}
		t.Fatal(err)
	}
	defer conn.CloseNow()
	var firstRelease socketPendingRelease
	for _, input := range []string{"first", "second"} {
		request, _ := json.Marshal(map[string]any{"type": "response.create", "model": "fixture", "input": input})
		if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
			t.Fatal(err)
		}
		if input == "second" && releases != nil {
			select {
			case <-secondSend:
			case <-ctx.Done():
				t.Fatal("second Mistral send did not start")
			}
			if err := f.s.Release(ctx, firstRelease.lease); err != nil {
				t.Fatal(err)
			}
			firstRelease.cleanup(ctx)
			close(continueSecond)
		}
		for {
			_, payload, err := conn.Read(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.noSecrets(string(payload))
			var event struct {
				Type  string `json:"type"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(payload, &event); err != nil {
				t.Fatal(err)
			}
			if event.Type == "error" {
				t.Fatalf("turn %q: %s", input, event.Error.Code)
			}
			if event.Type == "response.completed" {
				break
			}
		}
		// The terminal frame precedes the release callback and worker completion.
		// The next turn should start only after the current worker has finished.
		if input == "first" {
			if releases != nil {
				select {
				case firstRelease = <-releases:
				case <-ctx.Done():
					t.Fatal("first release was not submitted")
				}
				time.Sleep(20 * time.Millisecond)
				continue
			}
			for {
				var released int
				if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM "`+f.schema+`".admissions WHERE released`).Scan(&released); err != nil {
					t.Fatal(err)
				}
				if released == 1 {
					break
				}
				select {
				case <-time.After(10 * time.Millisecond):
				case <-ctx.Done():
					t.Fatal("first turn admission was not released")
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if releases != nil {
		var secondRelease socketPendingRelease
		select {
		case secondRelease = <-releases:
		case <-ctx.Done():
			t.Fatal("second release was not submitted")
		}
		if err := f.s.Release(ctx, secondRelease.lease); err != nil {
			t.Fatal(err)
		}
		secondRelease.cleanup(ctx)
	}
	if sends.Load() != 2 {
		t.Fatalf("physical sends = %d, want 2", sends.Load())
	}
	conn.CloseNow()
	var total, released int
	for {
		if err := f.db.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE released) FROM "`+f.schema+`".admissions`).Scan(&total, &released); err != nil {
			t.Fatal(err)
		}
		if released == 2 {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("second turn admission was not released")
		}
	}
	if total != 2 || released != 2 {
		t.Fatalf("admissions = %d, released = %d; want 2 each", total, released)
	}
}
