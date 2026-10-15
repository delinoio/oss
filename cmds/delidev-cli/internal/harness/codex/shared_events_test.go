// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func sharedNotification(t *testing.T, method string, params any) nativewire.Event {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return nativewire.Event{Kind: nativewire.Notification, Method: method, Params: raw}
}

func TestSharedEventsRouteMembersAndDescendants(t *testing.T) {
	router := newSharedEvents()
	first, second, child := domain.NewID(), domain.NewID(), domain.NewID()
	// Native start can precede the thread/start response that binds its member.
	if err := router.deliver(sharedNotification(t, "thread/started", map[string]any{"thread": map[string]any{"id": first}})); err != nil {
		t.Fatal(err)
	}
	firstEvents, err := router.bind(first)
	if err != nil {
		t.Fatal(err)
	}
	secondEvents, err := router.bind(second)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := router.next(context.Background(), firstEvents); err != nil || got.Method != "thread/started" {
		t.Fatalf("pending start: %v, %v", got.Method, err)
	}
	if err := router.deliver(sharedNotification(t, "thread/started", map[string]any{"thread": map[string]any{"id": child, "parentThreadId": first}})); err != nil {
		t.Fatal(err)
	}
	if err := router.deliver(sharedNotification(t, "turn/completed", map[string]any{"threadId": second})); err != nil {
		t.Fatal(err)
	}
	if err := router.deliver(sharedNotification(t, "item/completed", map[string]any{"threadId": child})); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"thread/started", "item/completed"} {
		got, err := router.next(context.Background(), firstEvents)
		if err != nil || got.Method != method {
			t.Fatalf("first member: got %v, %v; want %v", got.Method, err, method)
		}
	}
	got, err := router.next(context.Background(), secondEvents)
	if err != nil || got.Method != "turn/completed" {
		t.Fatalf("second member: %v, %v", got.Method, err)
	}
	select {
	case event := <-secondEvents:
		t.Fatalf("cross-thread event: %v", event.Method)
	default:
	}
}

func TestSharedEventsRouteLateResponseByRequest(t *testing.T) {
	router := newSharedEvents()
	first, second, request := domain.NewID(), domain.NewID(), domain.NewID()
	firstEvents, err := router.bind(first)
	if err != nil {
		t.Fatal(err)
	}
	secondEvents, err := router.bind(second)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.trackRequest(request, second); err != nil {
		t.Fatal(err)
	}
	id, _ := json.Marshal(request)
	if err := router.deliver(nativewire.Event{Kind: nativewire.LateResponse, ID: id}); err != nil {
		t.Fatal(err)
	}
	if got, err := router.next(context.Background(), secondEvents); err != nil || got.Kind != nativewire.LateResponse {
		t.Fatalf("late response: %v, %v", got.Kind, err)
	}
	select {
	case <-firstEvents:
		t.Fatal("late response reached another member")
	default:
	}
	if err := router.deliver(nativewire.Event{Kind: nativewire.LateResponse, ID: id}); err == nil {
		t.Fatal("duplicate late response must require recovery")
	}
}

func TestSharedEventsFailClosedOnUnroutableEvent(t *testing.T) {
	router := newSharedEvents()
	thread := domain.NewID()
	queue, err := router.bind(thread)
	if err != nil {
		t.Fatal(err)
	}
	if err := router.deliver(sharedNotification(t, "unknown/global", map[string]any{})); err == nil {
		t.Fatal("unroutable event accepted")
	}
	router.fail(sharedEventUncertain())
	if _, err := router.next(context.Background(), queue); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatalf("member was not fenced: %v", err)
	}
}
