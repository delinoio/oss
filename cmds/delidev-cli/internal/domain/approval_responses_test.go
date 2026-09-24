package domain

import (
	"encoding/json"
	"testing"
)

func TestApprovalResponseSelectsOnlyOriginalNativeDecision(t *testing.T) {
	original := approvalPublicationFixture().Approval
	command := original.Codex.Command
	command.ProposedExecpolicy = []string{"printf", ""}
	command.ProposedNetworkPolicy = []CodexNetworkPolicyAmendment{{Host: "fixture.invalid", Action: CodexNetworkAllow}}
	command.AvailableDecisions = append(command.AvailableDecisions,
		CodexApprovalDecision{Kind: CodexApprovalExecpolicy, Execpolicy: command.ProposedExecpolicy},
		CodexApprovalDecision{Kind: CodexApprovalNetworkPolicy, NetworkPolicy: &command.ProposedNetworkPolicy[0]})
	for _, decision := range command.AvailableDecisions {
		if err := (ApprovalResponseInput{Decision: &decision}).Validate(original); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{}`, `{"decision":null}`, `{"decision":"accept"}`, `{"decision":{"kind":"decline"}}`, `{"decision":{"kind":"acceptForSession"}}`, `{"decision":{"kind":"accept","execpolicy":["printf"]}}`, `{"decision":{"kind":"acceptWithExecpolicyAmendment","execpolicy":["printf"]}}`, `{"decision":{"kind":"applyNetworkPolicyAmendment","network_policy":{"host":"other.invalid","action":"allow"}}}`, `{"decision":{"kind":"accept"},"grant":{"permissions":{},"scope":"turn"}}`, `{"decision":{"kind":"accept"},"answers":{}}`} {
		t.Run(raw, func(t *testing.T) {
			var input ApprovalResponseInput
			if Decode([]byte(raw), &input) == nil && input.Validate(original) == nil {
				t.Fatal("unoffered/mixed response accepted")
			}
		})
	}
	command.AvailableDecisions = nil
	if err := (ApprovalResponseInput{Decision: &CodexApprovalDecision{Kind: CodexApprovalAccept}}).Validate(original); SafeError(err).Code != Unsupported {
		t.Fatal("missing choices acquired implicit authority", err)
	}
	original.Codex.Kind, original.Codex.Command, original.Codex.File = CodexFileApproval, nil, &CodexFileApprovalRequest{}
	for _, kind := range []CodexApprovalDecisionKind{CodexApprovalAccept, CodexApprovalAcceptSession, CodexApprovalDecline, CodexApprovalCancel} {
		if err := (ApprovalResponseInput{Decision: &CodexApprovalDecision{Kind: kind}}).Validate(original); err != nil {
			t.Fatal(err)
		}
	}
	if err := (ApprovalResponseInput{Decision: &CodexApprovalDecision{Kind: CodexApprovalExecpolicy, Execpolicy: []string{"printf"}}}).Validate(original); err == nil {
		t.Fatal("file approval installed command policy")
	}
}

func TestApprovalPermissionGrantPreservesRequestedRestrictions(t *testing.T) {
	requestJSON := `{"cwd":"/fixture","permissions":{"network":{"enabled":true},"file_system":{"read":["/fixture/read"],"write":["/fixture/write"],"glob_scan_max_depth":2,"entries":[{"access":"write","path":{"type":"glob_pattern","pattern":"/fixture/**"}},{"access":"deny","path":{"type":"glob_pattern","pattern":"**/.env"}}]}}}`
	var request CodexPermissionsApprovalRequest
	if err := Decode([]byte(requestJSON), &request); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"same", "reduced", "empty", "drop-deny", "new-read", "new-write", "normalized-path", "new-rule", "new-depth", "session-strict", "missing-scope", "invalid-scope"} {
		t.Run(change, func(t *testing.T) {
			var copied CodexPermissionsApprovalRequest
			if err := Decode([]byte(requestJSON), &copied); err != nil {
				t.Fatal(err)
			}
			grant := CodexPermissionGrant{Permissions: copied.Permissions, Scope: CodexPermissionTurn}
			switch change {
			case "reduced":
				grant.Permissions.FileSystem.Read = append(grant.Permissions.FileSystem.Read, grant.Permissions.FileSystem.Write...)
				grant.Permissions.FileSystem.Write = nil
				grant.Permissions.FileSystem.Entries[0].Access = CodexFilePermissionRead
				no := false
				grant.Permissions.Network.Enabled = &no
			case "empty":
				grant.Permissions = CodexPermissionProfile{}
			case "drop-deny":
				grant.Permissions.FileSystem.Entries = grant.Permissions.FileSystem.Entries[:1]
			case "new-read":
				path := "/other"
				grant.Permissions.FileSystem.Entries = append(grant.Permissions.FileSystem.Entries, CodexFilePermissionEntry{Access: CodexFilePermissionRead, Path: CodexFilePermissionPathValue{Kind: CodexFilePermissionPath, Path: &path}})
			case "new-write":
				path := "/fixture/read"
				grant.Permissions.FileSystem.Entries = append(grant.Permissions.FileSystem.Entries, CodexFilePermissionEntry{Access: CodexFilePermissionWrite, Path: CodexFilePermissionPathValue{Kind: CodexFilePermissionPath, Path: &path}})
			case "normalized-path":
				path := "/fixture/other/../write"
				grant.Permissions.FileSystem.Entries[0].Path = CodexFilePermissionPathValue{Kind: CodexFilePermissionPath, Path: &path}
			case "new-rule":
				*grant.Permissions.FileSystem.Entries[0].Path.Pattern = "/**"
			case "new-depth":
				*grant.Permissions.FileSystem.GlobScanMaxDepth = 3
			case "session-strict":
				yes := true
				grant.StrictAutoReview = &yes
				grant.Scope = CodexPermissionSession
			case "missing-scope":
				grant.Scope = ""
			case "invalid-scope":
				grant.Scope = "forever"
			}
			err := grant.Validate(&request)
			if (err == nil) != (change == "same" || change == "reduced" || change == "empty") {
				t.Fatalf("unexpected grant validation: %v", err)
			}
		})
	}
	yes := true
	if (CodexPermissionGrant{Permissions: CodexPermissionProfile{Network: &CodexAdditionalNetworkPermissions{Enabled: &yes}}, Scope: CodexPermissionTurn}).Validate(&CodexPermissionsApprovalRequest{Cwd: "/fixture"}) == nil {
		t.Fatal("unrequested network granted")
	}
	for _, raw := range []string{`{}`, `{"permissions":{},"scope":null}`, `{"permissions":null,"scope":"turn"}`, `{"permissions":{},"scope":"turn","scope":"session"}`, `{"permissions":{},"scope":"turn","unknown":true}`} {
		var grant CodexPermissionGrant
		if Decode([]byte(raw), &grant) == nil {
			t.Fatal("malformed explicit grant accepted", raw)
		}
	}
	// Retaining the original request must not be affected by grant validation.
	raw, _ := json.Marshal(request)
	var roundtrip CodexPermissionsApprovalRequest
	if Decode(raw, &roundtrip) != nil || roundtrip.Permissions.FileSystem.Entries[0].Access != CodexFilePermissionWrite {
		t.Fatal("grant validation changed original request")
	}
}

func TestApprovalPermissionEntriesOverrideDeprecatedMirrors(t *testing.T) {
	for _, entries := range []string{`[]`, `[{"access":"write","path":{"type":"path","path":"/effective"}}]`} {
		var request CodexPermissionsApprovalRequest
		if err := Decode([]byte(`{"cwd":"/fixture","permissions":{"file_system":{"write":["/ignored"],"entries":`+entries+`}}}`), &request); err != nil {
			t.Fatal(err)
		}
		before, _ := json.Marshal(request)
		for _, test := range []struct {
			profile string
			allowed bool
		}{
			{`{"file_system":{"write":["/ignored"]}}`, false},
			{`{"file_system":{"read":["/effective"]}}`, entries != `[]`},
			{`{"file_system":{"write":["/ignored"],"entries":[]}}`, true},
			{`{"file_system":{"entries":` + entries + `}}`, true},
		} {
			var profile CodexPermissionProfile
			if err := Decode([]byte(test.profile), &profile); err != nil {
				t.Fatal(err)
			}
			grant := CodexPermissionGrant{Scope: CodexPermissionTurn, Permissions: profile}
			if (grant.Validate(&request) == nil) != test.allowed {
				t.Fatal("public entries precedence changed native grant authority")
			}
		}
		after, _ := json.Marshal(request)
		if string(before) != string(after) {
			t.Fatal("validation rewrote original native request")
		}
	}
}
