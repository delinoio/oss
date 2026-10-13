package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
)

func newOpenCodeEventsFixture(t *testing.T) (*openCodeTextFixture, *OpenCodeEventPublisher) {
	t.Helper()
	f := newOpenCodeTextFixture(t)
	u, err := OpenOpenCodeUsagePublisher(f.c)
	if err != nil {
		t.Fatal(err)
	}
	c := &OpenCodeEventPublisher{api: &opencode.OwnedAPI{}, text: f.c, usage: u, seen: map[string]bool{}}
	f.user(t)
	return f, c
}

func publishOpenCodeFixtureEvent(t *testing.T, f *openCodeTextFixture, c *OpenCodeEventPublisher, o opencode.Observation) {
	t.Helper()
	if err := c.PublishObservation(context.Background(), f.observation(o)); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCodeEventsRequireEveryOriginalHistoryPart(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	publishOpenCodeFixtureEvent(t, f, c, f.assistant(false))
	publishOpenCodeFixtureEvent(t, f, c, usageStep(f, false))
	publishOpenCodeFixtureEvent(t, f, c, f.part(textPartOneID, "original final text", true))
	finish := usageStep(f, true)
	publishOpenCodeFixtureEvent(t, f, c, finish)
	final := f.assistant(true)
	final.Message.Assistant.Cost = *finish.Part.Step.Cost
	final.Message.Assistant.Usage = *finish.Part.Step.Usage
	publishOpenCodeFixtureEvent(t, f, c, final)
	history := opencode.HistoryObservation{Messages: []opencode.HistoryMessage{
		{ID: f.input.MessageID, Role: opencode.UserMessageRole, Parts: []opencode.HistoryPart{{ID: f.input.PartID, Kind: opencode.TextPartKind}}},
		{ID: textAssistantID, Role: opencode.AssistantMessageRole, Parts: []opencode.HistoryPart{{ID: usageStartID, Kind: opencode.StepStartPartKind}, {ID: textPartOneID, Kind: opencode.TextPartKind}, {ID: usageFinishID, Kind: opencode.StepFinishPartKind}}},
	}}
	if !c.completeHistory(history) {
		t.Fatal("complete original projections did not match history")
	}
	for _, kind := range []opencode.PartKind{opencode.FilePartKind, opencode.PatchPartKind, opencode.SnapshotPartKind, opencode.ToolPartKind} {
		history.Messages[1].Parts[1].Kind = kind
		if c.completeHistory(history) {
			t.Fatal("unpublished native family acquired complete-history proof")
		}
	}
	history.Messages[1].Parts[1].Kind = opencode.TextPartKind
	history.Messages[1].Parts = history.Messages[1].Parts[:2]
	if c.completeHistory(history) {
		t.Fatal("missing original finish observation acquired complete-history proof")
	}
	if _, err := c.PublishTerminal(context.Background()); err == nil {
		t.Fatal("fabricated native handle granted terminal publication")
	}
}

func TestOpenCodeEventsBlockUnimplementedFamiliesAndChangedArrivals(t *testing.T) {
	for _, name := range []string{"tool", "file", "snapshot", "interaction", "diff", "duplicate", "empty-error"} {
		t.Run(name, func(t *testing.T) {
			f, c := newOpenCodeEventsFixture(t)
			publishOpenCodeFixtureEvent(t, f, c, f.assistant(false))
			o := f.part(textPartOneID, "unhandled", true)
			switch name {
			case "tool":
				o.Part.Kind = opencode.ToolPartKind
				o.Part.Tool = &opencode.NativeToolPart{Name: "unimplemented_external"}
			case "file":
				o.Part.Kind = opencode.FilePartKind
			case "snapshot":
				o.Part.Kind = opencode.SnapshotPartKind
			case "interaction":
				o = opencode.Observation{Kind: opencode.QuestionAskedEvent}
			case "diff":
				raw, _ := json.Marshal(map[string]any{"sessionID": f.input.SessionID, "diff": []any{map[string]any{"file": "original"}}})
				o = opencode.Observation{Kind: opencode.SessionDiffEvent, Ancillary: raw}
			case "duplicate":
				o = f.observation(o)
				if err := c.PublishObservation(context.Background(), o); err != nil {
					t.Fatal(err)
				}
			case "empty-error":
				o = opencode.Observation{Kind: opencode.SessionErrorEvent}
			}
			if name != "duplicate" {
				o = f.observation(o)
			}
			if err := c.PublishObservation(context.Background(), o); err == nil {
				t.Fatal("unhandled or repeated event disappeared")
			}
			if err := c.PublishObservation(context.Background(), f.observation(f.part(textPartOneID, "later", true))); err == nil {
				t.Fatal("blocked event pipeline resumed")
			}
		})
	}
}

func TestOpenCodeTerminalClassificationDoesNotInferSuccessfulTruncationOrStop(t *testing.T) {
	for _, test := range []struct {
		finish  opencode.FinishReason
		problem opencode.NativeErrorKind
		outcome domain.ExecutionOutcome
		code    domain.Code
	}{
		{finish: opencode.FinishStop, outcome: domain.ExecutionSucceeded},
		{finish: opencode.FinishLength, outcome: domain.ExecutionFailed, code: domain.ResourceExhausted},
		{finish: opencode.FinishContentFilter, outcome: domain.ExecutionFailed, code: domain.PermissionDenied},
		{finish: opencode.FinishError, outcome: domain.ExecutionFailed, code: domain.Unavailable},
		{problem: opencode.ProviderAuthErrorKind, outcome: domain.ExecutionFailed, code: domain.Unauthenticated},
		{problem: opencode.ContextErrorKind, outcome: domain.ExecutionFailed, code: domain.ResourceExhausted},
		{problem: opencode.APIErrorKind, outcome: domain.ExecutionFailed, code: domain.Unavailable},
		{problem: opencode.AbortedErrorKind},
		{finish: opencode.FinishToolCalls},
		{finish: opencode.FinishUnknown},
		{},
	} {
		var finish *opencode.FinishReason
		var problem *opencode.NativeError
		if test.finish != "" {
			v := test.finish
			finish = &v
		}
		if test.problem != "" {
			problem = &opencode.NativeError{Kind: test.problem}
		}
		outcome, code, err := openCodeTerminalOutcome(finish, problem)
		if (err == nil) != (test.outcome != "") || outcome != test.outcome || code != test.code {
			t.Fatal("native terminal classification changed", test.finish, test.problem)
		}
	}
}

func TestOpenCodeTerminalAPIStatusClassification(t *testing.T) {
	for _, test := range []struct {
		status uint64
		code   domain.Code
	}{
		{http.StatusUnauthorized, domain.Unauthenticated},
		{http.StatusForbidden, domain.PermissionDenied},
		{http.StatusTooManyRequests, domain.ResourceExhausted},
		{http.StatusBadRequest, domain.Unavailable},
		{http.StatusInternalServerError, domain.Unavailable},
	} {
		status := test.status
		finish := opencode.FinishStop
		outcome, code, err := openCodeTerminalOutcome(&finish, &opencode.NativeError{Kind: opencode.APIErrorKind, StatusCode: &status})
		if err != nil || outcome != domain.ExecutionFailed || code != test.code {
			t.Fatalf("native API status %d lost failure precedence or classification", status)
		}
	}
}

func TestOpenCodeEventsOwnNativeFailureClassification(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	publishOpenCodeFixtureEvent(t, f, c, f.assistant(false))
	publishOpenCodeFixtureEvent(t, f, c, usageStep(f, false))
	final := f.assistant(true)
	status := uint64(http.StatusUnauthorized)
	final.Message.Assistant.Cost = "0"
	final.Message.Assistant.Error = &opencode.NativeError{Kind: opencode.APIErrorKind, StatusCode: &status, Provider: "private-provider"}
	publishOpenCodeFixtureEvent(t, f, c, final)
	status = http.StatusInternalServerError
	final.Message.Assistant.Error.Kind = opencode.UnknownErrorKind
	outcome, code, err := openCodeTerminalOutcome(c.finish, c.problem)
	if err != nil || outcome != domain.ExecutionFailed || code != domain.Unauthenticated || c.problem.Provider != "" {
		t.Fatal("caller mutation changed the original native failure classification")
	}
}

func fillOpenCodeKeys(keys map[string]bool, prefix string, count int) {
	for index := 0; index < count; index++ {
		keys[fmt.Sprintf("%s%d", prefix, index)] = true
	}
}

func fillOpenCodeDedupKeys(c *OpenCodeEventPublisher, native, snapshots int) {
	for _, seen := range []*map[string]bool{&c.seen, &c.text.seen, &c.usage.seen} {
		fillOpenCodeKeys(*seen, "evt_synthetic_", native)
	}
	c.snapshotSeen, c.text.snapshotSeen, c.usage.snapshotSeen = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, seen := range []map[string]bool{c.snapshotSeen, c.text.snapshotSeen, c.usage.snapshotSeen} {
		fillOpenCodeKeys(seen, openCodeSnapshotKeyPrefix+"synthetic:", snapshots)
	}
}

func TestOpenCodeEventsSnapshotKeysDoNotConsumeNativeEventCapacity(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	fillOpenCodeDedupKeys(c, 50000, 16000)
	publishOpenCodeFixtureEvent(t, f, c, f.assistant(false))
	publishOpenCodeFixtureEvent(t, f, c, f.part(textPartOneID, "after combined 66000 keys", false))
	if len(c.seen) != 50002 || len(c.snapshotSeen) != 16000 {
		t.Fatal("independent dedup classes were not retained separately", len(c.seen), len(c.snapshotSeen))
	}
}

func TestOpenCodeEventsNativeCapacityFailsWithoutTruncation(t *testing.T) {
	f, c := newOpenCodeEventsFixture(t)
	fillOpenCodeDedupKeys(c, maxOpenCodeEventKeys, 0)
	err := c.PublishObservation(context.Background(), f.observation(f.assistant(false)))
	if err == nil || domain.SafeError(err).Code != publicationUncertain().Code {
		t.Fatal("native capacity did not fail as publication uncertain", err)
	}
	if len(c.seen) != maxOpenCodeEventKeys || len(c.snapshotSeen) != 0 {
		t.Fatal("capacity failure truncated or grew the dedup set")
	}
}

func TestOpenCodeSnapshotKeyClassesAreIndependentlyBounded(t *testing.T) {
	events, snapshots := map[string]bool{}, map[string]bool{}
	fillOpenCodeKeys(events, "evt_synthetic_", 50000)
	fillOpenCodeKeys(snapshots, openCodeSnapshotKeyPrefix+"synthetic:", 16000)
	if openCodeKeyRejected(events, snapshots, "evt_new", false) || openCodeKeyRejected(events, snapshots, openCodeSnapshotKeyPrefix+"new", true) {
		t.Fatal("combined 66000 keys tripped an independent capacity")
	}
	if !openCodeKeyRejected(events, snapshots, "evt_synthetic_1", false) {
		t.Fatal("duplicate native event lost its rejection")
	}
	fillOpenCodeKeys(events, "evt_more_", maxOpenCodeEventKeys-len(events))
	fillOpenCodeKeys(snapshots, openCodeSnapshotKeyPrefix+"more:", maxOpenCodeSnapshotKeys-len(snapshots))
	if len(events) != 65536 || len(snapshots) != 24576 {
		t.Fatal("capacity constants changed", len(events), len(snapshots))
	}
	if !openCodeKeyRejected(events, snapshots, "evt_new", false) || !openCodeKeyRejected(events, snapshots, openCodeSnapshotKeyPrefix+"new", true) {
		t.Fatal("next new key at capacity was admitted")
	}
	idempotent, rejected := admitOpenCodeKey(events, snapshots, openCodeSnapshotKeyPrefix+"more:0", true)
	if !idempotent || rejected {
		t.Fatal("duplicate frozen snapshot was not idempotent at snapshot capacity")
	}
	idempotent, rejected = admitOpenCodeKey(events, snapshots, "evt_synthetic_1", false)
	if idempotent || !rejected {
		t.Fatal("duplicate native event lost its original rejection")
	}
	var lazy map[string]bool
	markOpenCodeKey(events, &lazy, openCodeSnapshotKeyPrefix+"lazy", true)
	if !lazy[openCodeSnapshotKeyPrefix+"lazy"] || !isOpenCodeSnapshotKey(openCodeSnapshotKeyPrefix+"lazy") || isOpenCodeSnapshotKey("evt_new") {
		t.Fatal("lazy snapshot map or key classification failed")
	}
}
