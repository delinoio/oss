// SPDX-License-Identifier: Apache-2.0
package server

import (
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func claudeSubagentPublicationFixture(t *testing.T) (*publicationFixture, []domain.SubagentObservation, uint64) {
	return claudeSubagentPublicationFixtureWithProposal(t, "{}", "{}")
}

func claudeSubagentPublicationFixtureWithProposal(t *testing.T, proposed, applied string) (*publicationFixture, []domain.SubagentObservation, uint64) {
	t.Helper()
	f := newClaudePublicationFixture(t, domain.ExecuteMode)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	sequence := uint64(2)
	u := domain.ClaudeMessageUpdate{ID: domain.NewID(), NativeID: "msg_original_agents", Model: f.input.Configuration.NativeModel, Mutation: domain.ClaudeMessageStart}
	publish := func() {
		t.Helper()
		sequence++
		e := f.event(domain.ExecutionClaudeMessageObserved, sequence)
		e.ClaudeMessage = &u
		f.publish(t, e)
	}
	publish()
	children := []domain.SubagentObservation{}
	for i, native := range []string{"original_agent_one", "original_agent_two"} {
		index, initial := uint32(i), "{}"
		ref := domain.ClaudeToolReference{ID: domain.NewID(), NativeID: native, Name: "Agent"}
		tool := domain.ClaudeToolUpdate{Reference: ref, MessageID: u.ID, NativeMessageID: u.NativeID, Index: index, Mutation: domain.ClaudeToolStart, InitialInput: &initial}
		u.Mutation, u.Index, u.Block, u.Tool = domain.ClaudeBlockStart, &index, &domain.ClaudeTextBlock{Kind: domain.ClaudeToolUse, Tool: &ref}, &tool
		publish()
		tool.Mutation, tool.InitialInput, tool.Proposal = domain.ClaudeToolProposalComplete, nil, &domain.ClaudeToolProposal{Proposed: proposed, Applied: applied}
		u.Mutation = domain.ClaudeBlockComplete
		publish()
		u.Mutation, u.Block, u.Tool = domain.ClaudeBlockStop, nil, nil
		publish()
		children = append(children, domain.SubagentObservation{ID: domain.NewID(), NativeID: "child_" + native, ParentID: string(f.thread), ParentToolID: native, Tool: &ref, Source: domain.ClaudeTaskSource, SourceID: string(domain.NewID()), Status: domain.SubagentRunning, Task: &domain.SubagentTask{}})
	}
	u.Mutation, u.Index = domain.ClaudeMessageStop, nil
	publish()
	return f, children, sequence
}
