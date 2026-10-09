//go:build !windows

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type startupOutcome struct {
	data        []byte
	diagnostics []byte
	err         error
	exit        int
}

func lifecycleBusy(outcome startupOutcome) bool {
	if outcome.err == nil || outcome.exit != 3 {
		return false
	}
	var response struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(outcome.data, &response) == nil && response.Error != nil && response.Error.Code == "lifecycle-busy"
}

func retryDaemonStart(ctx context.Context, attempt func(context.Context) startupOutcome) startupOutcome {
	var last startupOutcome
	for {
		if err := ctx.Err(); err != nil {
			last.err = err
			return last
		}
		result := attempt(ctx)
		if err := ctx.Err(); err != nil {
			// Preserve the last closed contention diagnostic when cancellation kills
			// a later attempt. The fixture deadline never grants another admission.
			if last.data == nil {
				last = result
			}
			last.err = err
			return last
		}
		if !lifecycleBusy(result) {
			return result
		}
		last = result
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			last.err = ctx.Err()
			return last
		case <-timer.C:
		}
	}
}

func TestDaemonStartRetryDecision(t *testing.T) {
	busy := startupOutcome{data: []byte(`{"error":{"code":"lifecycle-busy"}}`), err: errors.New("exit 3"), exit: 3}
	for _, test := range []struct {
		name   string
		result startupOutcome
		retry  bool
	}{
		{"closed contention", busy, true},
		{"other code", startupOutcome{data: []byte(`{"error":{"code":"unsafe-state"}}`), err: busy.err, exit: 3}, false},
		{"malformed JSON", startupOutcome{data: []byte(`{"error":`), err: busy.err, exit: 3}, false},
		{"wrong exit", startupOutcome{data: busy.data, err: busy.err, exit: 1}, false},
		{"launch failure", startupOutcome{err: errors.New("spawn failed"), exit: -1}, false},
		{"success", startupOutcome{data: []byte(`{"result":{}}`)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			calls := 0
			result := retryDaemonStart(ctx, func(context.Context) startupOutcome {
				calls++
				if calls == 1 {
					return test.result
				}
				return startupOutcome{}
			})
			want := 1
			if test.retry {
				want = 2
			}
			if calls != want || (!test.retry && result.err != test.result.err) {
				t.Fatalf("calls=%d result=%+v", calls, result)
			}
		})
	}
	t.Run("canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result := retryDaemonStart(ctx, func(context.Context) startupOutcome { t.Fatal("canceled attempt started"); return busy })
		if !errors.Is(result.err, context.Canceled) {
			t.Fatal(result.err)
		}
	})
	t.Run("contention deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		result := retryDaemonStart(ctx, func(context.Context) startupOutcome { return busy })
		if !errors.Is(result.err, context.DeadlineExceeded) || !bytes.Equal(result.data, busy.data) {
			t.Fatalf("lost final contention: %+v", result)
		}
	})
}
