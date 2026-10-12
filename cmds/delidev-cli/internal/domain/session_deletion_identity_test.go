// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestSessionDeletionIdentitySeparatesEveryOriginalOwner(t *testing.T) {
	original := SessionDeletionIdentity{NewID(), NewID(), NewID(), NewID(), NewID()}
	key, err := original.Key()
	if err != nil || len(key) != 64 {
		t.Fatal("invalid obligation key", err)
	}
	for _, field := range []string{"server", "session", "deletion", "machine", "device"} {
		changed := original
		switch field {
		case "server":
			changed.ServerID = NewID()
		case "session":
			changed.SessionID = NewID()
		case "deletion":
			changed.DeletionID = NewID()
		case "machine":
			changed.MachineID = NewID()
		case "device":
			changed.DeviceID = NewID()
		}
		next, err := changed.Key()
		if err != nil || next == key {
			t.Fatal("changed owner shared proof key", field, err)
		}
	}
	changed := original
	changed.DeviceID = "../../foreign"
	if _, err := changed.Key(); err == nil {
		t.Fatal("unvalidated path operand accepted")
	}
}
