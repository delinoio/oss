// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func childMessages(t *testing.T, f *continuationFixture, child *pb.Resource) []domain.ExecutionMessage {
	t.Helper()
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(child.Id), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	messages := []domain.ExecutionMessage{}
	for _, row := range rows {
		value, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, value)
	}
	return messages
}

func forkJobCount(t *testing.T, f *continuationFixture) int {
	t.Helper()
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.JobKind, SessionID: domain.ID(f.refresh(t).ID), Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		job, err := store.Decode[domain.Job](row)
		if err == nil && job.Type == domain.ForkSessionJob {
			count++
		}
	}
	return count
}

func TestOpenCodeForkWithoutReasoningKeepsProfileV1(t *testing.T) {
	// Even a Worker that also negotiated reasoning keeps v1 for a plain source.
	f := newOpenCodeForkSource(t, openCodeForkFixtureOptions{reasoningCapability: true})
	accepted, err := forkOpenCodeSource(t, f)
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	if input.OpenCode == nil || input.OpenCode.Profile != domain.OpenCodeForkProfileV1 {
		t.Fatal("plain source did not keep profile v1")
	}
	var envelope domain.Job
	_ = domain.Decode(job.DocumentJson, &envelope)
	if strings.Contains(string(envelope.Input), `"profile"`) {
		t.Fatal("profile v1 job bytes changed")
	}
	child := publishOpenCodeForkResult(t, f, job, input)
	var session domain.Session
	_ = domain.Decode(child.DocumentJson, &session)
	if session.Fork.OpenCodeProfile != domain.OpenCodeForkProfileV1 || strings.Contains(string(child.DocumentJson), "opencode_profile") {
		t.Fatal("profile v1 child seed changed")
	}
	for _, message := range childMessages(t, f, child) {
		if message.Role == domain.ArtifactMessage {
			t.Fatal("plain source inherited an artifact")
		}
	}
}

func TestOpenCodeForkReasoningSelectsProfileV2AndInheritsReasoning(t *testing.T) {
	f := newOpenCodeForkSource(t, openCodeForkFixtureOptions{reasoning: true, reasoningCapability: true})
	accepted, err := forkOpenCodeSource(t, f)
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	if input.Version != 2 || input.OpenCode == nil || input.OpenCode.Profile != domain.OpenCodeForkProfileV2 {
		t.Fatal("reasoning source did not select profile v2")
	}
	child := publishOpenCodeForkResult(t, f, job, input)
	var session domain.Session
	_ = domain.Decode(child.DocumentJson, &session)
	if session.Fork.OpenCodeProfile != domain.OpenCodeForkProfileV2 || session.Fork.Validate() != nil {
		t.Fatal("child seed lost profile v2")
	}
	var reasoning *domain.ExecutionMessage
	for _, message := range childMessages(t, f, child) {
		if message.Role == domain.ArtifactMessage {
			value := message
			reasoning = &value
		}
	}
	if reasoning == nil || reasoning.Inherited == nil || reasoning.Inherited.SessionID != input.SourceSessionID || reasoning.NativeID != openCodeForkFixtureReasoningChildPart || reasoning.Artifact == nil || reasoning.Artifact.Completed == nil || reasoning.Artifact.Completed.Text != "Private reasoning." {
		t.Fatalf("reasoning was not re-parented with Inherited provenance: %#v", reasoning)
	}
	if reasoning.NativeParentID == openCodeForkFixtureAssistantMessage || reasoning.NativeThreadID == string(input.Completion.NativeThreadID) || reasoning.ExecutionID != input.RuntimeID || reasoning.InputID != "" {
		t.Fatal("reasoning kept source identity")
	}
}

func TestOpenCodeForkReasoningRejectedBeforeAnyJobWithoutNewCapability(t *testing.T) {
	f := newOpenCodeForkSource(t, openCodeForkFixtureOptions{reasoning: true})
	before := forkJobCount(t, f)
	response, err := forkOpenCodeSource(t, f)
	if response != nil || err == nil || connect.CodeOf(err) == connect.CodeUnknown {
		t.Fatal("v1-only Worker accepted a reasoning source", err)
	}
	if forkJobCount(t, f) != before {
		t.Fatal("rejected reasoning source created a Fork job")
	}
}

func TestOpenCodeForkRejectsEveryOtherArtifactFamily(t *testing.T) {
	for name, edit := range map[string]func(*domain.ExecutionMessage){
		"revision": func(m *domain.ExecutionMessage) {
			m.Artifact.Started.Kind = domain.OpenCodeRevisionArtifact
			m.Artifact.Completed.Kind = domain.OpenCodeRevisionArtifact
		},
		"plan": func(m *domain.ExecutionMessage) {
			m.Artifact.Started.Kind = domain.PlanArtifact
			m.Artifact.Completed.Kind = domain.PlanArtifact
		},
		"incomplete":   func(m *domain.ExecutionMessage) { m.Artifact.Completed = nil },
		"streaming":    func(m *domain.ExecutionMessage) { m.State = domain.MessageStreaming },
		"summary":      func(m *domain.ExecutionMessage) { m.Artifact.Completed.Summary = []string{"x"} },
		"tool":         func(m *domain.ExecutionMessage) { m.Tool = &domain.ExecutionTool{} },
		"progress":     func(m *domain.ExecutionMessage) { m.Progress = &domain.NativeProgress{} },
		"phase":        func(m *domain.ExecutionMessage) { p := domain.MessagePhase("x"); m.Phase = &p },
		"input":        func(m *domain.ExecutionMessage) { m.InputID = domain.NewID() },
		"turn-parent":  func(m *domain.ExecutionMessage) { m.NativeParentID = m.NativeTurnID },
		"wrong-parent": func(m *domain.ExecutionMessage) { m.NativeParentID = "not-a-message" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newOpenCodeForkSource(t, openCodeForkFixtureOptions{reasoning: true, reasoningCapability: true, artifact: edit})
			before := forkJobCount(t, f)
			response, err := forkOpenCodeSource(t, f)
			if err == nil || response != nil {
				t.Fatal("ineligible artifact record admitted a Fork")
			}
			if forkJobCount(t, f) != before {
				t.Fatal("ineligible artifact record created a Fork job")
			}
		})
	}
}

func TestOpenCodeForkReasoningPublicationRequiresMappedPart(t *testing.T) {
	f := newOpenCodeForkSource(t, openCodeForkFixtureOptions{reasoning: true, reasoningCapability: true})
	accepted, err := forkOpenCodeSource(t, f)
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	// The Worker identity map omits the reasoning part: no child may publish.
	if published := publishOpenCodeForkResultProfile(t, f, job, input, true, false); published != nil {
		t.Fatal("unmapped reasoning part published a child")
	}
}

func TestOpenCodeForkReasoningProfileSeedRoundTrips(t *testing.T) {
	requests := domain.OpenCodeForkRequests{Restore: domain.NewID(), Fork: domain.NewID(), Move: domain.NewID(), Mark: domain.NewID(), DeleteSource: domain.NewID()}
	v1, _ := json.Marshal(requests)
	if strings.Contains(string(v1), "profile") || requests.Validate() != nil {
		t.Fatal("profile v1 requests changed")
	}
	requests.Profile = domain.OpenCodeForkProfileV2
	v2, _ := json.Marshal(requests)
	var decoded domain.OpenCodeForkRequests
	if !strings.Contains(string(v2), `"profile":"opencode-general-chat-v2"`) || domain.Decode(v2, &decoded) != nil || decoded != requests || decoded.Validate() != nil {
		t.Fatal("profile v2 requests do not round trip")
	}
	requests.Profile = "opencode-general-chat-v3"
	if requests.Validate() == nil {
		t.Fatal("unknown profile accepted")
	}
}

func TestOpenCodeForkReasoningChildNeedsReasoningCapabilityToResume(t *testing.T) {
	f := newOpenCodeForkSource(t, openCodeForkFixtureOptions{reasoning: true, reasoningCapability: true})
	accepted, err := forkOpenCodeSource(t, f)
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	child := publishOpenCodeForkResult(t, f, job, input)
	f.change.Session = child
	f.enqueue(t, "First reasoning child input.", domain.ExecuteMode)
	// The Worker reconnects without the independent capability; the retained
	// reasoning history must not be handed to a peer that cannot verify it.
	_, err = f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.remove-reasoning-capability", nil, func(tx *store.Tx) (any, error) {
		r, machine, err := activeMachine(tx, domain.ID(f.machine.Id))
		if err != nil {
			return nil, err
		}
		machine.WorkerCapabilities = slices.DeleteFunc(machine.WorkerCapabilities, func(capability domain.WorkerCapability) bool {
			return capability == domain.OpenCodeGeneralChatForkReasoningV1
		})
		return tx.Put(r.Kind, r.ID, r.Revision, r.SessionID, r.ProjectID, machine)
	})
	if err != nil {
		t.Fatal(err)
	}
	row := f.refresh(t)
	_, err = sessionClient(f.accountFixture).ControlSession(context.Background(), ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err == nil {
		after := f.refresh(t)
		session, decodeErr := store.Decode[domain.Session](after)
		if decodeErr != nil || session.ActiveExecutionID != "" || session.Dispatch == domain.DispatchClaimed {
			t.Fatal("child without the reasoning capability was dispatched")
		}
	}
}
