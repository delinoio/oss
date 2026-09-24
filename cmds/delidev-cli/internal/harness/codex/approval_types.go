package codex

import (
	"encoding/json"
	"reflect"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

type ApprovalKind string
type CommandApprovalKind string
type ApprovalDecisionKind string
type NetworkApprovalProtocol string
type NetworkPolicyAction string
type PermissionGrantScope string

const (
	CommandApproval        ApprovalKind            = "command"
	FileApproval           ApprovalKind            = "file-change"
	PermissionsApproval    ApprovalKind            = "permissions"
	ExecuteCommandApproval CommandApprovalKind     = "command"
	WriteStdinApproval     CommandApprovalKind     = "writeStdin"
	ApprovalAccept         ApprovalDecisionKind    = "accept"
	ApprovalAcceptSession  ApprovalDecisionKind    = "acceptForSession"
	ApprovalDecline        ApprovalDecisionKind    = "decline"
	ApprovalCancel         ApprovalDecisionKind    = "cancel"
	ApprovalExecpolicy     ApprovalDecisionKind    = "acceptWithExecpolicyAmendment"
	ApprovalNetworkPolicy  ApprovalDecisionKind    = "applyNetworkPolicyAmendment"
	ApprovalHTTP           NetworkApprovalProtocol = "http"
	ApprovalHTTPS          NetworkApprovalProtocol = "https"
	ApprovalSOCKSTCP       NetworkApprovalProtocol = "socks5Tcp"
	ApprovalSOCKSUDP       NetworkApprovalProtocol = "socks5Udp"
	NetworkAllow           NetworkPolicyAction     = "allow"
	NetworkDeny            NetworkPolicyAction     = "deny"
	PermissionTurn         PermissionGrantScope    = "turn"
	PermissionSession      PermissionGrantScope    = "session"
)

// All fields retain native request semantics. CommandActions are display hints,
// and requested permissions or proposed rules are never an implicit grant.
type ApprovalRequest struct {
	Kind        ApprovalKind
	StartedAtMS int64
	Command     *CommandApprovalRequest
	File        *FileApprovalRequest
	Permissions *PermissionsApprovalRequest
}

type CommandApprovalRequest struct {
	Kind                  CommandApprovalKind
	ApprovalID            *string
	EnvironmentID         *string
	Reason                *string
	Command               *string
	Cwd                   *string
	Actions               []CommandAction
	Network               *NetworkApprovalContext
	AdditionalPermissions *PermissionProfile
	ProposedExecpolicy    []string
	ProposedNetworkPolicy []NetworkPolicyAmendment
	AvailableDecisions    []ApprovalDecision
}

type FileApprovalRequest struct {
	Reason    *string
	GrantRoot *string
}

type PermissionsApprovalRequest struct {
	EnvironmentID *string
	Cwd           string
	Reason        *string
	Permissions   PermissionProfile
}

type NetworkApprovalContext struct {
	Host     string                  `json:"host"`
	Protocol NetworkApprovalProtocol `json:"protocol"`
}

type NetworkPolicyAmendment struct {
	Host   string              `json:"host"`
	Action NetworkPolicyAction `json:"action"`
}

// Decision objects retain the exact offered amendment, not a free-form shell
// prefix or host supplied by a responding client.
type ApprovalDecision struct {
	Kind          ApprovalDecisionKind
	Execpolicy    []string
	NetworkPolicy *NetworkPolicyAmendment
}

func (d ApprovalDecision) Validate() error {
	switch d.Kind {
	case ApprovalAccept, ApprovalAcceptSession, ApprovalDecline, ApprovalCancel:
		if d.Execpolicy != nil || d.NetworkPolicy != nil {
			return incompatible()
		}
	case ApprovalExecpolicy:
		if d.NetworkPolicy != nil || !validExecpolicy(d.Execpolicy) {
			return incompatible()
		}
	case ApprovalNetworkPolicy:
		if d.Execpolicy != nil || d.NetworkPolicy == nil || d.NetworkPolicy.validate() != nil {
			return incompatible()
		}
	default:
		return incompatible()
	}
	return nil
}

func (d ApprovalDecision) MarshalJSON() ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	switch d.Kind {
	case ApprovalExecpolicy:
		return json.Marshal(map[string]any{string(d.Kind): map[string]any{"execpolicy_amendment": d.Execpolicy}})
	case ApprovalNetworkPolicy:
		return json.Marshal(map[string]any{string(d.Kind): map[string]any{"network_policy_amendment": d.NetworkPolicy}})
	default:
		return json.Marshal(d.Kind)
	}
}

func (d *ApprovalDecision) UnmarshalJSON(raw []byte) error {
	var simple ApprovalDecisionKind
	if json.Unmarshal(raw, &simple) == nil {
		*d = ApprovalDecision{Kind: simple}
		return d.Validate()
	}
	var w struct {
		Execpolicy *struct {
			Prefix []*string `json:"execpolicy_amendment"`
		} `json:"acceptWithExecpolicyAmendment"`
		Network *struct {
			Amendment *NetworkPolicyAmendment `json:"network_policy_amendment"`
		} `json:"applyNetworkPolicyAmendment"`
	}
	var fields map[string]json.RawMessage
	if domain.Decode(raw, &w) != nil || domain.Decode(raw, &fields) != nil || len(fields) != 1 {
		return incompatible()
	}
	*d = ApprovalDecision{}
	if w.Execpolicy != nil {
		d.Kind = ApprovalExecpolicy
		if w.Execpolicy.Prefix != nil {
			d.Execpolicy = []string{}
		}
		for _, v := range w.Execpolicy.Prefix {
			if v == nil {
				return incompatible()
			}
			d.Execpolicy = append(d.Execpolicy, *v)
		}
	} else if w.Network != nil {
		d.Kind, d.NetworkPolicy = ApprovalNetworkPolicy, w.Network.Amendment
	} else {
		return incompatible()
	}
	return d.Validate()
}

func (n NetworkPolicyAmendment) validate() error {
	if domain.Text(n.Host, "native network rule host", 4096, true) != nil || (n.Action != NetworkAllow && n.Action != NetworkDeny) {
		return incompatible()
	}
	return nil
}

func validApprovalStrings(values []string, required bool) bool {
	if len(values) > 1024 || (required && len(values) == 0) {
		return false
	}
	for _, value := range values {
		if domain.Text(value, "native approval value", 4096, true) != nil {
			return false
		}
	}
	return true
}

func validExecpolicy(values []string) bool {
	if len(values) == 0 || len(values) > 1024 {
		return false
	}
	for i, value := range values {
		// Empty arguments after the executable retain their exact argv meaning.
		if domain.Text(value, "native execpolicy argument", 4096, i == 0) != nil {
			return false
		}
	}
	return true
}

func validateApprovalDecision(original *ApprovalRequest, decision ApprovalDecision) error {
	if original == nil || decision.Validate() != nil {
		return interactionConflict()
	}
	switch original.Kind {
	case CommandApproval:
		if original.Command == nil {
			return interactionConflict()
		}
		if original.Command.AvailableDecisions == nil {
			return domain.Fail(domain.Unsupported, "Native command approval decisions are unavailable.", "Retain the original request; do not invent approval or policy options for this profile.")
		}
		for _, offered := range original.Command.AvailableDecisions {
			if reflect.DeepEqual(offered, decision) {
				return nil
			}
		}
	case FileApproval:
		if original.File != nil && slices.Contains([]ApprovalDecisionKind{ApprovalAccept, ApprovalAcceptSession, ApprovalDecline, ApprovalCancel}, decision.Kind) {
			return nil
		}
	}
	return domain.Fail(domain.InvalidArgument, "The decision is not offered by this native approval request.", "Select an original native decision without changing its command, policy amendment, host or scope.")
}
