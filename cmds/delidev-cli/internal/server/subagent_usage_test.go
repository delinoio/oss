// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

func codexSubagentUsage() *domain.SubagentUsage {
	total, input, output := "100", "40", "60"
	return &domain.SubagentUsage{Scope: domain.SubagentCumulativeUsage, Total: &total, Input: &input, Output: &output, NativeReport: `{"total":{"inputTokens":40,"cachedInputTokens":10,"cacheWriteInputTokens":null,"outputTokens":60,"reasoningOutputTokens":10,"totalTokens":100},"last":{"inputTokens":40,"cachedInputTokens":10,"cacheWriteInputTokens":null,"outputTokens":60,"reasoningOutputTokens":10,"totalTokens":100},"modelContextWindow":null}`}
}

func TestSubagentMalformedUsageRejectsWholePublicationBeforeRetention(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	first := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "original-first", Usage: codexSubagentUsage()}
	second := first
	second.ID, second.NativeID, second.SourceID = domain.NewID(), string(domain.NewID()), "original-second"
	for _, scenario := range []string{"arbitrary report", "nested diagnostic", "counter mismatch", "wrong source", "missing report"} {
		t.Run(scenario, func(t *testing.T) {
			second.Usage = codexSubagentUsage()
			second.Source = domain.CodexHistorySource
			switch scenario {
			case "arbitrary report":
				second.Usage.NativeReport = `{"private":"credential-bearing diagnostic"}`
			case "nested diagnostic":
				second.Usage.NativeReport = strings.Replace(second.Usage.NativeReport, `"inputTokens":40`, `"inputTokens":40,"private":"diagnostic"`, 1)
			case "counter mismatch":
				wrong := "101"
				second.Usage.Total = &wrong
			case "wrong source":
				second.Source = domain.CodexActivitySource
			case "missing report":
				second.Usage.NativeReport = ""
			}
			event := f.event(domain.ExecutionSubagentObserved, 3)
			event.Subagents = []domain.SubagentObservation{first, second}
			if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("unvalidated usage did not return an ownership conflict", err)
			}
			for _, child := range event.Subagents {
				if _, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID); domain.SafeError(err).Code != domain.NotFound {
					t.Fatal("rejected usage partially retained a child", err)
				}
			}
			record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](record)
			if err != nil || session.Execution.LastSequence != 2 || len(session.Execution.Subagents) != 0 {
				t.Fatal("rejected usage advanced original execution ownership", err)
			}
		})
	}
	second.Source, second.Usage = domain.CodexHistorySource, codexSubagentUsage()
	event := f.event(domain.ExecutionSubagentObserved, 3)
	event.Subagents = []domain.SubagentObservation{first, second}
	original := f.publish(t, event)
	if reply, err := f.call(original); err != nil || !reply.Msg.Replayed {
		t.Fatal("valid original usage receipt failed exact replay", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.SubagentKind, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.Decode[domain.SubagentRecord](row)
	if err != nil || len(child.Sources) != 1 || child.Sources[0].Usage.NativeReport != second.Usage.NativeReport {
		t.Fatal("validated native usage bytes changed or replay duplicated coverage", err)
	}
}
