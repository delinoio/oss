// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Original identities survive restart independently of disposable preview bytes.
// Inspecting an undelivered request fences a delayed apply under that identity.
type NativeConfigurationOriginal struct {
	Actor domain.Principal `json:"actor"`
	JobID domain.ID        `json:"job_id,omitempty"`
}

func (t *Tx) NativeConfigurationOriginal(id domain.ID) (NativeConfigurationOriginal, bool, error) {
	var value NativeConfigurationOriginal
	if id.Validate() != nil {
		return value, false, domain.NativeConfigurationInvalid()
	}
	found, err := t.mcpMetadata("native-import-original-v1:"+string(id), &value)
	return value, found, err
}
func (t *Tx) PutNativeConfigurationOriginal(id domain.ID, value NativeConfigurationOriginal) error {
	if id.Validate() != nil {
		return domain.NativeConfigurationInvalid()
	}
	old, found, err := t.NativeConfigurationOriginal(id)
	if err != nil {
		return err
	}
	if found && old != value {
		return domain.NativeConfigurationChanged()
	}
	return t.putMCPMetadata("native-import-original-v1:"+string(id), value)
}
