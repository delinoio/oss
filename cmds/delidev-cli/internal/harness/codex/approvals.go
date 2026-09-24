package codex

import (
	"encoding/json"
	"reflect"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/nativewire"
)

func (c *Client) observeApprovalLocked(native nativewire.Event) (Event, error) {
	switch native.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
	default:
		return privateNative(native), nil
	}
	var header struct {
		ThreadID    domain.ID `json:"threadId"`
		TurnID      domain.ID `json:"turnId"`
		ItemID      string    `json:"itemId"`
		StartedAtMS *int64    `json:"startedAtMs"`
	}
	if json.Unmarshal(native.Params, &header) != nil || header.ThreadID.Validate() != nil || header.TurnID.Validate() != nil || domain.Text(header.ItemID, "native approval item", 1024, true) != nil || header.StartedAtMS == nil || *header.StartedAtMS < 0 || native.Token.Validate() != nil {
		return Event{}, incompatible()
	}
	if header.ThreadID != c.thread {
		return privateNative(native), nil
	}
	id, err := decodeNativeRequestID(native.ID)
	if err != nil {
		return Event{}, err
	}
	turn, known := c.execution.turns[header.TurnID]
	if !known && c.problem == nil {
		return Event{}, incompatible()
	}
	request := &ApprovalRequest{StartedAtMS: *header.StartedAtMS}
	switch native.Method {
	case "item/commandExecution/requestApproval":
		request.Kind = CommandApproval
		request.Command, err = decodeCommandApproval(native.Params)
	case "item/fileChange/requestApproval":
		var w struct {
			ThreadID    domain.ID `json:"threadId"`
			TurnID      domain.ID `json:"turnId"`
			ItemID      string    `json:"itemId"`
			StartedAtMS int64     `json:"startedAtMs"`
			Reason      *string   `json:"reason"`
			GrantRoot   *string   `json:"grantRoot"`
		}
		if domain.Decode(native.Params, &w) != nil || !optionalApprovalText(w.Reason, domain.MaxMessageText, false) || !optionalApprovalText(w.GrantRoot, 4096, true) {
			return Event{}, incompatible()
		}
		request.Kind, request.File = FileApproval, &FileApprovalRequest{Reason: w.Reason, GrantRoot: w.GrantRoot}
	case "item/permissions/requestApproval":
		var w struct {
			ThreadID      domain.ID          `json:"threadId"`
			TurnID        domain.ID          `json:"turnId"`
			ItemID        string             `json:"itemId"`
			StartedAtMS   int64              `json:"startedAtMs"`
			EnvironmentID *string            `json:"environmentId"`
			Cwd           *string            `json:"cwd"`
			Reason        *string            `json:"reason"`
			Permissions   *PermissionProfile `json:"permissions"`
		}
		if domain.Decode(native.Params, &w) != nil || w.Cwd == nil || !optionalApprovalText(w.Cwd, 4096, true) || !optionalApprovalText(w.Reason, domain.MaxMessageText, false) || !optionalApprovalText(w.EnvironmentID, 1024, true) || w.Permissions == nil {
			return Event{}, incompatible()
		}
		request.Kind, request.Permissions = PermissionsApproval, &PermissionsApprovalRequest{EnvironmentID: w.EnvironmentID, Cwd: *w.Cwd, Reason: w.Reason, Permissions: *w.Permissions}
	default:
		return privateNative(native), nil
	}
	if err != nil {
		return Event{}, err
	}
	interaction := &Interaction{ID: native.Token, NativeID: id, Kind: ApprovalInteraction, Approval: request}
	if err := c.retainInteractionLocked(native, header.TurnID, header.ItemID, interaction, known && !turn.Turn.Status.terminal()); err != nil {
		return Event{}, err
	}
	return Event{Kind: InteractionRequestedEvent, ThreadID: c.thread, TurnID: header.TurnID, ItemID: header.ItemID, Correlated: known, Late: turn.Turn.Status.terminal(), Interaction: interaction}, nil
}

func optionalApprovalText(value *string, limit int, required bool) bool {
	return value == nil || domain.Text(*value, "native approval field", limit, required) == nil
}

func decodeCommandApproval(raw []byte) (*CommandApprovalRequest, error) {
	var w struct {
		ThreadID      domain.ID                `json:"threadId"`
		TurnID        domain.ID                `json:"turnId"`
		ItemID        string                   `json:"itemId"`
		StartedAtMS   int64                    `json:"startedAtMs"`
		Kind          json.RawMessage          `json:"kind"`
		ApprovalID    *string                  `json:"approvalId"`
		EnvironmentID *string                  `json:"environmentId"`
		Reason        *string                  `json:"reason"`
		Command       *string                  `json:"command"`
		Cwd           *string                  `json:"cwd"`
		Actions       []json.RawMessage        `json:"commandActions"`
		Network       *NetworkApprovalContext  `json:"networkApprovalContext"`
		Additional    *PermissionProfile       `json:"additionalPermissions"`
		Execpolicy    []*string                `json:"proposedExecpolicyAmendment"`
		NetworkPolicy []NetworkPolicyAmendment `json:"proposedNetworkPolicyAmendments"`
		Decisions     []ApprovalDecision       `json:"availableDecisions"`
	}
	if domain.Decode(raw, &w) != nil || !optionalApprovalText(w.ApprovalID, 1024, true) || !optionalApprovalText(w.EnvironmentID, 1024, true) || !optionalApprovalText(w.Reason, domain.MaxMessageText, false) || !optionalApprovalText(w.Command, domain.MaxMessageText, false) || !optionalApprovalText(w.Cwd, 4096, true) || len(w.Actions) > 1024 || len(w.NetworkPolicy) > 128 || len(w.Decisions) > 128 {
		return nil, incompatible()
	}
	kind := ExecuteCommandApproval
	if len(w.Kind) != 0 {
		var value *CommandApprovalKind
		if json.Unmarshal(w.Kind, &value) != nil || value == nil {
			return nil, incompatible()
		}
		kind = *value
	}
	if kind != ExecuteCommandApproval && kind != WriteStdinApproval {
		return nil, incompatible()
	}
	if kind == WriteStdinApproval && w.ApprovalID == nil {
		return nil, incompatible()
	}
	if w.Network != nil {
		if domain.Text(w.Network.Host, "native approval host", 4096, true) != nil {
			return nil, incompatible()
		}
		switch w.Network.Protocol {
		case ApprovalHTTP, ApprovalHTTPS, ApprovalSOCKSTCP, ApprovalSOCKSUDP:
		default:
			return nil, incompatible()
		}
	}
	result := &CommandApprovalRequest{Kind: kind, ApprovalID: w.ApprovalID, EnvironmentID: w.EnvironmentID, Reason: w.Reason, Command: w.Command, Cwd: w.Cwd, Network: w.Network, AdditionalPermissions: w.Additional, ProposedNetworkPolicy: w.NetworkPolicy, AvailableDecisions: w.Decisions}
	if w.Execpolicy != nil {
		result.ProposedExecpolicy = []string{}
		for _, v := range w.Execpolicy {
			if v == nil {
				return nil, incompatible()
			}
			result.ProposedExecpolicy = append(result.ProposedExecpolicy, *v)
		}
		if (ApprovalDecision{Kind: ApprovalExecpolicy, Execpolicy: result.ProposedExecpolicy}).Validate() != nil {
			return nil, incompatible()
		}
	}
	if w.Actions != nil {
		result.Actions = []CommandAction{}
	}
	for _, v := range w.Actions {
		action, err := decodeCommandAction(v)
		if err != nil {
			return nil, err
		}
		result.Actions = append(result.Actions, action)
	}
	for _, rule := range w.NetworkPolicy {
		if rule.validate() != nil {
			return nil, incompatible()
		}
	}
	for i, decision := range w.Decisions {
		if decision.Validate() != nil {
			return nil, incompatible()
		}
		for _, prior := range w.Decisions[:i] {
			if reflect.DeepEqual(prior, decision) {
				return nil, incompatible()
			}
		}
		// Offered rules must remain the exact proposed policy. A remote extra
		// descriptor cannot silently widen a selected "remember" decision.
		if decision.Kind == ApprovalExecpolicy && !reflect.DeepEqual(decision.Execpolicy, result.ProposedExecpolicy) {
			return nil, incompatible()
		}
		if decision.Kind == ApprovalNetworkPolicy {
			found := false
			for _, rule := range w.NetworkPolicy {
				found = found || reflect.DeepEqual(rule, *decision.NetworkPolicy)
			}
			if !found {
				return nil, incompatible()
			}
		}
	}
	return result, nil
}

func cloneApproval(original *ApprovalRequest) (*ApprovalRequest, error) {
	raw, err := json.Marshal(original)
	if err != nil {
		return nil, incompatible()
	}
	var cloned ApprovalRequest
	if domain.Decode(raw, &cloned) != nil {
		return nil, incompatible()
	}
	return &cloned, nil
}
