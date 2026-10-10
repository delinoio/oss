// SPDX-License-Identifier: Apache-2.0
package domain

import "time"

const SessionNativeAppsV1 = "session-native-apps-v1"

// NativeAppsObservation retains only a bounded, original Worker inventory.
// Product reads cannot manufacture selection authority from client-supplied IDs.
type NativeAppsObservation struct {
	Inventory        NativeAppInventory `json:"inventory"`
	Actor            Principal          `json:"actor"`
	WorkerDeviceID   ID                 `json:"worker_device_id"`
	WorkerInstanceID ID                 `json:"worker_instance_id"`
	ObservedAt       time.Time          `json:"observed_at"`
}

func NativeAppsAssignmentScope(i ExecutionJobInput) NativeAppScope {
	return NativeAppScope{SessionID: i.SessionID, MachineID: i.MachineID, AccountID: i.AccountID, ConnectionID: i.ConnectionID, ConfigurationDigest: i.ConfigurationDigest}
}

func ValidateNativeAppsAssignment(i ExecutionJobInput, selection SessionNativeAppSelection) error {
	if selection.Validate() != nil || selection.Scope != NativeAppsAssignmentScope(i) || i.Configuration.Harness != Codex || i.Configuration.SidechatPolicy != "" || i.Fork != nil || i.SidechatRetry != nil {
		return NativeAppsUnavailable()
	}
	return nil
}
