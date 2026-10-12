// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// SessionDeletionIdentity names one original Worker obligation. Its key only
// separates durable metadata; cleanup still requires the full immutable work.
type SessionDeletionIdentity struct {
	ServerID   ID `json:"server_id"`
	SessionID  ID `json:"session_id"`
	DeletionID ID `json:"deletion_id"`
	MachineID  ID `json:"machine_id"`
	DeviceID   ID `json:"device_id"`
}

func (w SessionDeletionWork) Identity() SessionDeletionIdentity {
	return SessionDeletionIdentity{w.ServerID, w.SessionID, w.DeletionID, w.MachineID, w.DeviceID}
}
func (i SessionDeletionIdentity) Validate() error {
	for _, id := range []ID{i.ServerID, i.SessionID, i.DeletionID, i.MachineID, i.DeviceID} {
		if id.Validate() != nil {
			return SessionDeletionPending()
		}
	}
	return nil
}
func (i SessionDeletionIdentity) Key() (string, error) {
	if i.Validate() != nil {
		return "", SessionDeletionPending()
	}
	raw, _ := json.Marshal(i)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}
