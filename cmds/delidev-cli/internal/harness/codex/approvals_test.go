package codex

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func commandApprovalFixture() map[string]any {
	return map[string]any{"itemId": "approval-tool", "startedAtMs": 123, "command": "printf '%s' fixture", "cwd": "/fixture", "reason": "fixture reason", "availableDecisions": []any{"accept", "cancel"}}
}

func approvalEvent(t *testing.T, c *Client, turn domain.ID, method string, fields map[string]any) nativewire.Event {
	t.Helper()
	fields["threadId"], fields["turnId"] = c.thread, turn
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return nativewire.Event{Kind: nativewire.ServerRequest, Method: method, ID: json.RawMessage(`7`), Token: domain.NewID(), Params: raw}
}

func (f *threadFixture) approvalReply(id, raw json.RawMessage) bool {
	var decision struct {
		Decision ApprovalDecision `json:"decision"`
	}
	var grant PermissionGrant
	if domain.Decode(raw, &decision) != nil && domain.Decode(raw, &grant) != nil {
		return false
	}
	if file := os.Getenv("DELIDEV_CODEX_CAPTURE"); file != "" {
		out, err := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return false
		}
		err = json.NewEncoder(out).Encode(map[string]any{"method": "approval-response", "id": id, "result": raw})
		_ = out.Close()
		if err != nil {
			return false
		}
	}
	f.notify("serverRequest/resolved", map[string]any{"threadId": f.thread["id"], "requestId": id})
	return true
}

func ownedApprovalFixture(t *testing.T, mode, method string, fields map[string]any) (*Client, string, domain.ID, Event) {
	t.Helper()
	c, capture, _, _ := boundTurnFixture(t, mode)
	turn, err := c.StartTurn(context.Background(), domain.NewID(), domain.NewID(), input(domain.ExecuteMode))
	if err != nil {
		t.Fatal(err)
	}
	fixtureSignal(t, c, "approval", map[string]any{"requestId": 7, "method": method, "params": fields})
	return c, capture, turn.TurnID, nextKind(t, c, InteractionRequestedEvent)
}

func TestNativeApprovalsPreserveExactCommandsRulesAndRepeatedCallbacks(t *testing.T) {
	c, turn := observationClient()
	fields := commandApprovalFixture()
	prefix := []string{"printf", "", "%s"}
	rule := NetworkPolicyAmendment{Host: "fixture.invalid", Action: NetworkAllow}
	fields["proposedExecpolicyAmendment"] = prefix
	fields["proposedNetworkPolicyAmendments"] = []NetworkPolicyAmendment{rule}
	fields["availableDecisions"] = []ApprovalDecision{{Kind: ApprovalAccept}, {Kind: ApprovalExecpolicy, Execpolicy: prefix}, {Kind: ApprovalNetworkPolicy, NetworkPolicy: &rule}, {Kind: ApprovalCancel}}
	fields["networkApprovalContext"] = NetworkApprovalContext{Host: rule.Host, Protocol: ApprovalHTTPS}
	fields["additionalPermissions"] = map[string]any{"network": map[string]any{"enabled": true}}
	fields["commandActions"] = []any{map[string]any{"type": "unknown", "command": "printf '%s' fixture"}}
	fields["environmentId"] = "fixture-environment"
	native := approvalEvent(t, c, turn, "item/commandExecution/requestApproval", fields)
	e, err := c.observeEventLocked(native)
	if err != nil || e.Kind != InteractionRequestedEvent || !e.Correlated || e.Late || e.Interaction.Kind != ApprovalInteraction || e.Interaction.Questions != nil || e.ItemID != "approval-tool" || e.Interaction.ID != native.Token {
		t.Fatalf("approval identity/type changed: %v", err)
	}
	a := e.Interaction.Approval
	if a.Kind != CommandApproval || a.StartedAtMS != 123 || a.Command.Kind != ExecuteCommandApproval || *a.Command.Command != fields["command"] || *a.Command.Cwd != "/fixture" || *a.Command.EnvironmentID != "fixture-environment" || a.Command.Network.Host != rule.Host || !*a.Command.AdditionalPermissions.Network.Enabled || len(a.Command.Actions) != 1 || !reflect.DeepEqual(a.Command.ProposedExecpolicy, prefix) {
		t.Fatal("native command scope or optional descriptors changed")
	}
	owned := c.execution.interactions.arrivals[native.Token]
	a.Command.ProposedExecpolicy[0] = "changed"
	a.Command.AvailableDecisions[1].Execpolicy[0] = "changed"
	a.Command.Network.Host = "changed.invalid"
	if owned.approval.Command.ProposedExecpolicy[0] != "printf" || owned.approval.Command.AvailableDecisions[1].Execpolicy[0] != "printf" || owned.approval.Command.Network.Host != rule.Host {
		t.Fatal("caller mutation replaced immutable native approval")
	}
	fields = commandApprovalFixture()
	fields["kind"], fields["approvalId"] = "writeStdin", "separate-callback"
	second := approvalEvent(t, c, turn, native.Method, fields)
	second.ID = json.RawMessage(`8`)
	e, err = c.observeEventLocked(second)
	if err != nil || e.ItemID != "approval-tool" || e.Interaction.Approval.Command.Kind != WriteStdinApproval || len(c.execution.interactions.arrivals) != 2 {
		t.Fatal("separate approval callbacks on one native item were conflated", err)
	}
	second.Token = domain.NewID()
	if _, err := c.observeEventLocked(second); err == nil {
		t.Fatal("wire request identity reuse was accepted")
	}
}

func TestNativeApprovalsRejectAmbiguousOrWidenedRequests(t *testing.T) {
	for _, bad := range []string{"missing-time", "null-time", "negative-time", "wrong-kind", "null-kind", "stdin-no-callback", "unknown-field", "unknown-turn", "missing-arrival", "duplicate-decision", "unknown-decision", "null-decision", "mixed-decision", "unproposed-prefix", "unproposed-host", "bad-network", "null-prefix-argument", "cross-kind-permissions"} {
		t.Run(bad, func(t *testing.T) {
			c, turn := observationClient()
			fields := commandApprovalFixture()
			switch bad {
			case "missing-time":
				delete(fields, "startedAtMs")
			case "null-time":
				fields["startedAtMs"] = nil
			case "negative-time":
				fields["startedAtMs"] = -1
			case "wrong-kind":
				fields["kind"] = "shell"
			case "null-kind":
				fields["kind"] = nil
			case "stdin-no-callback":
				fields["kind"] = "writeStdin"
			case "unknown-field":
				fields["bypass"] = true
			case "unknown-turn":
				turn = domain.NewID()
			case "duplicate-decision":
				fields["availableDecisions"] = []any{"accept", "accept"}
			case "unknown-decision":
				fields["availableDecisions"] = []any{"approveAll"}
			case "null-decision":
				fields["availableDecisions"] = []any{nil}
			case "mixed-decision":
				fields["availableDecisions"] = []any{map[string]any{"acceptWithExecpolicyAmendment": map[string]any{"execpolicy_amendment": []string{"printf"}}, "applyNetworkPolicyAmendment": nil}}
			case "unproposed-prefix":
				fields["availableDecisions"] = []ApprovalDecision{{Kind: ApprovalExecpolicy, Execpolicy: []string{"printf"}}}
			case "unproposed-host":
				fields["availableDecisions"] = []ApprovalDecision{{Kind: ApprovalNetworkPolicy, NetworkPolicy: &NetworkPolicyAmendment{Host: "new.invalid", Action: NetworkAllow}}}
			case "bad-network":
				fields["networkApprovalContext"] = map[string]any{"host": "fixture.invalid", "protocol": "ftp"}
			case "null-prefix-argument":
				fields["proposedExecpolicyAmendment"] = []any{"printf", nil}
			case "cross-kind-permissions":
				fields["permissions"] = map[string]any{}
			}
			native := approvalEvent(t, c, turn, "item/commandExecution/requestApproval", fields)
			if bad == "missing-arrival" {
				native.Token = ""
			}
			if _, err := c.observeEventLocked(native); err == nil || len(c.execution.interactions.arrivals) != 0 {
				t.Fatal("invalid approval consumed ownership or passed validation")
			}
		})
	}
}

func TestNativeApprovalResponseIsExactOnceAndClosureIsNotAcceptance(t *testing.T) {
	c, capture, turn, event := ownedApprovalFixture(t, "approvals", "item/commandExecution/requestApproval", commandApprovalFixture())
	id := event.Interaction.ID
	event.Interaction.Approval.Command.AvailableDecisions = append(event.Interaction.Approval.Command.AvailableDecisions, ApprovalDecision{Kind: ApprovalAcceptSession})
	_, err := c.RespondApproval(context.Background(), domain.NewID(), id, turn, ApprovalDecision{Kind: ApprovalAcceptSession})
	assertCode(t, err, domain.InvalidArgument)
	_, err = c.AnswerQuestions(context.Background(), domain.NewID(), id, turn, QuestionAnswers{})
	assertCode(t, err, domain.Conflict)
	_, err = c.GrantPermissions(context.Background(), domain.NewID(), id, turn, PermissionGrant{Scope: PermissionSession})
	assertCode(t, err, domain.Conflict)
	_, err = c.RespondApproval(context.Background(), domain.NewID(), id, domain.NewID(), ApprovalDecision{Kind: ApprovalAccept})
	assertCode(t, err, domain.Conflict)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			_, err := c.RespondApproval(context.Background(), domain.NewID(), id, turn, ApprovalDecision{Kind: ApprovalAccept})
			results <- err
		})
	}
	wg.Wait()
	sent, conflicts := 0, 0
	for range 2 {
		err := <-results
		if err == nil {
			sent++
		} else if domain.SafeError(err).Code == domain.Conflict {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if sent != 1 || conflicts != 1 {
		t.Fatal("concurrent approval was not claimed exactly once")
	}
	closed := nextKind(t, c, InteractionClosedEvent)
	status, err := c.InspectInteraction(context.Background(), id)
	if err != nil || status != *closed.InteractionState || status.Delivery != QuestionTransmitted || status.Accepted || status.Closure != InteractionNativeClosed || !c.execution.interactions.blocksInput() || c.execution.interactions.arrivals[id].approval != nil || c.execution.interactions.bytes != 0 {
		t.Fatal("native closure implied acceptance or retained request payload")
	}
	raw, err := os.ReadFile(capture)
	if err != nil || strings.Count(string(raw), `"method":"approval-response"`) != 1 || !strings.Contains(string(raw), `"result":{"decision":"accept"}`) {
		t.Fatal("native reply was replayed or did not preserve the exact offered decision", err)
	}
	// A function output sharing the item's identity is not a question answer.
	e, err := observeFixture(c, "rawResponseItem/completed", map[string]any{"threadId": c.thread, "turnId": turn, "item": map[string]any{"type": "function_call_output", "call_id": "approval-tool", "output": "not a question response"}})
	if err != nil || e.Kind == QuestionAcceptedEvent || status.Accepted {
		t.Fatal("approval output became question acceptance", err)
	}
}

func TestNativeApprovalForeignAndUnknownRequestsRemainPrivate(t *testing.T) {
	c, turn := observationClient()
	for _, method := range []string{"item/commandExecution/requestApproval", "item/future/requestApproval"} {
		event := approvalEvent(t, c, turn, method, commandApprovalFixture())
		var fields map[string]any
		_ = json.Unmarshal(event.Params, &fields)
		fields["threadId"] = domain.NewID()
		if method == "item/future/requestApproval" {
			fields = map[string]any{"future": "private"}
		}
		event.Params, _ = json.Marshal(fields)
		e, err := c.observeEventLocked(event)
		raw, _ := json.Marshal(e)
		if err != nil || e.Kind != NativeExtensionEvent || e.Interaction != nil || strings.Contains(string(raw), "printf") || strings.Contains(string(raw), "private") || len(c.execution.interactions.arrivals) != 0 {
			t.Fatal("unsupported request escaped private handling", err)
		}
	}
}

func TestNativeApprovalMissingDecisionsDoNotInventPermission(t *testing.T) {
	for _, decisions := range []any{nil, []any{}} {
		fields := commandApprovalFixture()
		fields["availableDecisions"] = decisions
		c, _, turn, event := ownedApprovalFixture(t, "approvals", "item/commandExecution/requestApproval", fields)
		_, err := c.RespondApproval(context.Background(), domain.NewID(), event.Interaction.ID, turn, ApprovalDecision{Kind: ApprovalAccept})
		want := domain.InvalidArgument
		if decisions == nil {
			want = domain.Unsupported
		}
		assertCode(t, err, want)
		if len(c.execution.interactions.responses) != 0 {
			t.Fatal("absent native choices consumed a response claim")
		}
	}
}

func TestNativeApprovalRequestsShareBoundsWithoutReplacingEvidence(t *testing.T) {
	c, turn := observationClient()
	for i := range maxOpenInteractions + 1 {
		native := approvalEvent(t, c, turn, "item/commandExecution/requestApproval", commandApprovalFixture())
		native.ID, _ = json.Marshal(i)
		_, err := c.observeEventLocked(native)
		if i < maxOpenInteractions {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			assertCode(t, err, domain.ResourceExhausted)
			if len(c.execution.interactions.arrivals) != maxOpenInteractions || c.execution.interactions.open != maxOpenInteractions {
				t.Fatal("overflow replaced retained approvals")
			}
		}
	}
}

func TestNativeApprovalStopAndNativeClosurePreventResponse(t *testing.T) {
	for _, action := range []string{"close", "turn-ended", "pause", "canceled-context"} {
		t.Run(action, func(t *testing.T) {
			c, _, turn, event := ownedApprovalFixture(t, "approvals", "item/fileChange/requestApproval", map[string]any{"itemId": "patch-tool", "startedAtMs": 1, "grantRoot": "/fixture"})
			ctx := context.Background()
			switch action {
			case "close":
				fixtureSignal(t, c, "notify", map[string]any{"method": "serverRequest/resolved", "params": map[string]any{"threadId": c.thread, "requestId": 7}})
				nextKind(t, c, InteractionClosedEvent)
			case "turn-ended":
				fixtureSignal(t, c, "finish", map[string]any{"status": TurnInterrupted})
				nextKind(t, c, TurnCompletedEvent)
			case "pause":
				c.execution.paused = true
			case "canceled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := c.RespondApproval(ctx, domain.NewID(), event.Interaction.ID, turn, ApprovalDecision{Kind: ApprovalAccept})
			if err == nil || len(c.execution.interactions.responses) != 0 {
				t.Fatal("ineligible approval was claimed or sent")
			}
		})
	}
}

func TestNativeApprovalUncertainDeliveryRetainsIdentityWithoutResend(t *testing.T) {
	fields := commandApprovalFixture()
	prefix := []string{"printf"}
	for range 50 {
		prefix = append(prefix, strings.Repeat("x", 4096))
	}
	decision := ApprovalDecision{Kind: ApprovalExecpolicy, Execpolicy: prefix}
	fields["proposedExecpolicyAmendment"], fields["availableDecisions"] = prefix, []ApprovalDecision{decision}
	c, _, turn, event := ownedApprovalFixture(t, "approvals-blocked", "item/commandExecution/requestApproval", fields)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	id := domain.NewID()
	status, err := c.RespondApproval(ctx, id, event.Interaction.ID, turn, decision)
	assertCode(t, err, domain.RecoveryRequired)
	if status.ResponseID != id || status.Delivery != QuestionDeliveryUncertain || status.Accepted || !c.execution.paused || !c.execution.interactions.blocksInput() {
		t.Fatal("uncertain approval lost response identity or pause")
	}
	_, err = c.RespondApproval(context.Background(), domain.NewID(), event.Interaction.ID, turn, decision)
	assertCode(t, err, domain.RecoveryRequired)
	retained, err := c.InspectInteraction(context.Background(), event.Interaction.ID)
	if err != nil || retained != status {
		t.Fatal("replay replaced the original uncertain response")
	}
}
