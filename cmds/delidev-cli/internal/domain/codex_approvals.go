package domain

import (
	"encoding/json"
	"reflect"
	"slices"
)

// Each harness owns its native approval meaning. This envelope deliberately
// does not translate native permissions into a synthetic common sandbox.
type ApprovalRequest struct {
	Harness Harness               `json:"harness"`
	Version string                `json:"version"`
	Codex   *CodexApprovalRequest `json:"codex"`
}

func invalidCodexApproval() error {
	return Fail(InvalidArgument, "Invalid native approval observation.", "Preserve the pinned native request kind, scope and original offered decisions without response authority.")
}

func (a ApprovalRequest) Validate() error {
	if a.Harness != Codex || a.Version != CodexProtocolVersion || a.Codex == nil {
		return invalidCodexApproval()
	}
	return a.Codex.Validate()
}

func (a *CodexApprovalRequest) UnmarshalJSON(raw []byte) error {
	type plain CodexApprovalRequest
	var decoded plain
	var fields map[string]json.RawMessage
	var start *int64
	if Decode(raw, &decoded) != nil || Decode(raw, &fields) != nil || json.Unmarshal(fields["started_at_ms"], &start) != nil || start == nil {
		return invalidCodexApproval()
	}
	*a = CodexApprovalRequest(decoded)
	return a.Validate()
}

func optionalCodexApprovalText(value *string, limit int, required bool) bool {
	return value == nil || Text(*value, "native approval field", limit, required) == nil
}

func (p *CodexPermissionsApprovalRequest) UnmarshalJSON(raw []byte) error {
	var wire struct {
		EnvironmentID *string                 `json:"environment_id"`
		Cwd           *string                 `json:"cwd"`
		Reason        *string                 `json:"reason"`
		Permissions   *CodexPermissionProfile `json:"permissions"`
	}
	if Decode(raw, &wire) != nil || wire.Cwd == nil || wire.Permissions == nil {
		return invalidCodexApproval()
	}
	*p = CodexPermissionsApprovalRequest{EnvironmentID: wire.EnvironmentID, Cwd: *wire.Cwd, Reason: wire.Reason, Permissions: *wire.Permissions}
	return nil
}

func (a CodexApprovalRequest) Validate() error {
	if a.StartedAtMS < 0 {
		return invalidCodexApproval()
	}
	switch a.Kind {
	case CodexCommandApproval:
		if a.Command == nil || a.File != nil || a.Permissions != nil {
			return invalidCodexApproval()
		}
		return a.Command.Validate()
	case CodexFileApproval:
		if a.Command != nil || a.File == nil || a.Permissions != nil || !optionalCodexApprovalText(a.File.Reason, MaxMessageText, false) || !optionalCodexApprovalText(a.File.GrantRoot, 4096, true) {
			return invalidCodexApproval()
		}
	case CodexPermissionsApproval:
		if a.Command != nil || a.File != nil || a.Permissions == nil {
			return invalidCodexApproval()
		}
		p := a.Permissions
		if Text(p.Cwd, "native approval cwd", 4096, true) != nil || !optionalCodexApprovalText(p.Reason, MaxMessageText, false) || !optionalCodexApprovalText(p.EnvironmentID, 1024, true) || p.Permissions.Validate() != nil {
			return invalidCodexApproval()
		}
	default:
		return invalidCodexApproval()
	}
	return nil
}

func (c CodexCommandApprovalRequest) Validate() error {
	if (c.Kind != CodexExecuteCommandApproval && c.Kind != CodexWriteStdinApproval) || (c.Kind == CodexWriteStdinApproval && c.ApprovalID == nil) || !optionalCodexApprovalText(c.ApprovalID, 1024, true) || !optionalCodexApprovalText(c.EnvironmentID, 1024, true) || !optionalCodexApprovalText(c.Reason, MaxMessageText, false) || !optionalCodexApprovalText(c.Command, MaxMessageText, false) || !optionalCodexApprovalText(c.Cwd, 4096, true) || len(c.Actions) > 1024 || len(c.AvailableDecisions) > 128 || len(c.ProposedNetworkPolicy) > 128 {
		return invalidCodexApproval()
	}
	if c.Network != nil && (Text(c.Network.Host, "native approval host", 4096, true) != nil || !slices.Contains([]CodexNetworkApprovalProtocol{CodexApprovalHTTP, CodexApprovalHTTPS, CodexApprovalSOCKSTCP, CodexApprovalSOCKSUDP}, c.Network.Protocol)) {
		return invalidCodexApproval()
	}
	if c.AdditionalPermissions != nil && c.AdditionalPermissions.Validate() != nil {
		return invalidCodexApproval()
	}
	if c.ProposedExecpolicy != nil && !validCodexExecpolicy(c.ProposedExecpolicy) {
		return invalidCodexApproval()
	}
	for _, a := range c.Actions {
		if a.Validate() != nil {
			return invalidCodexApproval()
		}
	}
	for _, rule := range c.ProposedNetworkPolicy {
		if rule.Validate() != nil {
			return invalidCodexApproval()
		}
	}
	for i, decision := range c.AvailableDecisions {
		if decision.Validate() != nil || slices.ContainsFunc(c.AvailableDecisions[:i], func(other CodexApprovalDecision) bool { return reflect.DeepEqual(other, decision) }) {
			return invalidCodexApproval()
		}
		if decision.Kind == CodexApprovalExecpolicy && !reflect.DeepEqual(decision.Execpolicy, c.ProposedExecpolicy) {
			return invalidCodexApproval()
		}
		if decision.Kind == CodexApprovalNetworkPolicy && !slices.ContainsFunc(c.ProposedNetworkPolicy, func(rule CodexNetworkPolicyAmendment) bool { return reflect.DeepEqual(rule, *decision.NetworkPolicy) }) {
			return invalidCodexApproval()
		}
	}
	return nil
}

func (d CodexApprovalDecision) Validate() error {
	switch d.Kind {
	case CodexApprovalAccept, CodexApprovalAcceptSession, CodexApprovalDecline, CodexApprovalCancel:
		if d.Execpolicy != nil || d.NetworkPolicy != nil {
			return invalidCodexApproval()
		}
	case CodexApprovalExecpolicy:
		if d.NetworkPolicy != nil || !validCodexExecpolicy(d.Execpolicy) {
			return invalidCodexApproval()
		}
	case CodexApprovalNetworkPolicy:
		if d.Execpolicy != nil || d.NetworkPolicy == nil || d.NetworkPolicy.Validate() != nil {
			return invalidCodexApproval()
		}
	default:
		return invalidCodexApproval()
	}
	return nil
}

func (n CodexNetworkPolicyAmendment) Validate() error {
	if Text(n.Host, "native network rule host", 4096, true) != nil || (n.Action != CodexNetworkAllow && n.Action != CodexNetworkDeny) {
		return invalidCodexApproval()
	}
	return nil
}

func validCodexApprovalStrings(values []string, required bool) bool {
	if len(values) > 1024 || (required && len(values) == 0) {
		return false
	}
	for _, value := range values {
		if Text(value, "native approval value", 4096, true) != nil {
			return false
		}
	}
	return true
}

func validCodexExecpolicy(values []string) bool {
	if len(values) == 0 || len(values) > 1024 {
		return false
	}
	for i, value := range values {
		if Text(value, "native execpolicy argument", 4096, i == 0) != nil {
			return false
		}
	}
	return true
}

type CodexApprovalKind string
type CodexCommandApprovalKind string
type CodexApprovalDecisionKind string
type CodexNetworkApprovalProtocol string
type CodexNetworkPolicyAction string
type CodexPermissionGrantScope string

const (
	CodexCommandApproval        CodexApprovalKind            = "command"
	CodexFileApproval           CodexApprovalKind            = "file-change"
	CodexPermissionsApproval    CodexApprovalKind            = "permissions"
	CodexExecuteCommandApproval CodexCommandApprovalKind     = "command"
	CodexWriteStdinApproval     CodexCommandApprovalKind     = "writeStdin"
	CodexApprovalAccept         CodexApprovalDecisionKind    = "accept"
	CodexApprovalAcceptSession  CodexApprovalDecisionKind    = "acceptForSession"
	CodexApprovalDecline        CodexApprovalDecisionKind    = "decline"
	CodexApprovalCancel         CodexApprovalDecisionKind    = "cancel"
	CodexApprovalExecpolicy     CodexApprovalDecisionKind    = "acceptWithExecpolicyAmendment"
	CodexApprovalNetworkPolicy  CodexApprovalDecisionKind    = "applyNetworkPolicyAmendment"
	CodexApprovalHTTP           CodexNetworkApprovalProtocol = "http"
	CodexApprovalHTTPS          CodexNetworkApprovalProtocol = "https"
	CodexApprovalSOCKSTCP       CodexNetworkApprovalProtocol = "socks5Tcp"
	CodexApprovalSOCKSUDP       CodexNetworkApprovalProtocol = "socks5Udp"
	CodexNetworkAllow           CodexNetworkPolicyAction     = "allow"
	CodexNetworkDeny            CodexNetworkPolicyAction     = "deny"
	CodexPermissionTurn         CodexPermissionGrantScope    = "turn"
	CodexPermissionSession      CodexPermissionGrantScope    = "session"
)

// All fields retain native request semantics. CommandActions are display hints,
// and requested permissions or proposed rules are never an implicit grant.
type CodexApprovalRequest struct {
	Kind        CodexApprovalKind                `json:"kind"`
	StartedAtMS int64                            `json:"started_at_ms"`
	Command     *CodexCommandApprovalRequest     `json:"command,omitempty"`
	File        *CodexFileApprovalRequest        `json:"file,omitempty"`
	Permissions *CodexPermissionsApprovalRequest `json:"permissions,omitempty"`
}

type CodexCommandApprovalRequest struct {
	Kind                  CodexCommandApprovalKind      `json:"kind"`
	ApprovalID            *string                       `json:"approval_id"`
	EnvironmentID         *string                       `json:"environment_id"`
	Reason                *string                       `json:"reason"`
	Command               *string                       `json:"command"`
	Cwd                   *string                       `json:"cwd"`
	Actions               []CommandAction               `json:"actions"`
	Network               *CodexNetworkApprovalContext  `json:"network"`
	AdditionalPermissions *CodexPermissionProfile       `json:"additional_permissions"`
	ProposedExecpolicy    []string                      `json:"proposed_execpolicy"`
	ProposedNetworkPolicy []CodexNetworkPolicyAmendment `json:"proposed_network_policy"`
	AvailableDecisions    []CodexApprovalDecision       `json:"available_decisions"`
}

type CodexFileApprovalRequest struct {
	Reason    *string `json:"reason"`
	GrantRoot *string `json:"grant_root"`
}

type CodexPermissionsApprovalRequest struct {
	EnvironmentID *string                `json:"environment_id"`
	Cwd           string                 `json:"cwd"`
	Reason        *string                `json:"reason"`
	Permissions   CodexPermissionProfile `json:"permissions"`
}

type CodexNetworkApprovalContext struct {
	Host     string                       `json:"host"`
	Protocol CodexNetworkApprovalProtocol `json:"protocol"`
}

type CodexNetworkPolicyAmendment struct {
	Host   string                   `json:"host"`
	Action CodexNetworkPolicyAction `json:"action"`
}

// Decision objects retain the exact offered amendment, not a free-form shell
// prefix or host supplied by a responding client.
type CodexApprovalDecision struct {
	Kind          CodexApprovalDecisionKind    `json:"kind"`
	Execpolicy    []string                     `json:"execpolicy"`
	NetworkPolicy *CodexNetworkPolicyAmendment `json:"network_policy"`
}
