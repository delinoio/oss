// SPDX-License-Identifier: Apache-2.0
package workspace

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// NativeAppsReadRequest observes the exact original live native process. It is
// never a filesystem query, a provider login or permission to call a connector.
type NativeAppsReadRequest struct {
	Scope            domain.NativeAppScope `json:"scope"`
	ForceRefresh     bool                  `json:"force_refresh"`
	WorkerDeviceID   domain.ID             `json:"worker_device_id"`
	WorkerInstanceID domain.ID             `json:"worker_instance_id"`
}

func (v NativeAppsReadRequest) Validate() error {
	if v.Scope.Validate() != nil || v.WorkerDeviceID.Validate() != nil || v.WorkerInstanceID.Validate() != nil {
		return domain.NativeAppsUnavailable()
	}
	return nil
}
