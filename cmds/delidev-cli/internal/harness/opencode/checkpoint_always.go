package opencode

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

const checkpointAlwaysBody = `{"reply":"always"}`

type checkpointAlwaysPermission struct {
	Claim SessionClaim     `json:"claim"`
	Rules []PermissionRule `json:"rules"`
}

// The verified native Build and Plan agents both advertise Read and have no
// Read-deny rule. Appending these exact Read allowances to session permission
// preserves the native last-match behavior and tool availability. Other
// permissions require their separate precedence/advertisement profile.
func (o *inputObserver) checkpointAlways(value *observedInteraction) (checkpointAlwaysPermission, bool) {
	var result checkpointAlwaysPermission
	claim, valid := o.checkpointPermission(value, PermissionAlways)
	if !valid || value.value.Permission.Name != "read" || len(value.value.Permission.Always) == 0 || len(value.value.Permission.Always) > 128 || o.parts[claim.PartID].value.Tool.Name != "read" {
		return result, false
	}
	result.Claim = claim
	for _, pattern := range value.value.Permission.Always {
		if domain.Text(pattern, "native remembered permission", 4096, true) != nil {
			return checkpointAlwaysPermission{}, false
		}
		result.Rules = append(result.Rules, PermissionRule{Permission: "read", Pattern: pattern, Action: PermissionAllow})
	}
	return result, true
}

func validCheckpointPermissionProfile(p *checkpointToolHistory) bool {
	if p.InteractionFree || len(p.Once)+len(p.Always)+len(p.Policy) > maxObservedInteractions {
		return false
	}
	if p.Version == 2 {
		return len(p.Once) != 0 && len(p.Always) == 0 && len(p.Policy) == 0 && p.AppliedAlways == 0
	}
	if (p.Version != 3 && p.Version != 4) || (p.Version == 4) != (len(p.Policy) != 0) || len(p.Always) == 0 || p.AppliedAlways > uint32(len(p.Always)) {
		return false
	}
	rules := 0
	for _, approval := range p.Always {
		if len(approval.Rules) == 0 {
			return false
		}
		for _, rule := range approval.Rules {
			if rule.Permission != "read" || rule.Action != PermissionAllow || domain.Text(rule.Pattern, "native remembered permission", 4096, true) != nil {
				return false
			}
			rules++
		}
	}
	return rules <= 128
}

func checkpointAppliedPermissions(p *checkpointToolHistory, count uint32) []PermissionRule {
	result := []PermissionRule{}
	if p == nil || count > uint32(len(p.Always)) {
		return result
	}
	for _, approval := range p.Always[:count] {
		result = append(result, approval.Rules...)
	}
	return result
}
