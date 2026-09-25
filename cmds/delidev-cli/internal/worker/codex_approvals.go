package worker

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

func codexApprovalRequest(native *codex.ApprovalRequest) (*domain.ApprovalRequest, error) {
	if native == nil {
		return nil, publicationUncertain()
	}
	a := &domain.CodexApprovalRequest{Kind: domain.CodexApprovalKind(native.Kind), StartedAtMS: native.StartedAtMS}
	if n := native.Command; n != nil {
		c := &domain.CodexCommandApprovalRequest{Kind: domain.CodexCommandApprovalKind(n.Kind), ApprovalID: n.ApprovalID, EnvironmentID: n.EnvironmentID, Reason: n.Reason, Command: n.Command, Cwd: n.Cwd, ProposedExecpolicy: slices.Clone(n.ProposedExecpolicy), AdditionalPermissions: codexApprovalPermissions(n.AdditionalPermissions)}
		if n.Network != nil {
			c.Network = &domain.CodexNetworkApprovalContext{Host: n.Network.Host, Protocol: domain.CodexNetworkApprovalProtocol(n.Network.Protocol)}
		}
		if n.Actions != nil {
			c.Actions = []domain.CommandAction{}
		}
		for _, action := range n.Actions {
			kind := map[codex.CommandActionKind]domain.CommandActionKind{codex.ReadCommandAction: domain.ReadCommandAction, codex.ListCommandAction: domain.ListCommandAction, codex.SearchCommandAction: domain.SearchCommandAction, codex.UnknownCommandAction: domain.UnknownCommandAction}[action.Kind]
			c.Actions = append(c.Actions, domain.CommandAction{Kind: kind, Command: action.Command, Name: action.Name, Path: action.Path, Query: action.Query})
		}
		if n.ProposedNetworkPolicy != nil {
			c.ProposedNetworkPolicy = []domain.CodexNetworkPolicyAmendment{}
		}
		for _, rule := range n.ProposedNetworkPolicy {
			c.ProposedNetworkPolicy = append(c.ProposedNetworkPolicy, domain.CodexNetworkPolicyAmendment{Host: rule.Host, Action: domain.CodexNetworkPolicyAction(rule.Action)})
		}
		if n.AvailableDecisions != nil {
			c.AvailableDecisions = []domain.CodexApprovalDecision{}
		}
		for _, decision := range n.AvailableDecisions {
			d := domain.CodexApprovalDecision{Kind: domain.CodexApprovalDecisionKind(decision.Kind), Execpolicy: slices.Clone(decision.Execpolicy)}
			if decision.NetworkPolicy != nil {
				d.NetworkPolicy = &domain.CodexNetworkPolicyAmendment{Host: decision.NetworkPolicy.Host, Action: domain.CodexNetworkPolicyAction(decision.NetworkPolicy.Action)}
			}
			c.AvailableDecisions = append(c.AvailableDecisions, d)
		}
		a.Command = c
	}
	if n := native.File; n != nil {
		a.File = &domain.CodexFileApprovalRequest{Reason: n.Reason, GrantRoot: n.GrantRoot}
	}
	if n := native.Permissions; n != nil {
		a.Permissions = &domain.CodexPermissionsApprovalRequest{EnvironmentID: n.EnvironmentID, Cwd: n.Cwd, Reason: n.Reason, Permissions: *codexApprovalPermissions(&n.Permissions)}
	}
	result := &domain.ApprovalRequest{Harness: domain.Codex, Version: codex.SupportedVersion, Codex: a}
	if err := result.Validate(); err != nil {
		return nil, err
	}
	// The public outbox owns a deep copy, never mutable pointers retained by a
	// native caller. Its tagged DTO deliberately differs from native wire JSON.
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, publicationUncertain()
	}
	var owned domain.ApprovalRequest
	if err := domain.Decode(raw, &owned); err != nil {
		return nil, err
	}
	return &owned, nil
}

func codexApprovalPermissions(native *codex.PermissionProfile) *domain.CodexPermissionProfile {
	if native == nil {
		return nil
	}
	p := &domain.CodexPermissionProfile{}
	if native.Network != nil {
		p.Network = &domain.CodexAdditionalNetworkPermissions{Enabled: native.Network.Enabled}
	}
	if n := native.FileSystem; n != nil {
		f := &domain.CodexAdditionalFilePermissions{Read: slices.Clone(n.Read), Write: slices.Clone(n.Write), GlobScanMaxDepth: n.GlobScanMaxDepth}
		if n.Entries != nil {
			f.Entries = []domain.CodexFilePermissionEntry{}
		}
		for _, e := range n.Entries {
			path := domain.CodexFilePermissionPathValue{Kind: domain.CodexFilePermissionPathKind(e.Path.Kind), Path: e.Path.Path, Pattern: e.Path.Pattern}
			if e.Path.Special != nil {
				path.Special = &domain.CodexFilePermissionSpecialValue{Kind: domain.CodexFilePermissionSpecialKind(e.Path.Special.Kind), Path: e.Path.Special.Path, Subpath: e.Path.Special.Subpath}
			}
			f.Entries = append(f.Entries, domain.CodexFilePermissionEntry{Access: domain.CodexFilePermissionAccess(e.Access), Path: path})
		}
		p.FileSystem = f
	}
	return p
}
