package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"testing"
)

func TestClaudeUsagePublicationRetainsOriginalScopeWithoutBilling(t *testing.T) {
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	message := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_original", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	e := f.event(domain.ExecutionClaudeMessageObserved, 3)
	e.ClaudeMessage = &message
	f.publish(t, e)
	count := domain.ClaudeUsageCount("9007199254740993")
	value := domain.ClaudeUsageObservation{Source: domain.ClaudeMessageStartUsage, NativeEventID: string(domain.NewID()), MessageID: message.ID, NativeMessageID: message.NativeID, Model: message.Model, Provider: &domain.ClaudeProviderUsage{Input: &count}}
	e = f.event(domain.ExecutionClaudeUsageObserved, 4)
	e.ObservationID = domain.NewID()
	e.ClaudeUsage = &value
	for _, bad := range []string{"message", "model", "mixed", "uncompleted", "provider-id"} {
		wrong := value
		switch bad {
		case "message":
			wrong.MessageID = domain.NewID()
		case "model":
			wrong.Model = "foreign"
		case "mixed":
			wrong.Result = &domain.ClaudeResultUsage{}
		case "uncompleted":
			i := uint32(0)
			wrong.Source, wrong.Index = domain.ClaudeBlockCompleteUsage, &i
		case "provider-id":
			wrong.NativeMessageID = "other"
		}
		rejected := e
		rejected.ClaudeUsage = &wrong
		if _, err := f.call(f.requestEvent(t, rejected)); err == nil {
			t.Fatal("foreign usage accepted", bad)
		}
	}
	request := f.publish(t, e)
	if response, err := f.call(request); err != nil || !response.Msg.Replayed {
		t.Fatal("original usage receipt not replayed", err)
	}
	e.Sequence = 5
	e.ObservationID = domain.NewID()
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("same original envelope acquired another record")
	}
	next := f.event(domain.ExecutionClaudeMessageObserved, 5)
	message.Mutation = domain.ClaudeMessageStop
	next.ClaudeMessage = &message
	f.publish(t, next)
	cost := domain.ClaudeNativeUSD("0.0006994999999999999")
	value = domain.ClaudeUsageObservation{Source: domain.ClaudeInputResultUsage, NativeEventID: string(domain.NewID()), Result: &domain.ClaudeResultUsage{MainLoop: &domain.ClaudeProviderUsage{Input: &count}, NativeCostUSD: &cost}}
	e.Sequence = 6
	e.ClaudeUsage = &value
	f.publish(t, e)
	e.Sequence = 7
	e.ObservationID = domain.NewID()
	value.NativeEventID = string(domain.NewID())
	if _, err := f.call(f.requestEvent(t, e)); err == nil {
		t.Fatal("a new envelope duplicated the original input result")
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 2 {
		t.Fatal("overlapping usage was merged or duplicated", err)
	}
	for _, row := range rows {
		u, err := store.Decode[domain.ClaudeUsageRecord](row)
		if err != nil || u.Usage.Validate() != nil || u.Harness != domain.ClaudeCode || u.ExecutionID != f.input.ExecutionID || u.AccountID != f.input.AccountID || u.ConnectionID != f.input.ConnectionID {
			t.Fatal("native usage attribution changed", err)
		}
		if _, err := f.service.Store.ResponseUsage(context.Background(), row.ID); err == nil {
			t.Fatal("native usage entered billing ledger")
		}
	}
	r, _ := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Execution.LastSequence != 6 || s.Execution.LatestResponseUsageID != "" || s.Outcome != domain.ExecutionRunning {
		t.Fatal("usage changed independent accounting or terminal state", err)
	}
}
