// SPDX-License-Identifier: Apache-2.0

package clone

import (
	"errors"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsCapabilityClassification(t *testing.T) {
	for _, err := range []error{windows.ERROR_DISK_FULL, windows.ERROR_ACCESS_DENIED, windows.ERROR_CRC, windows.ERROR_INVALID_PARAMETER} {
		got := capabilityError(err)
		if errors.Is(got, ErrUnsupported) || !errors.Is(got, err) {
			t.Fatalf("misclassified %v: %v", err, got)
		}
	}
	for _, err := range []error{windows.ERROR_NOT_SAME_DEVICE, windows.ERROR_NOT_SUPPORTED, windows.ERROR_INVALID_FUNCTION} {
		if !errors.Is(capabilityError(err), ErrUnsupported) {
			t.Fatalf("missing capability error: %v", err)
		}
	}
}

func TestWindowsIOCTLLayout(t *testing.T) {
	var data duplicateExtents
	if unsafe.Sizeof(data) != 32 || unsafe.Offsetof(data.SourceOffset) != 8 || unsafe.Offsetof(data.ByteCount) != 24 {
		t.Fatalf("invalid x64 DUPLICATE_EXTENTS_DATA layout: %+v", data)
	}
	if unsafe.Sizeof(integrityInformation{}) != 16 {
		t.Fatal("invalid integrity information layout")
	}
}
