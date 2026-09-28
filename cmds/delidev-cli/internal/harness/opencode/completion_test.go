package opencode

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func prepareHistoryCleanup(f *historyFixture, cleanup func(context.Context) error) {
	stream := f.api.session.events
	stream.cancel = func() {}
	stream.body = io.NopCloser(strings.NewReader(""))
	stream.done = make(chan struct{})
	close(stream.done)
	f.api.session.closeOwned = cleanup
}

func TestOpenCodeCompletedCleanupRetainsOriginalHistoryAndIndependentCopies(t *testing.T) {
	f := newHistoryFixture(t)
	calls := 0
	prepareHistoryCleanup(f, func(context.Context) error { calls++; return nil })
	proof, err := f.api.CloseCompleted(context.Background())
	if err != nil || calls != 1 || len(proof.Messages) != 2 || !f.api.completionAttempted || f.api.session.events.status() == nil {
		t.Fatal("original completion did not join history and stream cleanup", err)
	}
	retained := copyHistoryObservation(proof)
	proof.Messages[0].ID = "changed"
	proof.Messages[0].Parts[0].Digest = "changed"
	again, err := f.api.CloseCompleted(context.Background())
	if err != nil || calls != 1 || !reflect.DeepEqual(retained, again) {
		t.Fatal("completed cleanup repeated native work or shared mutable proof")
	}
	if _, err := f.api.Next(context.Background()); err == nil || f.o.snapshot().NeedsRecovery {
		t.Fatal("closed original stream resumed or rewrote settled evidence")
	}
}

func TestOpenCodeCompletedCleanupRejectsMissingHistoryBeforeSideEffects(t *testing.T) {
	for _, mode := range []string{"pending", "busy", "changed-message", "changed-part", "transient", "unsettled", "missing-cleanup"} {
		t.Run(mode, func(t *testing.T) {
			f := newHistoryFixture(t)
			calls := 0
			prepareHistoryCleanup(f, func(context.Context) error { calls++; return nil })
			f.mode = mode
			if mode == "unsettled" {
				f.o.progress.SettledObserved = false
			}
			if mode == "missing-cleanup" {
				f.api.session.closeOwned = nil
			}
			proof, err := f.api.CloseCompleted(context.Background())
			if err == nil || calls != 0 || f.api.completionAttempted || proof.Digest != "" {
				t.Fatal("missing original completion evidence reached cleanup")
			}
		})
	}
}

func TestOpenCodeCompletedCleanupFailureCannotBecomeSuccessAfterOrdinaryClose(t *testing.T) {
	f := newHistoryFixture(t)
	calls := 0
	prepareHistoryCleanup(f, func(context.Context) error {
		calls++
		return domain.Fail(domain.RecoveryRequired, "Fixture cleanup failure.", "Retain ownership.")
	})
	if proof, err := f.api.CloseCompleted(context.Background()); err == nil || proof.Digest != "" || calls != 1 {
		t.Fatal("failed cleanup produced completion proof")
	}
	f.api.session.closeOwned = func(context.Context) error { calls++; return nil }
	if err := f.api.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if proof, err := f.api.CloseCompleted(context.Background()); err == nil || proof.Digest != "" || calls != 2 {
		t.Fatal("ordinary cleanup erased completion uncertainty or repeated work")
	}
}

func TestOpenCodeCompletedCleanupCancellationDoesNotConsumeAuthority(t *testing.T) {
	f := newHistoryFixture(t)
	calls := 0
	prepareHistoryCleanup(f, func(context.Context) error { calls++; return nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.api.CloseCompleted(ctx); err == nil || calls != 0 || len(f.reads) != 0 || f.api.completionAttempted {
		t.Fatal("canceled caller consumed original cleanup")
	}
	if _, err := f.api.CloseCompleted(context.Background()); err != nil || calls != 1 {
		t.Fatal("canceled read fabricated a mutation claim")
	}
}

func TestOpenCodeCompletedCleanupSerializesWithOriginalReader(t *testing.T) {
	f := newHistoryFixture(t)
	calls := 0
	prepareHistoryCleanup(f, func(context.Context) error { calls++; return nil })
	f.api.reading <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := f.api.CloseCompleted(ctx); err == nil || calls != 0 || len(f.reads) != 0 || f.api.completionAttempted {
		t.Fatal("completion bypassed the original reader")
	}
	<-f.api.reading
	results := make(chan HistoryObservation, 2)
	for range 2 {
		go func() {
			proof, err := f.api.CloseCompleted(context.Background())
			if err != nil {
				t.Error(err)
			}
			results <- proof
		}()
	}
	one, two := <-results, <-results
	if calls != 1 || one.Digest == "" || !reflect.DeepEqual(one, two) {
		t.Fatal("concurrent original completion repeated cleanup or lost original proof")
	}
}
