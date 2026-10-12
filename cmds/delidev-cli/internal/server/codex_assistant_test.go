// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestNativeAssistantMetadataRetainsStartedAndCompletedOriginalFacts(t *testing.T) {
	sf := newSubscriptionFixture(t)
	clear(sf.login())
	_, err := sf.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.assistant-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, sf.input.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexAsyncMessagesV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	sf.input.NativeAsyncMessages = true
	takeSubscriptionExecutionFixture(t, sf)
	f := publicationFixtureFromAuthority(t, sf.authorityFixture)
	publisher, err := worker.OpenExecutionPublisher(publicationWorkerConfig(t, f))
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	mapper := worker.NewCodexEventPublisher(publisher)
	if err = mapper.BindThread(context.Background(), codex.ThreadResult{RequestID: f.input.ThreadRequestID, Thread: &codex.Thread{ID: f.thread}, Effective: &codex.EffectiveSettings{Model: f.input.Configuration.NativeModel, Provider: "openai", Sandbox: codex.Sandbox{Type: codex.ReadOnly}, ApprovalPolicy: codex.ApprovalOnRequest, ApprovalsReviewer: "user"}}); err != nil {
		t.Fatal(err)
	}
	if err = mapper.AcceptInput(context.Background(), codex.TurnResult{RequestID: f.input.TurnRequestID, InputID: f.input.InputID, TurnID: f.turn}); err != nil {
		t.Fatal(err)
	}
	var first, last domain.CodexAssistantMetadata
	_ = domain.Decode([]byte(`{"delivery":null,"questions":null}`), &first)
	_ = domain.Decode([]byte(`{"delivery":"async","questions":[{"title":"First","options":null},{"title":"Second","options":[]},{"title":"Choices","options":["B","A"]}]}`), &last)
	native := &codex.Message{ID: "original-assistant", Role: codex.AssistantRole, Text: "", CodexAssistant: &first}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.MessageStartedEvent, ItemID: native.ID, Message: native})
	native.Text = "Original"
	native.CodexAssistant = &last
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.MessageCompletedEvent, ItemID: native.ID, Message: native})
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range rows {
		m, e := store.Decode[domain.ExecutionMessage](r)
		if e != nil {
			t.Fatal(e)
		}
		if m.NativeID != native.ID {
			continue
		}
		found = true
		if m.CodexAssistant == nil || !domain.SameCodexAssistantMetadata(m.CodexAssistant.Started, &first) || !domain.SameCodexAssistantMetadata(m.CodexAssistant.Completed, &last) || m.Text != "Original" || m.State != domain.MessageComplete {
			t.Fatal("original lifecycle projections changed")
		}
	}
	if !found {
		t.Fatal("typed assistant message was not retained")
	}
	sr, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](sr)
	if err != nil || session.Outcome == domain.ExecutionSucceeded {
		t.Fatal("message completion became whole-turn success")
	}
}
