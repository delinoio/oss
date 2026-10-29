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
)

func TestProviderAcceptanceSequentialResponsesSocketTurns(t *testing.T) {
	var sends atomic.Int32
	f := newProviderAcceptance(t, "azure-openai", "deployment", func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"fixture ok\"}\n\n"+
			"data: {\"type\":\"response.completed\",\"response\":"+providerAcceptanceResponses+"}\n\n")
	})
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
	for _, input := range []string{"first", "second"} {
		request, _ := json.Marshal(map[string]any{"type": "response.create", "model": "fixture", "input": input})
		if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
			t.Fatal(err)
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
