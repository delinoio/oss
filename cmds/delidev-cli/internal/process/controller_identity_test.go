// SPDX-License-Identifier: Apache-2.0
package process

import (
	"errors"
	"os"
	"runtime"
	"testing"
)

func changedControllerBirth(birth string) string {
	switch runtime.GOOS {
	case "linux":
		return "11111111-1111-1111-1111-111111111111:1"
	case "darwin":
		return "1.000001"
	default:
		return "1:1"
	}
}
func TestControllerIdentityObservationSeparatesErrorsFromExit(t *testing.T) {
	original, err := CaptureControllerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if state := ObserveControllerIdentity(original); state != ControllerAlive {
		t.Fatal(state)
	}
	for _, test := range []struct {
		name   string
		birth  string
		err    error
		absent bool
		want   ControllerObservation
	}{
		{"same", original.Birth, nil, false, ControllerAlive},
		{"reused", changedControllerBirth(original.Birth), nil, false, ControllerExited},
		{"invalid-birth", "invalid", nil, false, ControllerUnknown},
		{"permission", "", os.ErrPermission, false, ControllerUnknown},
		{"missing-inspection", "", os.ErrNotExist, false, ControllerUnknown},
		{"kernel-read", "", errors.New("synthetic kernel failure"), false, ControllerUnknown},
		{"definitive-absence", "", os.ErrNotExist, true, ControllerExited},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := observeControllerIdentity(original, func(int) (string, error) { return test.birth, test.err }, func(int, error) bool { return test.absent })
			if state != test.want {
				t.Fatal(state)
			}
		})
	}
	for _, identity := range []ControllerIdentity{{}, {PID: -1, Birth: original.Birth}, {PID: original.PID, Birth: "invalid"}, {PID: original.PID, Birth: ""}} {
		state := observeControllerIdentity(identity, func(int) (string, error) { t.Fatal("invalid identity inspected"); return "", nil }, func(int, error) bool { t.Fatal("invalid identity absence"); return true })
		if state != ControllerUnknown {
			t.Fatal(state)
		}
	}
}
func TestControllerPermissionFailureIsNotAbsence(t *testing.T) {
	if controllerAbsent(os.Getpid(), os.ErrPermission) {
		t.Fatal("permission denial proved exit")
	}
}
