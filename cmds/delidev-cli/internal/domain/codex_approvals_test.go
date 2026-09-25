package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func approvalPublicationFixture() ExecutionInteractionUpdate {
	command := "printf '%s' fixture"
	return ExecutionInteractionUpdate{ID: NewID(), NativeItemID: "approval-tool", NativeRequestID: InteractionRequestID{Kind: InteractionTextID, Text: "native-approval"}, Type: NativeApprovalInteraction, Approval: &ApprovalRequest{Harness: Codex, Version: CodexProtocolVersion, Codex: &CodexApprovalRequest{Kind: CodexCommandApproval, StartedAtMS: 1, Command: &CodexCommandApprovalRequest{Kind: CodexExecuteCommandApproval, Command: &command, AvailableDecisions: []CodexApprovalDecision{{Kind: CodexApprovalAccept}, {Kind: CodexApprovalCancel}}}}}}
}

func TestApprovalPublicationValidatesExactPinnedGraph(t *testing.T) {
	for _, bad := range []string{"harness", "version", "kind", "mixed-payload", "unknown-command", "invalid-option", "mixed-decision", "new-prefix", "new-host", "duplicate-option", "invalid-action", "question-fields", "closure"} {
		t.Run(bad, func(t *testing.T) {
			u := approvalPublicationFixture()
			a, c := u.Approval.Codex, u.Approval.Codex.Command
			switch bad {
			case "harness":
				u.Approval.Harness = ClaudeCode
			case "version":
				u.Approval.Version = "next"
			case "kind":
				a.Kind = "future"
			case "mixed-payload":
				a.File = &CodexFileApprovalRequest{}
			case "unknown-command":
				c.Kind = "shell"
			case "invalid-option":
				c.AvailableDecisions[0].Kind = "bypass"
			case "mixed-decision":
				c.AvailableDecisions[0].Execpolicy = []string{"printf"}
			case "new-prefix":
				c.AvailableDecisions[0] = CodexApprovalDecision{Kind: CodexApprovalExecpolicy, Execpolicy: []string{"printf"}}
			case "new-host":
				c.AvailableDecisions[0] = CodexApprovalDecision{Kind: CodexApprovalNetworkPolicy, NetworkPolicy: &CodexNetworkPolicyAmendment{Host: "new.invalid", Action: CodexNetworkAllow}}
			case "duplicate-option":
				c.AvailableDecisions = append(c.AvailableDecisions, c.AvailableDecisions[0])
			case "invalid-action":
				c.Actions = []CommandAction{{Kind: UnknownCommandAction, Command: "printf", Query: c.Command}}
			case "question-fields":
				u.Questions = &QuestionRequest{}
			case "closure":
				u.Closure = InteractionNativeClosed
			}
			if u.Validate(ExecutionInteractionRequested) == nil {
				t.Fatal("invalid approval graph was accepted")
			}
		})
	}
	u := approvalPublicationFixture()
	if err := u.Validate(ExecutionInteractionRequested); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(u)
	for _, bad := range []string{strings.Replace(string(raw), `"started_at_ms":1`, `"started_at_ms":null`, 1), strings.Replace(string(raw), `"started_at_ms":1,`, "", 1), strings.Replace(string(raw), `"kind":"command"`, `"kind":"command","decision":"accept"`, 1)} {
		var decoded ExecutionInteractionUpdate
		if Decode([]byte(bad), &decoded) == nil && decoded.Validate(ExecutionInteractionRequested) == nil {
			t.Fatal("malformed required fields or response authority were accepted")
		}
	}
	u.Closure, u.Approval = InteractionNativeClosed, nil
	if err := u.Validate(ExecutionInteractionClosed); err != nil {
		t.Fatal("metadata-only approval closure failed", err)
	}
}

func TestApprovalPublicationRequiresPermissionPayloadAndClosedPaths(t *testing.T) {
	for _, profile := range []string{`null`, `{"file_system":{"entries":[{"access":"none","path":{"type":"path","path":"/fixture"}}]}}`, `{"file_system":{"entries":[{"access":"read","path":{"type":"path","path":"/fixture","pattern":null}}]}}`, `{"file_system":{"glob_scan_max_depth":0}}`} {
		raw := `{"kind":"permissions","started_at_ms":1,"permissions":{"cwd":"/fixture","permissions":` + profile + `}}`
		var decoded CodexApprovalRequest
		if Decode([]byte(raw), &decoded) == nil {
			t.Fatal("invalid permission payload accepted")
		}
	}
	for _, raw := range []string{`{"kind":"permissions","started_at_ms":1,"permissions":{"cwd":"/fixture"}}`, `{"kind":"permissions","started_at_ms":1,"permissions":{"permissions":{}}}`} {
		var decoded CodexApprovalRequest
		if Decode([]byte(raw), &decoded) == nil {
			t.Fatal("absent permission fields acquired defaults")
		}
	}
	var decoded CodexApprovalRequest
	if err := Decode([]byte(`{"kind":"permissions","started_at_ms":1,"permissions":{"cwd":"/fixture","permissions":{"file_system":{"entries":[{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}}}`), &decoded); err != nil {
		t.Fatal(err)
	}
}
