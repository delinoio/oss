// SPDX-License-Identifier: Apache-2.0
package nativewire

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func generatedReplyRequest(t *testing.T, c *Connection) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, domain.NewID(), "interactions", struct{}{}); err != nil {
		t.Fatal(err)
	}
	event, err := c.Next(ctx)
	if err != nil || event.Kind != ServerRequest {
		t.Fatal("original request missing", err)
	}
	if _, err := c.Next(ctx); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestGeneratedReplyClaimsBeforeOneConcurrentSample(t *testing.T) {
	c, _, _ := startFixture(t, "normal")
	event := generatedReplyRequest(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var samples atomic.Int32
	var joined sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		joined.Add(1)
		go func() {
			defer joined.Done()
			errors <- c.ReplyGenerated(ctx, event, func() (any, error) {
				samples.Add(1)
				return map[string]int64{"currentTimeAt": 1700000000}, nil
			})
		}()
	}
	joined.Wait()
	close(errors)
	sent, conflicts := 0, 0
	for err := range errors {
		if err == nil {
			sent++
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if sent != 1 || conflicts != 1 || samples.Load() != 1 {
		t.Fatal("reply sampled or sent more than once")
	}
	response, err := c.Next(ctx)
	if err != nil || string(response.Params) != `{"currentTimeAt":1700000000}` {
		t.Fatal("generated result changed", err)
	}
	produce := func() (any, error) { samples.Add(1); return struct{}{}, nil }
	if err := c.ReplyGenerated(ctx, event, produce); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("answered arrival was replayable")
	}
	replacement := generatedReplyRequest(t, c)
	if string(replacement.ID) != string(event.ID) || replacement.Token == event.Token {
		t.Fatal("fixture did not replace the native request identity")
	}
	if err := c.ReplyGenerated(ctx, event, produce); domain.SafeError(err).Code != domain.Conflict || samples.Load() != 1 {
		t.Fatal("stale token sampled against a replacement")
	}
	if retired, err := c.RetireRequest(replacement); !retired || err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedReplyNeverSamplesUnownedCanceledOrStoppedArrival(t *testing.T) {
	for _, kind := range []string{"token", "ID", "notification", "canceled", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			c, _, _ := startFixture(t, "normal")
			event := generatedReplyRequest(t, c)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "token":
				event.Token = domain.NewID()
			case "ID":
				event.ID = json.RawMessage(`null`)
			case "notification":
				event.Kind = Notification
			case "canceled":
				cancel()
			case "stopped":
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			}
			samples := 0
			if err := c.ReplyGenerated(ctx, event, func() (any, error) { samples++; return struct{}{}, nil }); err == nil || samples != 0 {
				t.Fatal("unowned request sampled a generated value")
			}
		})
	}
}

func TestGeneratedReplyFailedProductionAndPipeRetainClaim(t *testing.T) {
	for _, kind := range []string{"producer", "encoding", "pipe"} {
		t.Run(kind, func(t *testing.T) {
			mode := "normal"
			if kind == "pipe" {
				mode = "blocked-input"
			}
			c, config, _ := startFixture(t, mode)
			event := Event{Kind: ServerRequest, ID: json.RawMessage(`7`), Token: domain.NewID()}
			if kind != "pipe" {
				event = generatedReplyRequest(t, c)
			} else {
				// The bounded-input fixture cannot emit a request because it
				// never reads stdin. Seed its original arrival for the pipe test.
				c.mu.Lock()
				c.incoming["n:7"] = incoming{token: event.Token}
				c.mu.Unlock()
			}
			samples := 0
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			claimed := make(chan struct{})
			go func() {
				select {
				case <-claimed:
					if kind == "pipe" {
						cancel()
					}
				case <-ctx.Done():
				}
			}()
			err := c.ReplyGenerated(ctx, event, func() (any, error) {
				samples++
				close(claimed)
				switch kind {
				case "producer":
					return nil, errors.New("private-producer-error")
				case "encoding":
					return make(chan int), nil
				default:
					return strings.Repeat("x", 900<<10), nil
				}
			})
			if domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("failed generated reply did not retain uncertainty", err)
			}
			if err := c.ReplyGenerated(context.Background(), event, func() (any, error) { samples++; return struct{}{}, nil }); err == nil || samples != 1 {
				t.Fatal("uncertain response was resampled")
			}
			c.mu.Lock()
			retained := c.incoming["n:7"].replying
			c.mu.Unlock()
			if !retained || c.Close() != nil || process.ReconcileOwner(config.Directory, config.OwnerID) != nil {
				t.Fatal("original response claim or independent cleanup was lost")
			}
		})
	}
}
