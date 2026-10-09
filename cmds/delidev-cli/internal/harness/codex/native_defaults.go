// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Capture only bounded, non-secret fields from the same config/read which
// verifies the original private API/subscription process. Never use model/list
// isDefault flags, requested thread overrides, or a separate discovery process.
func (c *Client) captureNativeDefaults(config map[string]json.RawMessage) (returned error) {
	c.nativeDefaults = nil
	defer func() {
		if c.expectedNativeDefaults != nil {
			if c.nativeDefaults == nil {
				returned = domain.NativeDefaultsUnavailable()
			} else {
				returned = c.expectedNativeDefaults.VerifyCurrent(*c.nativeDefaults, c.version, c.nativeDefaultsExecutable)
			}
		}
	}()
	if c.mode != ThreadProtocol {
		return nil
	}
	d := domain.NativeHarnessDefaults{Version: 1}
	for key, target := range map[string]**string{"model": &d.Model, "model_reasoning_effort": &d.Effort, "service_tier": &d.ServiceTier, "approval_policy": &d.ApprovalPolicy} {
		raw := config[key]
		if len(raw) == 0 || string(raw) == "null" {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil
		}
		*target = &value
	}
	if raw := config["approvals_reviewer"]; len(raw) != 0 && string(raw) != "null" {
		var v domain.ApprovalsReviewer
		if json.Unmarshal(raw, &v) != nil {
			return nil
		}
		d.ApprovalsReviewer = &v
	}
	if d.Validate() == nil {
		c.nativeDefaults = &d
	}
	return nil
}

func (c *Client) VerifyNativeDefaults(proof *domain.NativeHarnessDefaultProof, executable string) error {
	if proof == nil || c.nativeDefaults == nil {
		return domain.NativeDefaultsUnavailable()
	}
	if err := proof.VerifyCurrent(*c.nativeDefaults, c.version, executable); err != nil {
		return err
	}
	c.expectedNativeDefaults = proof
	c.nativeDefaultsExecutable = executable
	return nil
}

func (c *Client) NativeDefaults() *domain.NativeHarnessDefaults {
	if c.nativeDefaults == nil {
		return nil
	}
	copy := *c.nativeDefaults
	return &copy
}
