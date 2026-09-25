//go:build windows

package process

import (
	"fmt"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"golang.org/x/sys/windows"
)

func TestWindowsNativeLoaderFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want domain.Code
	}{
		{windows.ERROR_FILE_NOT_FOUND, domain.NotFound},
		{windows.ERROR_PATH_NOT_FOUND, domain.NotFound},
		{windows.ERROR_ACCESS_DENIED, domain.PermissionDenied},
		{windows.ERROR_BAD_FORMAT, domain.Unsupported},
		{windows.ERROR_INVALID_MODULETYPE, domain.Unsupported},
		{windows.ERROR_INVALID_EXE_SIGNATURE, domain.Unsupported},
		{windows.ERROR_EXE_MARKED_INVALID, domain.Unsupported},
		{windows.ERROR_BAD_EXE_FORMAT, domain.Unsupported},
		{windows.ERROR_EXE_MACHINE_TYPE_MISMATCH, domain.Unsupported},
		{windows.ERROR_IMAGE_SUBSYSTEM_NOT_PRESENT, domain.Unsupported},
		{windows.ERROR_NOT_ENOUGH_MEMORY, domain.Unavailable},
	} {
		wrapped := fmt.Errorf("private OS diagnostic: %w", tc.err)
		failure := launchFailure(windowsLaunchCode(wrapped))
		if failure.Code != tc.want {
			t.Fatalf("native status %v: %s, want %s", tc.err, failure.Code, tc.want)
		}
		if failure.Cause != "" {
			t.Fatal("native diagnostic retained")
		}
	}
}
