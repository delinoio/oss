package opencode

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestOwnedAPIEmptyValuesHaveNoAuthority(t *testing.T) {
	for _, api := range []*OwnedAPI{nil, {}} {
		ctx := context.Background()
		if _, err := api.CreateSession(ctx, domain.NewID()); err == nil {
			t.Fatal("empty owner gained creation authority")
		}
		if _, err := api.StartText(ctx, domain.NewID(), "private prompt"); err == nil {
			t.Fatal("empty owner gained input authority")
		}
		if _, err := api.Next(ctx); err == nil {
			t.Fatal("empty owner gained event authority")
		}
		if _, err := api.Progress(ctx); err == nil {
			t.Fatal("empty owner gained observation authority")
		}
		if err := api.Close(ctx); err == nil {
			t.Fatal("empty owner gained cleanup authority")
		}
		if _, err := api.CloseCompleted(ctx); err == nil {
			t.Fatal("empty owner gained completed cleanup authority")
		}
		if _, _, err := api.RetainCheckpoint(ctx); err == nil {
			t.Fatal("empty owner gained checkpoint authority")
		}
	}
}

func TestOwnedAPIInvalidInputDoesNotConsumeOriginalListener(t *testing.T) {
	f := newSessionFixture(t)
	original := domain.NewID()
	if _, err := f.api.create(context.Background(), original, fixtureSettings()); err != nil {
		t.Fatal(err)
	}
	api := &OwnedAPI{session: f.api, reading: make(chan struct{}, 1)}
	for _, test := range []struct {
		request domain.ID
		text    string
	}{
		{original, "private input"},
		{"invalid", "private input"},
		{domain.NewID(), ""},
		{domain.NewID(), "invalid\x00input"},
		{domain.NewID(), "invalid\xffinput"},
		{domain.NewID(), strings.Repeat("x", (256<<10)+1)},
		{domain.NewID(), strings.Repeat("\x01", 256<<10)},
	} {
		if _, err := api.StartText(context.Background(), test.request, test.text); err == nil || f.api.eventAttempt || f.api.events != nil || f.api.input != nil || f.postCount() != 1 || len(f.claims) != 1 {
			t.Fatal("invalid native input changed original claim/subscription authority")
		}
	}
}

func TestOwnedAPIConcurrentNextPreservesOriginalObservationOrder(t *testing.T) {
	f := newObserverFixture(t)
	first := f.event(MessageUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "info": f.user})
	second := f.event(MessagePartUpdatedEvent, map[string]any{"sessionID": fixtureSessionID, "part": f.input, "time": 1240})
	stream := &eventStream{ctx: context.Background(), queue: make(chan NativeEvent, 2), pending: len(first.Properties) + len(second.Properties)}
	stream.queue <- first
	stream.queue <- second
	session := &sessionAPI{creation: &f.o.creation, input: &f.o.input, observer: f.o, events: stream, gate: make(chan struct{}, 1)}
	api := &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	// Hold observation, after the first consumer can dequeue its event. A
	// competing consumer must not remove/process the input part before user.
	f.o.mu.Lock()
	locked := true
	defer func() {
		if locked {
			f.o.mu.Unlock()
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := api.Next(context.Background())
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for len(stream.queue) != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(stream.queue) != 1 {
		t.Fatal("original first event was not dequeued")
	}
	waiter, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := api.Next(waiter); err == nil || len(stream.queue) != 1 || stream.status() != nil {
		t.Fatal("canceled second consumer reordered or canceled the original stream")
	}
	f.o.mu.Unlock()
	locked = false
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	observation, err := api.Next(context.Background())
	if err != nil || observation.EventID != second.ID {
		t.Fatal("second original event was lost")
	}
	progress, err := api.Progress(context.Background())
	if err != nil || !progress.UserSeen || !progress.InputPartSeen || progress.NeedsRecovery {
		t.Fatal("serialized original observations lost their input ownership")
	}
}

func TestOwnedAPICanBindOriginalUnobservedClaimForCleanup(t *testing.T) {
	f := newStopFixture(t, PermissionInteraction)
	session := f.r.api
	session.observer, session.runtimeRoot = nil, f.r.f.o.root
	api := &OwnedAPI{session: session, reading: make(chan struct{}, 1)}
	progress, err := api.Progress(context.Background())
	if err != nil || progress.UserSeen || progress.InputPartSeen || progress.RequestID != session.input.receipt.RequestID || f.r.posts != 0 {
		t.Fatal("original claim binding invented native observations or reads")
	}
	stop, err := api.ClaimOwnedStop(context.Background(), domain.NewID())
	if err != nil || stop.NativeAttempted || stop.HTTPAccepted || f.r.posts != 0 || len(f.r.claims) != 1 {
		t.Fatal("unobserved original claim gained native Stop or replay authority")
	}
	stop, err = api.FinishStopCleanup(context.Background())
	if err != nil || !stop.CleanupVerified || f.closeCalls != 1 {
		t.Fatal("original cleanup authority was not joined")
	}
}
