// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// grokLogCapture records the structured events a Service emits.
type grokLogCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *grokLogCapture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

// capture replaces the fixture Service's logger and returns its recorder.
func (f *grokFixture) captureLogs() *grokLogCapture {
	c := &grokLogCapture{}
	f.service.logger = slog.New(slog.NewJSONHandler(c, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return c
}

// events returns every captured record whose message starts with grok_subscription_.
func (c *grokLogCapture) events(t *testing.T) []map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(c.buf.String()), "\n") {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("unreadable log line %q: %v", line, err)
		}
		if msg, _ := record["msg"].(string); strings.HasPrefix(msg, "grok_subscription_") {
			out = append(out, record)
		}
	}
	return out
}

// named returns the captured records of one event.
func (c *grokLogCapture) named(t *testing.T, event string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, record := range c.events(t) {
		if record["msg"] == event {
			out = append(out, record)
		}
	}
	return out
}

// requireLogged asserts how many records of one event were emitted.
func (c *grokLogCapture) requireLogged(t *testing.T, event string, want int) []map[string]any {
	t.Helper()
	got := c.named(t, event)
	if len(got) != want {
		t.Fatalf("%s logged %d times, want %d: %v", event, len(got), want, c.events(t))
	}
	return got
}

// The log table of a Grok refusal. Each event states what the mutation
// persisted: recorded only when the refusal reached the account, not_recorded
// (with its reason) when the mutation wrote nothing, unrecorded when the store
// failed. A line never claims more than the commit established.
func TestGrokTakeRefusalLogsOnlyWhatItPersisted(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	d := domain.GrokErrorDiagnostic(grokAdmissionFailure(&domain.Installation{Harness: domain.GrokBuild, State: domain.InstallationDetected, Version: "1.0.45"}))
	const (
		recorded    = "grok_subscription_take_refusal_recorded"
		notRecorded = "grok_subscription_take_refusal_not_recorded"
		unrecorded  = "grok_subscription_take_refusal_unrecorded"
	)
	record := func(f *grokFixture, account, operation domain.ID, action domain.SubscriptionAction) {
		f.service.recordGrokTakeRefusal(context.Background(), account, operation, f.input.MachineID, action, d)
	}
	for _, test := range []struct {
		name string
		// run drives recordGrokTakeRefusal against the fixture.
		run func(*grokFixture, domain.ID)
		// want is the number of records per outcome event; reason is the reason
		// of the not_recorded event.
		want   map[string]int
		reason grokRecordOutcome
	}{
		{name: "exact queued original", run: func(f *grokFixture, op domain.ID) {
			record(f, f.input.AccountID, op, domain.SubscriptionLogin)
		}, want: map[string]int{recorded: 1}},
		{name: "identical refusal already stored", run: func(f *grokFixture, op domain.ID) {
			record(f, f.input.AccountID, op, domain.SubscriptionLogin)
			record(f, f.input.AccountID, op, domain.SubscriptionLogin)
		}, want: map[string]int{recorded: 1, notRecorded: 1}, reason: grokRecordKept},
		{name: "another operation", run: func(f *grokFixture, op domain.ID) {
			record(f, f.input.AccountID, domain.NewID(), domain.SubscriptionLogin)
		}, want: map[string]int{notRecorded: 1}, reason: grokRecordTargetChanged},
		{name: "another lifecycle action", run: func(f *grokFixture, op domain.ID) {
			record(f, f.input.AccountID, op, domain.SubscriptionRefresh)
		}, want: map[string]int{notRecorded: 1}, reason: grokRecordTargetChanged},
		{name: "claimed original", run: func(f *grokFixture, op domain.ID) {
			if _, err := f.take(&pb.RequestSubscriptionResponse{OperationId: string(op)}, login); err != nil {
				f.t.Fatal(err)
			}
			record(f, f.input.AccountID, op, domain.SubscriptionLogin)
		}, want: map[string]int{notRecorded: 1}, reason: grokRecordTargetChanged},
		{name: "missing account", run: func(f *grokFixture, op domain.ID) {
			record(f, domain.NewID(), op, domain.SubscriptionLogin)
		}, want: map[string]int{unrecorded: 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			op := f.queue(login)
			logs := f.captureLogs()
			test.run(f, domain.ID(op.OperationId))
			for _, event := range []string{recorded, notRecorded, unrecorded} {
				logs.requireLogged(t, event, test.want[event])
			}
			if test.want[notRecorded] > 0 {
				if got := logs.named(t, notRecorded)[0]["reason"]; got != string(test.reason) {
					t.Fatalf("%s reason = %v, want %s", notRecorded, got, test.reason)
				}
			}
			// Persistence is the ground truth for the recorded event.
			_, a := f.record()
			if persisted := grokRecordFor(a.Subscription, op.OperationId) != nil; persisted != (test.want[recorded] > 0) {
				t.Fatalf("persisted = %v but %s logged %d times", persisted, recorded, test.want[recorded])
			}
		})
	}
}

// A refused Take logs its pre-persistence classification as a possibility, never
// as an outcome: refusal_recordable is true only for the exact queued original,
// and the outcome event follows from the recording transaction.
func TestGrokTakeInstallationRefusalLogsRecordableNotRecorded(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	for _, test := range []struct {
		name        string
		foreign     bool
		recordable  bool
		recordedLog int
	}{
		{name: "queued original", recordable: true, recordedLog: 1},
		{name: "foreign operation", foreign: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			f.setGrokInstallation(t, func(installations *[]domain.Installation) { (*installations)[0].Version = "1.0.45" })
			op := f.queue(login)
			sent := op
			if test.foreign {
				sent = &pb.RequestSubscriptionResponse{OperationId: string(domain.NewID())}
			}
			logs := f.captureLogs()
			if _, err := f.take(sent, login); err == nil {
				t.Fatal("Take admitted an installation below the subscription baseline")
			}
			refused := logs.requireLogged(t, "grok_subscription_take_installation_refused", 1)[0]
			if _, claimed := refused["refusal_recorded"]; claimed {
				t.Fatal("the pre-persistence refusal line claims an outcome")
			}
			if refused["refusal_recordable"] != test.recordable {
				t.Fatalf("refusal_recordable = %v, want %v", refused["refusal_recordable"], test.recordable)
			}
			logs.requireLogged(t, "grok_subscription_take_refusal_recorded", test.recordedLog)
			_, a := f.record()
			if persisted := grokRecordFor(a.Subscription, op.OperationId) != nil; persisted != test.recordable {
				t.Fatalf("persisted = %v, want %v", persisted, test.recordable)
			}
		})
	}
}

// The log table of a Grok cleanup failure after Finish settled. The cleanup
// diagnostic is "recorded" only when it replaced the stored outcome; a native
// failure that already stored its own diagnostic stays authoritative and is
// logged as not_recorded (record_kept) even though the cleanup failure is real.
func TestGrokCleanupFailureLogsOnlyWhatItPersisted(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	cause := domain.Fail(domain.Unavailable, "The vault removal failed.", "")
	for _, test := range []struct {
		name string
		// finish settles the original login; success selects a succeeded outcome.
		success bool
		// other records the cleanup against another operation.
		other   bool
		missing bool
		event   string
		reason  string
	}{
		{name: "succeeded outcome", success: true, event: "grok_subscription_cleanup_failure_recorded"},
		{name: "native failure already stored its diagnostic", event: "grok_subscription_cleanup_failure_not_recorded", reason: string(grokRecordKept)},
		{name: "another operation", success: true, other: true, event: "grok_subscription_cleanup_failure_not_recorded", reason: string(grokRecordTargetChanged)},
		{name: "missing account", success: true, missing: true, event: "grok_subscription_cleanup_failure_unrecorded"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGrokFixture(t)
			op := f.queue(login)
			lease, err := f.take(op, login)
			if err != nil {
				t.Fatal(err)
			}
			generation := domain.NewID()
			var bundle []byte
			if test.success {
				bundle = f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1")
			}
			if _, err := f.finishWith(lease, generation, bundle, test.success, false, true); err != nil {
				t.Fatal(err)
			}
			logs := f.captureLogs()
			account, operation := f.input.AccountID, domain.ID(op.OperationId)
			if test.other {
				operation = domain.NewID()
			}
			if test.missing {
				account = domain.NewID()
			}
			before, _ := f.record()
			f.service.recordGrokCleanupFailure(context.Background(), account, operation, domain.GrokSubscriptionBaselineVersion, cause)
			for _, event := range []string{"grok_subscription_cleanup_failure_recorded", "grok_subscription_cleanup_failure_not_recorded", "grok_subscription_cleanup_failure_unrecorded"} {
				want := 0
				if event == test.event {
					want = 1
				}
				logs.requireLogged(t, event, want)
			}
			if test.reason != "" {
				if got := logs.named(t, test.event)[0]["reason"]; got != test.reason {
					t.Fatalf("reason = %v, want %s", got, test.reason)
				}
			}
			after, a := f.record()
			changed := after.Revision != before.Revision
			recorded := len(logs.named(t, "grok_subscription_cleanup_failure_recorded")) > 0
			if changed != recorded {
				t.Fatalf("account changed = %v but recorded logged = %v", changed, recorded)
			}
			if o := a.Subscription.ServerOperation; recorded && (o.GrokDiagnostic == nil || o.GrokDiagnostic.Phase != domain.GrokCleanup) {
				t.Fatalf("recorded without the cleanup diagnostic: %+v", o)
			}
		})
	}
}

// Finish logs its settlement only after the transaction committed, and exactly
// once: a replay of the same request settles nothing again.
func TestGrokFinishLogsTheSettlementOnceAfterTheCommit(t *testing.T) {
	login := pb.SubscriptionAction_SUBSCRIPTION_ACTION_LOGIN
	f := newGrokFixture(t)
	op := f.queue(login)
	lease, err := f.take(op, login)
	if err != nil {
		t.Fatal(err)
	}
	logs := f.captureLogs()
	generation := domain.NewID()
	bundle := f.sealed(t, f.input.AccountID, generation, "synthetic-principal-1")
	if _, err := f.finishWith(lease, generation, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	settled := logs.requireLogged(t, "grok_subscription_operation_settled", 1)[0]
	if settled["state"] != string(domain.SubscriptionSucceeded) || settled["operation_id"] != op.OperationId || settled["diagnostic_attached"] != false {
		t.Fatalf("unexpected settlement line: %v", settled)
	}
	if _, a := f.record(); a.Subscription.ServerOperation == nil || a.Subscription.ServerOperation.State != domain.SubscriptionSucceeded {
		t.Fatal("the logged settlement is not the persisted outcome")
	}
	// The identical request replays the receipt and writes nothing.
	if _, err := f.finishWith(lease, generation, bundle, true, false, true); err != nil {
		t.Fatal(err)
	}
	logs.requireLogged(t, "grok_subscription_operation_settled", 1)
}
