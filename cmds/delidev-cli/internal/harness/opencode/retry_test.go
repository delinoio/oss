package opencode

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOriginalRetryRetainsSchedulingWithoutStopAuthority(t *testing.T) {
	f := newObserverFixture(t)
	f.start()
	o := f.observe(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": "retry", "attempt": 2, "next": 1300, "message": "private diagnostic"}})
	if o.Retry == nil || *o.Retry != (NativeRetry{Attempt: 2, Next: 1300}) || f.o.stop != nil || f.o.snapshot().Status != NativeStatusRetry || f.o.snapshot().TerminalObserved || f.o.snapshot().SettledObserved {
		t.Fatal("retry observation invented Stop, acceptance or completion")
	}
	frozen, err := o.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	o.Retry.Attempt = 999
	copy, err := frozen.Thaw()
	if err != nil || copy.Retry.Attempt != 2 || f.o.currentRetry.value.Attempt != 2 {
		t.Fatal("caller mutation changed original retry observation")
	}
}

func TestEarlierRetryCannotAuthorizeLaterMissingFinish(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		t.Run("stop-first="+fmtBool(stopFirst), func(t *testing.T) {
			f := newStopFixture(t, PermissionInteraction)
			r := f.r
			if stopFirst {
				if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err != nil {
					t.Fatal(err)
				}
			}
			r.f.observe(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": "retry", "attempt": 1, "next": 1300, "message": "private diagnostic"}})
			r.f.status(NativeStatusBusy)
			if !stopFirst {
				if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); err != nil {
					t.Fatal(err)
				}
			}
			value := *r.f.o.messages[r.f.o.progress.AssistantID].value.Assistant
			end := int64(1400)
			value.Completed = &end
			if r.f.o.stoppedBackoffCandidate(NativeMessage{ID: r.f.o.progress.AssistantID, Assistant: &value}) || !stopFirst && len(r.f.o.stop.retries) != 0 {
				t.Fatal("an earlier retry authorized a later unrelated cancellation shape")
			}
		})
	}
}

func TestNativeRetryRejectsUnboundedOrActionBearingSchedules(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(v map[string]any) { v["action"] = map[string]any{"link": "private://provider"} },
		func(v map[string]any) { v["attempt"] = -1 },
		func(v map[string]any) { v["attempt"] = 0.5 },
		func(v map[string]any) { v["next"] = uint64(9007199254740992) },
		func(v map[string]any) { v["next"] = nil },
		func(v map[string]any) { delete(v, "message") },
		func(v map[string]any) { v["message"] = "invalid\x00diagnostic" },
	} {
		value := map[string]any{"type": "retry", "attempt": 1, "next": 1300, "message": "private diagnostic"}
		change(value)
		raw, _ := json.Marshal(value)
		if _, err := decodeNativeRetry(raw); err == nil {
			t.Fatal("unsupported native retry acquired scheduling evidence")
		}
	}
	f := newObserverFixture(t)
	f.start()
	f.o.retries = make([]observedRetry, 1024)
	f.reject(SessionStatusEvent, map[string]any{"sessionID": fixtureSessionID, "status": map[string]any{"type": "retry", "attempt": 1, "next": 1300, "message": "private diagnostic"}})
}
