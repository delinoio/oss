// SPDX-License-Identifier: Apache-2.0
//go:build linux

package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxNativeAdmissionPreservesStatxNotExist(t *testing.T) {
	root := admissionRoot(t)
	missing := filepath.Join(root, "managed", "absent")

	if _, err := CaptureNativeIdentity(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native identity capture did not preserve statx ENOENT: %v", err)
	}

	admission, err := CaptureRemovalAdmission(context.Background(), root, []string{missing})
	if err != nil {
		t.Fatalf("capture admission with an absent operand: %v", err)
	}
	found := false
	for _, entry := range admission.Entries {
		if entry.Path == "managed/absent" {
			found = true
			if entry.Identity != nil {
				t.Fatal("expected-absent operand acquired native removal authority")
			}
		}
	}
	if !found {
		t.Fatal("expected-absent operand was not retained in the admission")
	}
}
