// SPDX-License-Identifier: Apache-2.0
package nativewire

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func pendingClockFixture(t *testing.T) (*Connection, Event) {
	t.Helper()
	c, _, _ := startFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, domain.NewID(), "current-time", struct{}{}); err != nil {
		t.Fatal(err)
	}
	event, err := c.Next(ctx)
	if err != nil || event.Kind != ServerRequest {
		t.Fatal(event, err)
	}
	return c, event
}
func TestCurrentTimeSamplesAfterOriginalClaimOnly(t *testing.T) {
	c, event := pendingClockFixture(t)
	samples := 0
	clock := func() time.Time { samples++; return time.Unix(1700000000, 987654321) }
	replaced := event
	replaced.Token = domain.NewID()
	if err := c.ReplyCurrentTime(context.Background(), replaced, clock); err == nil || samples != 0 {
		t.Fatal("replacement sampled or replied", err)
	}
	if err := c.ReplyCurrentTime(context.Background(), event, clock); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := c.Next(ctx)
	if err != nil || result.Method != "resolved" || string(result.Params) != `{"currentTimeAt":1700000000}` || samples != 1 {
		t.Fatal(result, err, samples)
	}
	if err := c.ReplyCurrentTime(ctx, event, clock); err == nil || samples != 1 {
		t.Fatal("replay resampled or resent", err)
	}
}
func TestCurrentTimeStoppedOrUncertainReplyCannotResample(t *testing.T) {
	for _, stopped := range []bool{false, true} {
		c, event := pendingClockFixture(t)
		samples := 0
		if stopped {
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
		}
		clock := func() time.Time { samples++; _ = c.process.CloseInput(); return time.Unix(1700000000, 1) }
		if err := c.ReplyCurrentTime(context.Background(), event, clock); err == nil {
			t.Fatal("closed pipe accepted reply")
		}
		expected := 1
		if stopped {
			expected = 0
		}
		if samples != expected {
			t.Fatal("wrong clock sampling count", samples)
		}
		if err := c.ReplyCurrentTime(context.Background(), event, clock); err == nil || samples != expected {
			t.Fatal("uncertain reply resampled or resent", err)
		}
	}
}
func TestCurrentTimeCannotBorrowOtherPendingMethod(t *testing.T) {
	c, _, _ := startFixture(t, "normal")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Call(ctx, domain.NewID(), "interactions", struct{}{}); err != nil {
		t.Fatal(err)
	}
	event, err := c.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	event.Method = "currentTime/read"
	samples := 0
	if err := c.ReplyCurrentTime(ctx, event, func() time.Time { samples++; return time.Now() }); err == nil || samples != 0 {
		t.Fatal("foreign method gained clock reply")
	}
	notification := Event{Kind: Notification, Method: "currentTime/read", ID: json.RawMessage(`7`), Token: event.Token}
	if err := c.ReplyCurrentTime(ctx, notification, time.Now); err == nil {
		t.Fatal("notification gained reply")
	}
}
