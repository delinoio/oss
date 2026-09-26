package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type stoppedHistoryFixture struct {
	stop    *stopFixture
	history *historyFixture
	pending []json.RawMessage
	kind    InteractionKind
}

func (f *stoppedHistoryFixture) posts() int {
	f.stop.r.mu.Lock()
	defer f.stop.r.mu.Unlock()
	return f.stop.r.posts
}

func newStoppedHistoryFixture(t *testing.T, kind InteractionKind, lost bool) *stoppedHistoryFixture {
	t.Helper()
	stop := newStopFixture(t, kind)
	r := stop.r
	r.lost = lost
	if _, err := r.api.stopInput(context.Background(), r.f.o, domain.NewID()); (err != nil) != lost {
		t.Fatal(err)
	}
	observeInterruptedStop(r)
	history := &historyFixture{t: t, o: r.f.o, api: &OwnedAPI{session: r.api, reading: make(chan struct{}, 1)}}
	f := &stoppedHistoryFixture{stop: stop, history: history, kind: kind, pending: []json.RawMessage{append([]byte(nil), r.f.o.interactions[r.id].raw...)}}
	r.api.client = &http.Client{Transport: f}
	return f
}

func (f *stoppedHistoryFixture) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Path != "/"+string(f.kind) {
		return f.history.RoundTrip(request)
	}
	f.history.reads = append(f.history.reads, request.URL.RequestURI())
	if request.Method != http.MethodGet {
		f.history.t.Error("stopped history performed another mutation")
	}
	raw, _ := json.Marshal(f.pending)
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
}

func TestOpenCodeStoppedHistoryPreservesUnansweredCancellationAndLostHTTP(t *testing.T) {
	for _, kind := range []InteractionKind{PermissionInteraction, QuestionInteraction} {
		for _, lost := range []bool{false, true} {
			f := newStoppedHistoryFixture(t, kind, lost)
			a := f.history.api
			proof, err := a.CloseAfterStop(context.Background())
			if err != nil || !proof.Stop.InterruptedObserved || !proof.Stop.TerminalObserved || !proof.Stop.IdleObserved || !proof.Stop.CleanupVerified || !proof.Stop.PendingCleared || proof.Stop.HTTPAccepted == lost || proof.Stop.RepliesUncertain || f.stop.closeCalls != 1 || f.posts() != 1 || len(proof.History.Messages) != 2 || len(proof.History.Digest) != 64 {
				t.Fatal("original stopped history lost independent facts", err)
			}
			original := f.history.o.interactions[f.stop.r.id]
			if !original.closed || !original.canceled || original.rejected || original.attempt != nil {
				t.Fatal("interrupted pending request acquired an answer or rejection")
			}
			retained := copyStoppedHistory(proof)
			proof.History.Messages[0].Parts[0].Digest = "changed"
			proof.Stop.RequestID = domain.NewID()
			again, err := a.CloseAfterStop(context.Background())
			if err != nil || !reflect.DeepEqual(retained, again) || f.stop.closeCalls != 1 || f.posts() != 1 {
				t.Fatal("cached stopped history repeated cleanup or shared caller memory")
			}
			if _, err := a.CloseCompleted(context.Background()); err == nil {
				t.Fatal("Stop cleanup acquired normal completion authority")
			}
		}
	}
}

func TestOpenCodeStoppedHistoryRejectsUnprovenScopeBeforeCleanup(t *testing.T) {
	for _, name := range []string{"owner-only", "unsent", "wrong-claim", "wrong-receipt", "unsettled", "missing-idle", "missing-terminal", "unknown-pending", "changed-pending", "duplicate-pending", "pending-null", "busy", "changed-message", "changed-part", "uncertain-answer", "missing-cleanup"} {
		t.Run(name, func(t *testing.T) {
			f := newStoppedHistoryFixture(t, PermissionInteraction, false)
			o := f.history.o
			switch name {
			case "owner-only":
				o.stop.claim.Kind = StopOwnedRuntimeMutation
			case "unsent":
				o.stop.sent = false
			case "wrong-claim":
				o.stop.claim.InputRequestID = domain.NewID()
			case "wrong-receipt":
				o.stop.receipt.InputRequestID = domain.NewID()
			case "unsettled":
				o.progress.SettledObserved = false
			case "missing-idle":
				o.progress.IdleNotification = false
			case "missing-terminal":
				o.progress.TerminalObserved = false
			case "unknown-pending", "changed-pending":
				var value map[string]any
				_ = json.Unmarshal(f.pending[0], &value)
				if name == "unknown-pending" {
					value["id"] = "per_01960dcbe1fcABCDEFGHIJKLMN"
				} else {
					value["permission"] = "foreign"
				}
				f.pending[0], _ = json.Marshal(value)
			case "duplicate-pending":
				f.pending = append(f.pending, f.pending[0])
			case "pending-null":
				f.pending = nil
			case "busy", "changed-message", "changed-part":
				f.history.mode = name
			case "uncertain-answer":
				o.interactions[f.stop.r.id].attempt = &interactionAttempt{sent: true, receipt: InteractionReceipt{RequestID: domain.NewID()}}
			case "missing-cleanup":
				f.history.api.session.closeOwned = nil
			}
			proof, err := f.history.api.CloseAfterStop(context.Background())
			if err == nil || proof.History.Digest != "" || f.stop.closeCalls != 0 || f.history.api.completionAttempted {
				t.Fatal("unproven stopped history reached process cleanup")
			}
		})
	}
}

func TestOpenCodeStoppedHistoryCannotAdoptEarlierOrFailedCleanup(t *testing.T) {
	for _, earlier := range []bool{false, true} {
		f := newStoppedHistoryFixture(t, PermissionInteraction, false)
		a := f.history.api
		if earlier {
			if _, err := a.FinishStopCleanup(context.Background()); err != nil {
				t.Fatal(err)
			}
		} else {
			a.session.closeOwned = func(context.Context) error { f.stop.closeCalls++; return errors.New("private cleanup failure") }
			if _, err := a.CloseAfterStop(context.Background()); err == nil || f.stop.closeCalls != 1 {
				t.Fatal("failed cleanup acquired stopped history proof")
			}
			a.session.closeOwned = func(context.Context) error { f.stop.closeCalls++; return nil }
			if err := a.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		before := f.stop.closeCalls
		if proof, err := a.CloseAfterStop(context.Background()); err == nil || proof.History.Digest != "" || f.stop.closeCalls != before {
			t.Fatal("ordinary cleanup fabricated original stopped-history proof")
		}
	}
}

func TestOpenCodeStoppedHistoryMissingInventoryDoesNotBecomeAnAnswer(t *testing.T) {
	f := newStoppedHistoryFixture(t, QuestionInteraction, true)
	f.pending = []json.RawMessage{}
	proof, err := f.history.api.CloseAfterStop(context.Background())
	original := f.history.o.interactions[f.stop.r.id]
	if err != nil || !proof.Stop.CleanupVerified || proof.Stop.HTTPAccepted || !original.canceled || !original.closed || original.rejected || original.attempt != nil {
		t.Fatal("missing native pending entry acquired response authority", err)
	}
}

func TestOpenCodeStoppedHistoryCancellationAndReaderCannotConsumeCleanup(t *testing.T) {
	for _, reader := range []bool{false, true} {
		f := newStoppedHistoryFixture(t, PermissionInteraction, false)
		a := f.history.api
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		if reader {
			a.reading <- struct{}{}
		} else {
			cancel()
		}
		_, err := a.CloseAfterStop(ctx)
		cancel()
		if err == nil || f.stop.closeCalls != 0 || len(f.history.reads) != 0 || a.completionAttempted {
			t.Fatal("canceled or concurrent reader consumed original Stop cleanup")
		}
		if reader {
			<-a.reading
		}
		results := make(chan StoppedHistoryObservation, 2)
		for range 2 {
			go func() {
				proof, err := a.CloseAfterStop(context.Background())
				if err != nil {
					t.Error(err)
				}
				results <- proof
			}()
		}
		one, two := <-results, <-results
		if f.stop.closeCalls != 1 || f.posts() != 1 || one.History.Digest == "" || !reflect.DeepEqual(one, two) {
			t.Fatal("concurrent Stop completion repeated mutation/cleanup or lost proof")
		}
	}
}
