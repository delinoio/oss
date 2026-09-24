package codex

import (
	"encoding/json"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This memory-only prepared shape is not a native send authorization. Native
// methods revalidate it against the original retained arrival before replying.
type PreparedApprovalResponse struct {
	Decision *ApprovalDecision `json:"-"`
	Grant    *PermissionGrant  `json:"-"`
}

func PrepareApprovalResponse(original *domain.ApprovalRequest, input domain.ApprovalResponseInput) (PreparedApprovalResponse, error) {
	if err := input.Validate(original); err != nil {
		return PreparedApprovalResponse{}, err
	}
	var prepared PreparedApprovalResponse
	var response any
	if d := input.Decision; d != nil {
		decision := &ApprovalDecision{Kind: ApprovalDecisionKind(d.Kind), Execpolicy: slices.Clone(d.Execpolicy)}
		if d.NetworkPolicy != nil {
			decision.NetworkPolicy = &NetworkPolicyAmendment{Host: d.NetworkPolicy.Host, Action: NetworkPolicyAction(d.NetworkPolicy.Action)}
		}
		prepared.Decision = decision
		response = struct {
			Decision *ApprovalDecision `json:"decision"`
		}{decision}
	} else {
		g := input.Grant
		prepared.Grant = &PermissionGrant{Permissions: nativeApprovalPermissions(g.Permissions), Scope: PermissionGrantScope(g.Scope), StrictAutoReview: g.StrictAutoReview}
		response = prepared.Grant
	}
	raw, err := json.Marshal(response)
	if err != nil || len(raw) > maxAnswerBytes {
		return PreparedApprovalResponse{}, domain.Fail(domain.ResourceExhausted, "The encoded native approval response exceeds its bound.", "Select a complete native response that fits the supported transport limit.")
	}
	return prepared, nil
}

func nativeApprovalPermissions(p domain.CodexPermissionProfile) PermissionProfile {
	result := PermissionProfile{}
	if p.Network != nil {
		result.Network = &AdditionalNetworkPermissions{Enabled: p.Network.Enabled}
	}
	if f := p.FileSystem; f != nil {
		fs := &AdditionalFilePermissions{Read: slices.Clone(f.Read), Write: slices.Clone(f.Write), GlobScanMaxDepth: f.GlobScanMaxDepth}
		if f.Entries != nil {
			fs.Entries = []FilePermissionEntry{}
		}
		for _, entry := range f.Entries {
			path := FilePermissionPathValue{Kind: FilePermissionPathKind(entry.Path.Kind), Path: entry.Path.Path, Pattern: entry.Path.Pattern}
			if s := entry.Path.Special; s != nil {
				path.Special = &FilePermissionSpecialValue{Kind: FilePermissionSpecialKind(s.Kind), Path: s.Path, Subpath: s.Subpath}
			}
			fs.Entries = append(fs.Entries, FilePermissionEntry{Access: FilePermissionAccess(entry.Access), Path: path})
		}
		result.FileSystem = fs
	}
	return result
}
